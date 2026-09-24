package storage

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	key, err := s.key()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rel = normalizeRel(rel)
	plainAbs, err := s.notePathSafe(rel)
	if err != nil {
		return err
	}
	lockedAbs, err := s.lockedPathSafe(rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(lockedAbs); err == nil {
		return nil // already locked
	}
	raw, err := os.ReadFile(plainAbs)
	if err != nil {
		return err
	}
	sealed, err := vaultlock.Seal(key, raw)
	if err != nil {
		return err
	}
	if err := atomicWrite(lockedAbs, sealed); err != nil {
		return err
	}
	if err := os.Remove(plainAbs); err != nil && !os.IsNotExist(err) {
		// The note is readable either way; leaving both would be worse than
		// reporting it, because the plaintext would still be on disk.
		return fmt.Errorf("locked, but the unencrypted copy is still there: %w", err)
	}
	return s.setIndexLocked(rel, true)
}

// UnlockNote decrypts a note back to an ordinary one.
func (s *Store) UnlockNote(rel string) error {
	key, err := s.key()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
	raw, err := vaultlock.Open(key, sealed)
	if err != nil {
		return err
	}
	if err := atomicWrite(plainAbs, raw); err != nil {
		return err
	}
	if err := os.Remove(lockedAbs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.setIndexLocked(rel, false)
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
		if err := s.LockNote(rel); err != nil {
			return err
		}
	}
	return atomicWrite(marker, nil)
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

// ChangePassword re-seals every locked note under a new password. It is done
// note by note rather than atomically, so a failure part way through is
// reported with the old password still working for whatever is left.
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

	locked, err := s.LockedNotes()
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, rel := range locked {
		abs, err := s.lockedPathSafe(rel)
		if err != nil {
			return err
		}
		sealed, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		raw, err := vaultlock.Open(oldKey, sealed)
		if err != nil {
			return fmt.Errorf("re-sealing %q: %w", rel, err)
		}
		resealed, err := vaultlock.Seal(newKey, raw)
		if err != nil {
			return err
		}
		if err := atomicWrite(abs, resealed); err != nil {
			return err
		}
	}

	data, err := newCfg.Marshal()
	if err != nil {
		return err
	}
	if err := atomicWrite(s.lockConfigPath(), data); err != nil {
		return err
	}
	s.lockMu.Lock()
	s.lockCfg, s.lockKey = newCfg, newKey
	s.lockMu.Unlock()
	return nil
}
