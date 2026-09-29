package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"atlas-notes/internal/markup"
	"atlas-notes/internal/vaultlock"
)

// A locked note is stored under a different extension from an ordinary one, so
// the vault scan can tell them apart without opening every file, and so that
// what is on disk never silently disagrees with what the index believes.
const lockedExt = ".md.enc"

// lockFileName holds the salt and verifier, beside the notes it protects. It
// lives in the vault rather than in the config directory because the vault is
// the source of truth: a vault copied to another machine, or restored from a
// backup, has to still open with its own password.
const lockFileName = ".atlas-lock.json"

// folderMarkerName marks a folder as locked, so a note created in it later is
// locked too. A marker file rather than a row in the index, because the index
// is a rebuildable cache and this is a decision the user made.
const folderMarkerName = ".atlas-locked"

// ErrLocked is returned when a note's content is asked for while the vault is
// locked. It is not a failure to report as an error; it is the cue to ask for
// the password.
var ErrLocked = errors.New("this note is locked")

// ErrNoPassword is returned when a lock operation is attempted on a vault that
// has never had a password set.
var ErrNoPassword = errors.New("no vault password has been set")

// lockConfigPath is where the salt and verifier live.
func (s *Store) lockConfigPath() string { return filepath.Join(s.VaultPath, lockFileName) }

// loadLockConfig reads the vault's lock configuration, if it has one. A vault
// with no locked notes has no lock file and this returns nil.
func (s *Store) loadLockConfig() (*vaultlock.Config, error) {
	data, err := os.ReadFile(s.lockConfigPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return vaultlock.Parse(data)
}

// HasPassword reports whether a vault password has ever been set.
func (s *Store) HasPassword() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	return s.lockCfg != nil
}

// IsUnlocked reports whether the password has been given this session, so
// locked notes can be read and written.
func (s *Store) IsUnlocked() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	return s.lockKey != nil
}

// SetPassword sets the vault's password for the first time. It refuses to
// replace an existing one: every locked note is sealed with the key derived
// from it, and changing it means re-sealing them all (see ChangePassword).
func (s *Store) SetPassword(password string) error {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lockCfg != nil {
		return errors.New("this vault already has a password")
	}
	cfg, key, err := vaultlock.New(password)
	if err != nil {
		return err
	}
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := atomicWrite(s.lockConfigPath(), data); err != nil {
		return err
	}
	s.lockCfg, s.lockKey = cfg, key
	return nil
}

// Unlock derives the key from password and holds it for this session.
func (s *Store) Unlock(password string) error {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lockCfg == nil {
		return ErrNoPassword
	}
	key, err := s.lockCfg.Unlock(password)
	if err != nil {
		return err
	}
	s.lockKey = key
	return nil
}

// Lock forgets the key. Locked notes cannot be read again until the password
// is given. It is called on shutdown, and can be called from the interface.
func (s *Store) Lock() {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	s.lockKey = nil
}

// key returns the session key, or ErrLocked.
func (s *Store) key() (vaultlock.Key, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lockCfg == nil {
		return nil, ErrNoPassword
	}
	if s.lockKey == nil {
		return nil, ErrLocked
	}
	return s.lockKey, nil
}

// lockedPathSafe is notePathSafe for the encrypted form of a note.
func (s *Store) lockedPathSafe(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	return abs + lockedExt, nil
}

