package storage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"atlas-notes/internal/imagefit"
	"atlas-notes/internal/markup"
	"atlas-notes/internal/vaultlock"
)

// Pasted images are ordinary files in one folder at the top of the vault. They
// are not hidden and not packed into the notes, so a note stays plain Markdown
// that any other app can open, the images show up in sync like any other file,
// and a note's text does not grow by megabytes when a picture is added.
//
// The one exception is a locked note. Its pictures are as private as its words,
// so they are sealed with the vault key under the same name plus ".enc", and a
// note refers to them the same way whether they are sealed or not.

// attachmentsDir is the folder images are saved into.
const attachmentsDir = "attachments"

// sealedExt is added to an image's file name while it is encrypted.
const sealedExt = ".enc"

// maxAttachmentBytes caps how much of an image file is read. imagefit brings
// pasted images well under this, so a file over it did not come from the app
// and is not worth holding in memory.
const maxAttachmentBytes = 64 << 20

// sealOverhead is what sealing adds to a file: the nonce and the tag.
const sealOverhead = 64

// maxAttachmentBaseLen caps the note's share of an image's file name, so a long
// title does not push the path past what a file system or a sync tool accepts.
const maxAttachmentBaseLen = 40

// unsafeInName are the characters that a file name or a Markdown link cannot
// carry as they are. Spaces are handled with them, but need no listing here.
const unsafeInName = `()[]<>#?%*:|"/\`

// errBadImagePath is a reference that is not a picture inside the vault: a web
// address, an absolute path, or a file that is not an image at all.
var errBadImagePath = errors.New("not the path of an image in this vault")

// errAttachmentTooLarge is an image file over maxAttachmentBytes.
var errAttachmentTooLarge = fmt.Errorf("the image is larger than %d MiB", maxAttachmentBytes>>20)

// attachmentNow is the clock an image's name is stamped with. The tests replace
// it, so a name can be checked exactly instead of by its shape.
var attachmentNow = time.Now

// attachmentsPath is the absolute path of the attachments folder.
func (s *Store) attachmentsPath() string {
	return filepath.Join(s.VaultPath, attachmentsDir)
}

// SaveAttachment stores a pasted image for a note and returns the path to write
// into the note's Markdown. The image is prepared by imagefit first, so what is
// stored may be smaller than what came in and has no metadata.
//
// The path is relative to the note's own folder ("attachments/x.png" for a note
// in the root, "../attachments/x.png" one folder down), which is what other
// Markdown apps resolve links against. It has no spaces in it, so it needs no
// escaping.
//
// When the note is locked the image is sealed and written as "<name>.enc". The
// returned path still names the plain file: the note does not change when it
// is locked or unlocked, and ReadAttachment finds whichever form is there.
func (s *Store) SaveAttachment(noteRel string, data []byte) (string, error) {
	noteRel = normalizeRel(noteRel)
	if noteRel == "" {
		return "", errors.New("empty note name")
	}
	// A locked vault cannot take an image for a locked note, and finding that
	// out before the image is decoded and resized saves the wait.
	if s.noteIsLocked(noteRel) {
		if _, err := s.key(); err != nil {
			return "", err
		}
	}
	fitted, ext, err := imagefit.Fit(data)
	if err != nil {
		return "", err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Asked again under the lock, so a note that was locked while the image
	// was being prepared does not get it in the clear.
	locked := s.noteIsLocked(noteRel)
	var key vaultlock.Key
	if locked {
		if key, err = s.key(); err != nil {
			return "", err
		}
	}

	dir := s.attachmentsPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// The time, to the second, keeps a note's images in the order they were
	// added and makes a clash rare. A counter settles the ones that remain.
	stem := attachmentBase(noteRel) + "-" + attachmentNow().Format("20060102-150405")
	name := stem + ext
	for i := 2; attachmentTaken(dir, name); i++ {
		if i > 9999 {
			return "", errors.New("too many images with the same name")
		}
		name = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}

	if locked {
		sealed, err := vaultlock.Seal(key, fitted)
		if err != nil {
			return "", err
		}
		if err := atomicWrite(filepath.Join(dir, name+sealedExt), sealed); err != nil {
			return "", err
		}
	} else if err := atomicWrite(filepath.Join(dir, name), fitted); err != nil {
		return "", err
	}
	return strings.Repeat("../", strings.Count(noteRel, "/")) + attachmentsDir + "/" + name, nil
}

// noteIsLocked reports whether an image saved for a note must be sealed: the
// note is locked, or is in a locked folder and will be locked when it is saved.
func (s *Store) noteIsLocked(noteRel string) bool {
	return s.IsNoteLocked(noteRel) || s.lockedByFolder(noteRel)
}

// attachmentBase is the note's name made safe to use as the start of a file
// name. Anything a file system or a Markdown link would trip on becomes "-", a
// run of them becomes one, and the result is cut to maxAttachmentBaseLen.
func attachmentBase(noteRel string) string {
	var b strings.Builder
	n := 0
	dash := true // starts true, so nothing unsafe leads the name
	for _, r := range path.Base(noteRel) {
		if n >= maxAttachmentBaseLen {
			break
		}
		if r == '-' || unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(unsafeInName, r) {
			if !dash {
				b.WriteByte('-')
				n++
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		n++
		dash = false
	}
	// A dot at the front would hide the file, and one at the end is refused by
	// Windows.
	name := strings.Trim(b.String(), "-.")
	if name == "" {
		return "note"
	}
	return name
}

// attachmentTaken reports whether a name is in use in dir, as a plain file or
// as a sealed one. Both count, so a name is never reused because the earlier
// image happens to be encrypted.
func attachmentTaken(dir, name string) bool {
	for _, n := range []string{name, name + sealedExt} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// ReadAttachment returns the image a note refers to by mdPath, as written in its
// Markdown. A sealed image is decrypted, and a locked vault gives ErrLocked.
//
// A reference that is not a local image is refused: a web address, an absolute
// path, anything that is not an image file, and anything that leaves the vault.
// Note text can come from a synced folder, so a note must not be able to make
// the app read an arbitrary file.
//
// The path is tried relative to the note, and then in the attachments folder by
// file name alone. The second is why a note moved to another folder still shows
// its pictures, without its links being rewritten.
func (s *Store) ReadAttachment(noteRel, mdPath string) ([]byte, error) {
	candidates, err := s.attachmentCandidates(noteRel, mdPath)
	if err != nil {
		return nil, err
	}
	for _, abs := range candidates {
		data, err := s.readAttachmentFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return data, err
	}
	return nil, fmt.Errorf("%q: %w", mdPath, fs.ErrNotExist)
}

// attachmentCandidates is where a note's image reference may be found, best
// first, as absolute paths of the plain file. It is the one place that decides
// what a reference may point at, so reading, locking and unlocking agree.
func (s *Store) attachmentCandidates(noteRel, mdPath string) ([]string, error) {
	// Backslashes are read as separators, as Windows apps write them, so a
	// reference cannot mean one thing to this check and another to the file
	// system.
	p := strings.ReplaceAll(mdPath, `\`, "/")
	if strings.Contains(p, "://") || strings.ContainsRune(p, 0) ||
		strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') ||
		!markup.IsImagePath(p) {
		return nil, fmt.Errorf("%q: %w", mdPath, errBadImagePath)
	}
	folder := path.Dir(normalizeRel(noteRel)) // "." for a note in the root
	abs := filepath.Join(s.VaultPath, filepath.FromSlash(path.Join(folder, p)))
	if !withinVault(s.VaultPath, abs) {
		return nil, fmt.Errorf("%q: %w", mdPath, errOutsideVault)
	}
	// The base of a reference that passed the checks above cannot be ".." or
	// hold a separator, so this stays inside the attachments folder.
	fallback := filepath.Join(s.attachmentsPath(), path.Base(p))
	if fallback == abs {
		return []string{abs}, nil
	}
	return []string{abs, fallback}, nil
}

// ErrSealedAttachment is returned for a picture that is stored encrypted,
// when something asks for its file: there is no file anything else could open.
var ErrSealedAttachment = errors.New("this picture is encrypted with its note")

// AttachmentFile is the file a note's picture is stored in, for opening it in
// another app. A picture stored encrypted has none that another app could
// read, and returns ErrSealedAttachment: decrypting it to a file for the
// occasion would leave a copy in the clear.
func (s *Store) AttachmentFile(noteRel, mdPath string) (string, error) {
	candidates, err := s.attachmentCandidates(noteRel, mdPath)
	if err != nil {
		return "", err
	}
	for _, abs := range candidates {
		if isFile(abs) {
			return abs, nil
		}
		if isFile(abs + ".enc") {
			return "", ErrSealedAttachment
		}
	}
	return "", fmt.Errorf("%q: %w", mdPath, fs.ErrNotExist)
}

// readAttachmentFile reads an image, plain or sealed. It returns an error
// wrapping fs.ErrNotExist only when neither form is there.
func (s *Store) readAttachmentFile(abs string) ([]byte, error) {
	// The file itself is never a link (readCapped refuses one), but a folder
	// on the way to it could be: the attachments folder linked to somewhere
	// else. The folder it really is in has to be inside the vault as it
	// really is.
	if !s.reallyInVault(filepath.Dir(abs)) {
		return nil, fmt.Errorf("%s: %w", filepath.Base(abs), errOutsideVault)
	}
	data, err := readCapped(abs, maxAttachmentBytes)
	if !errors.Is(err, fs.ErrNotExist) {
		return data, err
	}
	sealed, err := readCapped(abs+sealedExt, maxAttachmentBytes+sealOverhead)
	if err != nil {
		return nil, err
	}
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	return vaultlock.Open(key, sealed)
}

// readCapped reads a regular file of at most limit bytes. It looks before it
// opens, because opening a named pipe that was put in a synced folder would
// wait for a writer that never comes.
func readCapped(p string, limit int64) ([]byte, error) {
	// Lstat, not Stat: a symbolic link is refused rather than followed, so a
	// link synced into the vault cannot make a note read a file outside it.
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(p))
	}
	if info.Size() > limit {
		return nil, errAttachmentTooLarge
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errAttachmentTooLarge
	}
	return data, nil
}

// isFile reports whether p is a regular file.
func isFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

// inAttachments reports whether abs is in the attachments folder or below it.
func (s *Store) inAttachments(abs string) bool {
	rel, err := filepath.Rel(s.attachmentsPath(), abs)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// sealNoteImages encrypts the images in the attachments folder that a note
// refers to, and removes the plain files. An image that lives anywhere else in
// the vault is the user's own file, put there on purpose, and is left alone.
//
// A failure is returned rather than skipped: a picture in the clear beside a
// locked note is exactly what the user locked it to avoid, so it has to be
// reported, not hidden.
func (s *Store) sealNoteImages(noteRel, text string, key vaultlock.Key) error {
	for _, img := range markup.Summarize(text).Images {
		candidates, err := s.attachmentCandidates(noteRel, img)
		if err != nil {
			continue // a web address, or something that is not in the vault
		}
		for _, abs := range candidates {
			if isFile(abs) {
				if s.inAttachments(abs) {
					if err := sealAttachmentFile(key, abs); err != nil {
						return fmt.Errorf("locked, but the image %q is still unencrypted: %w", img, err)
					}
				}
				break
			}
			if isFile(abs + sealedExt) {
				break // sealed already, so the fallback is not this note's image
			}
		}
	}
	return nil
}

// unsealNoteImages is sealNoteImages the other way: the sealed images in the
// attachments folder that a note refers to come back as plain files.
func (s *Store) unsealNoteImages(noteRel, text string, key vaultlock.Key) error {
	for _, img := range markup.Summarize(text).Images {
		candidates, err := s.attachmentCandidates(noteRel, img)
		if err != nil {
			continue
		}
		for _, abs := range candidates {
			if isFile(abs) {
				break // already plain
			}
			if isFile(abs + sealedExt) {
				// A picture another locked note also shows stays sealed: its
				// text can be copied between notes, and unlocking one must not
				// put the other's picture in the clear. It still shows here,
				// since a sealed picture is read while the vault is unlocked.
				if s.inAttachments(abs) && !s.lockedNoteShows(noteRel, abs) {
					if err := unsealAttachmentFile(key, abs); err != nil {
						return fmt.Errorf("unlocked, but the image %q is still encrypted: %w", img, err)
					}
				}
				break
			}
		}
	}
	return nil
}

// sealAttachmentFile writes the sealed form of a plain image and only then
// removes the plain one, so a failure part way never loses the picture.
func sealAttachmentFile(key vaultlock.Key, abs string) error {
	data, err := readCapped(abs, maxAttachmentBytes)
	if err != nil {
		return err
	}
	sealed, err := vaultlock.Seal(key, data)
	if err != nil {
		return err
	}
	if err := atomicWrite(abs+sealedExt, sealed); err != nil {
		return err
	}
	return os.Remove(abs)
}

// unsealAttachmentFile is sealAttachmentFile in reverse.
func unsealAttachmentFile(key vaultlock.Key, abs string) error {
	sealed, err := readCapped(abs+sealedExt, maxAttachmentBytes+sealOverhead)
	if err != nil {
		return err
	}
	data, err := vaultlock.Open(key, sealed)
	if err != nil {
		return err
	}
	if err := atomicWrite(abs, data); err != nil {
		return err
	}
	return os.Remove(abs + sealedExt)
}

// lockedNoteShows reports whether a locked note other than except shows the
// picture stored at abs. Locked notes are not in the link index, so each is
// read; unlocking is rare and asked for, and the vault is unlocked by then.
// A locked note that cannot be read counts as showing it, which keeps the
// picture sealed: the safe side to be wrong on.
func (s *Store) lockedNoteShows(except, abs string) bool {
	locked, err := s.LockedNotes()
	if err != nil {
		return true
	}
	for _, rel := range locked {
		if rel == normalizeRel(except) {
			continue
		}
		text, err := s.readLockedNote(rel)
		if err != nil {
			return true
		}
		for _, img := range markup.Summarize(text).Images {
			candidates, err := s.attachmentCandidates(rel, img)
			if err != nil {
				continue
			}
			for _, c := range candidates {
				if c == abs {
					return true
				}
			}
		}
	}
	return false
}

// reallyInVault reports whether dir, with every symbolic link on the way
// resolved, is inside the vault, also resolved. A folder that does not exist
// is not outside anything: there is nothing in it to read.
func (s *Store) reallyInVault(dir string) bool {
	real, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	vault, err := filepath.EvalSymlinks(s.VaultPath)
	if err != nil {
		return false
	}
	return withinVault(vault, real)
}