// IsNoteLocked reports whether a note is stored encrypted. It answers from the
// filesystem, not the index, because that is what determines how the note has
// to be read.
func (s *Store) IsNoteLocked(rel string) bool {
	abs, err := s.lockedPathSafe(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// LockNote encrypts a note in place. The plaintext file is removed only once
// the encrypted one is safely on disk.
func (s *Store) LockNote(rel string) error {
	if err := s.lockNote(rel); err != nil {
		return err
	}
	return s.purgeRemovedWords()
}

// lockNote is LockNote without the purge, so that locking a folder can lock
// every note in it and purge once at the end.
func (s *Store) lockNote(rel string) error {
	key, err := s.key()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.refreshCompression()

	rel = normalizeRel(rel)
	lockedAbs, err := s.lockedPathSafe(rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(lockedAbs); err == nil {
		// Already locked. Its images are still looked at, because a lock that
		// stopped at an image leaves the note locked and the image in the clear,
		// and asking again has to finish the job rather than report success.
		text, err := s.readLockedNote(rel)
		if err != nil {
			return err
		}
		if err := s.sealHistory(rel, key); err != nil {
			return fmt.Errorf("locked, but its earlier versions are not: %w", err)
		}
		return s.sealNoteImages(rel, text, key)
	}
	// The note is found in whatever format it is in, and sealed as zstd: a
	// locked note's payload does not follow the vault's format, so that older
	// versions of the app and the phone can still open it.
	text, err := s.readPlain(rel)
	if err != nil {
		return err
	}
	sealed, err := vaultlock.Seal(key, s.enc.EncodeAll(text, nil))
	if err != nil {
		return err
	}
	if err := atomicWrite(lockedAbs, sealed); err != nil {
		return err
	}
	if err := s.removePlain(rel, "", "", text, key); err != nil {
		// The note is readable either way; leaving both would be worse than
		// reporting it, because the plaintext would still be on disk.
		return fmt.Errorf("locked, but the unencrypted copy is still there: %w", err)
	}
	if err := s.setIndexLocked(rel, true); err != nil {
		return err
	}
	// Its earlier versions said what it said. They are kept, but sealed, so
	// that locking leaves no plaintext of the note in history either.
	if err := s.sealHistory(rel, key); err != nil {
		return fmt.Errorf("locked, but its earlier versions are not: %w", err)
	}
	// The note's pictures are as private as its words. The note is locked by
	// now, so a failure here leaves it locked and says which image is not.
	return s.sealNoteImages(rel, string(text), key)
}

// UnlockNote decrypts a note back to an ordinary one.
func (s *Store) UnlockNote(rel string) error {
	key, err := s.key()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.refreshCompression()

	rel = normalizeRel(rel)
	plainAbs, err := s.notePathSafe(rel)
	if err != nil {
		return err
	}
	lockedAbs, err := s.lockedPathSafe(rel)
	if err != nil {
		return err
	}
	sealed, err := os.ReadFile(lockedAbs)
	if os.IsNotExist(err) {
		return nil // already unlocked
	}
	if err != nil {
		return err
	}
	payload, err := vaultlock.Open(key, sealed)
	if err != nil {
		return err
	}
	// The payload is decoded whatever it holds, and the note comes out in the
	// vault's format, not the seal's.
	text, err := s.decodePayload(payload)
	if err != nil {
		return err
	}
	data, err := s.encode(s.Compression(), text)
	if err != nil {
		return err
	}
	// A plain copy that says something else, in this format or another, is kept
	// as a note of its own before the note is put back in its place.
	if err := s.removePlain(rel, "", "", text, nil); err != nil {
		return err
	}
	if err := atomicWrite(plainAbs, data); err != nil {
		return err
	}
	if err := os.Remove(lockedAbs); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := s.setIndexLocked(rel, false); err != nil {
		return err
	}
	// A sealed image left behind still opens while the vault is unlocked, so
	// unlike locking there is nothing to retry: the note is simply as it was.
	return s.unsealNoteImages(rel, string(text), key)
}

// folderMarkerPath is the marker file inside a locked folder.
func (s *Store) folderMarkerPath(folder string) (string, error) {
	if folder == "" || folder == "." {
		return "", errors.New("the vault root cannot be locked as a folder")
	}
	abs, err := s.resolve(folder)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, folderMarkerName), nil
}

// IsFolderLocked reports whether new notes in a folder are locked on creation.
func (s *Store) IsFolderLocked(folder string) bool {
	marker, err := s.folderMarkerPath(folder)
	if err != nil {
		return false
	}
	_, err = os.Stat(marker)
	return err == nil
}

// LockFolder locks every note in a folder, including those in folders beneath
// it, and marks the folder so notes created there later are locked too.
func (s *Store) LockFolder(folder string) error {
	if _, err := s.key(); err != nil {
		return err
	}
	marker, err := s.folderMarkerPath(folder)
	if err != nil {
		return err
	}
	notes, err := s.notesUnder(folder)
	if err != nil {
		return err
	}
	for _, rel := range notes {
		if err := s.lockNote(rel); err != nil {
			return err
		}
	}
	if err := atomicWrite(marker, nil); err != nil {
		return err
	}
	return s.purgeRemovedWords()
}

// UnlockFolder decrypts every note in a folder and clears the marker.
func (s *Store) UnlockFolder(folder string) error {
	if _, err := s.key(); err != nil {
		return err
	}
	marker, err := s.folderMarkerPath(folder)
	if err != nil {
		return err
	}
	notes, err := s.notesUnder(folder)
	if err != nil {
		return err
	}
	for _, rel := range notes {
		if err := s.UnlockNote(rel); err != nil {
			return err
		}
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// notesUnder lists every note in a folder and the folders below it.
func (s *Store) notesUnder(folder string) ([]string, error) {
	folder = normalizeRel(folder)
	prefix := folder + "/"
	all, err := s.ListNotes()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range all {
		if strings.HasPrefix(n.Path, prefix) {
			out = append(out, n.Path)
		}
	}
	return out, nil
}

// lockedByFolder reports whether a note should be created locked because the
// folder it is being written into is locked.
func (s *Store) lockedByFolder(rel string) bool {
	for folder := path.Dir(rel); folder != "." && folder != "/" && folder != ""; folder = path.Dir(folder) {
		if s.IsFolderLocked(folder) {
			return true
		}
	}
	return false
}

// sealedFile is one file a password change rewrites: what it holds now and what
// it will hold under the new key. The old bytes are kept so that a change that
// fails part way can put every file back.
type sealedFile struct {
	path     string
	old, new []byte
}

// writeSealed writes one file of a password change. It is a variable so that a
// test can make a write fail part way through.
var writeSealed = atomicWrite

// resealBytes opens a sealed file's contents with the old key and seals them
// again under the new one, in memory.
func resealBytes(p string, sealed []byte, oldKey, newKey vaultlock.Key) (sealedFile, error) {
	raw, err := vaultlock.Open(oldKey, sealed)
	if err != nil {
		return sealedFile{}, fmt.Errorf("re-sealing %q: %w", filepath.Base(p), err)
	}
	resealed, err := vaultlock.Seal(newKey, raw)
	if err != nil {
		return sealedFile{}, err
	}
	return sealedFile{p, sealed, resealed}, nil
}

// planNoteReseal reseals every locked note in memory. The notes are found on
// disk and not in the index: a note that arrived by sync since the last scan is
// sealed under the old key like the rest, and one left out would be lost.
func (s *Store) planNoteReseal(oldKey, newKey vaultlock.Key) ([]sealedFile, error) {
	var plan []sealedFile
	err := filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if s.hiddenDir(p, d) {
			return filepath.SkipDir
		}
		if d.IsDir() || isAppleDouble(d.Name()) {
			return nil
		}
		if _, locked, _, ok := relFromFileName(d.Name()); !ok || !locked {
			return nil
		}
		sealed, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := resealBytes(p, sealed, oldKey, newKey)
		if err != nil {
			return err
		}
		plan = append(plan, f)
		return nil
	})
	return plan, err
}

// planAttachmentReseal reseals every sealed image in the vault's attachments
// folder in memory. It is resealAttachments without the writing.
func (s *Store) planAttachmentReseal(oldKey, newKey vaultlock.Key) ([]sealedFile, error) {
	dir := s.attachmentsPath()
	var plan []sealedFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && os.IsNotExist(err) {
				return nil // no image has ever been saved
			}
			return err
		}
		plainName := strings.TrimSuffix(d.Name(), sealedExt)
		if d.IsDir() || plainName == d.Name() || !markup.IsImagePath(plainName) {
			return nil
		}
		sealed, err := readCapped(p, maxAttachmentBytes+sealOverhead)
		if err != nil {
			return err
		}
		f, err := resealBytes(p, sealed, oldKey, newKey)
		if err != nil {
			return err
		}
		plan = append(plan, f)
		return nil
	})
	return plan, err
}

// ChangePassword re-seals everything that is sealed under a new password, all
// or nothing. Every locked note, every sealed image and every sealed version is
// opened with the old key and sealed again in memory first, and nothing is
// written until all of that has worked, so a file that will not open leaves the
// vault untouched and the old password working. Then the files are written, and
// if one of those writes fails the ones already written are put back from the
// old bytes held in memory. The new salt and verifier are written last: they are
// only in memory until then, so files under the new key without them could never
// be opened again.
//
// A sealed version of a note's history that will not open with the old key is
// left as it is and logged, and does not stop the change: it could not be read
// before, and history is a record, not the vault.
func (s *Store) ChangePassword(current, next string) error {
	if !s.HasPassword() {
		return ErrNoPassword
	}
	if err := s.Unlock(current); err != nil {
		return err
	}
	oldKey, err := s.key()
	if err != nil {
		return err
	}
	newCfg, newKey, err := vaultlock.New(next)
	if err != nil {
		return err
	}
	cfgData, err := newCfg.Marshal()
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	plan, err := s.planNoteReseal(oldKey, newKey)
	if err != nil {
		return err
	}
	// Sealed images are under the same key as the notes that refer to them.
	images, err := s.planAttachmentReseal(oldKey, newKey)
	if err != nil {
		return err
	}
	plan = append(plan, images...)
	history, herr := s.planHistoryReseal(oldKey, newKey)
	if herr != nil {
		// Versions that could not be opened stay as they are, and the rest of
		// the history is re-sealed with everything else.
		log.Printf("atlas-notes: resealing history: %v", herr)
	}
	plan = append(plan, history...)

	var written []sealedFile
	undo := func() {
		for i := len(written) - 1; i >= 0; i-- {
			if err := writeSealed(written[i].path, written[i].old); err != nil {
				log.Printf("atlas-notes: putting %s back after a failed password change: %v", written[i].path, err)
			}
		}
	}
	for _, f := range plan {
		if err := writeSealed(f.path, f.new); err != nil {
			undo()
			return fmt.Errorf("re-sealing %q: %w", filepath.Base(f.path), err)
		}
		written = append(written, f)
	}
	if err := writeSealed(s.lockConfigPath(), cfgData); err != nil {
		undo()
		return err
	}
	s.lockMu.Lock()
	s.lockCfg, s.lockKey = newCfg, newKey
	s.lockMu.Unlock()
	return nil
}
