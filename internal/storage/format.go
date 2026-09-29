package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/ulikunitz/xz"

	"atlas-notes/internal/vaultlock"
)

// Compression is how a vault stores its notes on disk. It is a property of the
// vault and not of the device, because a vault is a folder that syncs between
// machines, and every one of them has to write what the others can read.
type Compression string

const (
	// CompressionNone stores a note as plain Markdown. It is the default: a
	// vault of plain files can be read, searched and repaired with any tool.
	CompressionNone Compression = "none"
	CompressionZstd Compression = "zstd"
	CompressionGzip Compression = "gzip"
	CompressionXZ   Compression = "xz"
)

// Compressions lists every format, in the order a settings list shows them.
func Compressions() []Compression {
	return []Compression{CompressionNone, CompressionZstd, CompressionGzip, CompressionXZ}
}

// ParseCompression reads a format's name. Anything it does not recognise, an
// empty string included, is CompressionNone: a settings file written by a newer
// version, or edited by hand, must still leave the vault readable.
func ParseCompression(name string) Compression {
	c := Compression(strings.ToLower(strings.TrimSpace(name)))
	for _, known := range Compressions() {
		if c == known {
			return c
		}
	}
	return CompressionNone
}

// ext is the file extension a note in this format carries. It is how a note is
// read back, so it is what says which decoder to use.
func (c Compression) ext() string {
	switch c {
	case CompressionZstd:
		return ".md.zst"
	case CompressionGzip:
		return ".md.gz"
	case CompressionXZ:
		return ".md.xz"
	}
	return ".md"
}

// vaultFileName holds the vault's settings, beside the notes they describe. It
// lives in the vault for the reason the lock file does: the vault is what gets
// copied, synced and restored, and its format has to go with it.
const vaultFileName = ".atlas-vault.json"

type vaultSettings struct {
	Compression Compression `json:"compression"`
}

// loadCompression reads the vault's format. A vault with no settings file, or
// one that cannot be read or understood, is plain Markdown.
func loadCompression(vault string) Compression {
	data, err := os.ReadFile(filepath.Join(vault, vaultFileName))
	if err != nil {
		return CompressionNone
	}
	var v vaultSettings
	if json.Unmarshal(data, &v) != nil {
		return CompressionNone
	}
	return ParseCompression(string(v.Compression))
}

// settingsModTime is when the vault's settings file last changed, or the zero
// time when there is none. Comparing it is how a change is noticed without
// reading the file every time.
func settingsModTime(vault string) time.Time {
	info, err := os.Stat(filepath.Join(vault, vaultFileName))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// refreshCompression picks up a format change made on another device. The
// settings file syncs with the vault, and if it were read only at start-up this
// device would go on saving in the old format until it was restarted, leaving
// the vault with notes in two. It costs a stat, and the file is read again only
// when its time has changed.
func (s *Store) refreshCompression() {
	mod := settingsModTime(s.VaultPath)
	s.formatMu.RLock()
	same := mod.Equal(s.formatMod)
	s.formatMu.RUnlock()
	if same {
		return
	}
	c := loadCompression(s.VaultPath)
	s.formatMu.Lock()
	s.compression, s.formatMod = c, mod
	s.formatMu.Unlock()
}

// Compression is the format the vault writes new and saved notes in.
func (s *Store) Compression() Compression {
	s.formatMu.RLock()
	defer s.formatMu.RUnlock()
	return s.compression
}

// SetCompression changes the format the vault writes in. It converts nothing:
// notes already on disk stay as they are, and stay readable, until they are
// saved or ConvertVault is run.
func (s *Store) SetCompression(c Compression) error {
	if ParseCompression(string(c)) != c {
		return fmt.Errorf("unknown compression %q", c)
	}
	// Held so that a save never sees the format change between choosing a file
	// name and writing it.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	data, err := json.Marshal(vaultSettings{Compression: c})
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.VaultPath, vaultFileName), data); err != nil {
		return err
	}
	mod := settingsModTime(s.VaultPath)
	s.formatMu.Lock()
	s.compression, s.formatMod = c, mod
	s.formatMu.Unlock()
	return nil
}

// errNoteTooLarge is what a note that would need more than maxNoteBytes to hold
// is refused with, in whatever form it arrives.
var errNoteTooLarge = fmt.Errorf("the note is larger than %d MiB", maxNoteBytes>>20)

// readFileCapped reads a note file, refusing one bigger than maxNoteBytes. A
// note can arrive from a synced folder or a backup, so nothing about its size
// is taken on trust.
func readFileCapped(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if info, err := f.Stat(); err == nil {
		if info.Size() > maxNoteBytes {
			return nil, errNoteTooLarge
		}
		buf.Grow(int(info.Size()) + 1)
	}
	if _, err := buf.ReadFrom(io.LimitReader(f, maxNoteBytes+1)); err != nil {
		return nil, err
	}
	if buf.Len() > maxNoteBytes {
		return nil, errNoteTooLarge
	}
	return buf.Bytes(), nil
}

// readAllCapped reads a decompressor to its end, failing rather than growing
// past maxNoteBytes. This is what turns a few kilobytes of gzip or xz that
// expand to gigabytes into an error instead of an out-of-memory kill; zstd
// gets the same protection from its decoder's memory limit.
func readAllCapped(r io.Reader) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, maxNoteBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxNoteBytes {
		return nil, errNoteTooLarge
	}
	return out, nil
}

// decode turns a note's bytes on disk into its text, by the format they are in.
func (s *Store) decode(c Compression, data []byte) ([]byte, error) {
	switch c {
	case CompressionZstd:
		return s.dec.DecodeAll(data, nil)
	case CompressionGzip:
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readAllCapped(r)
	case CompressionXZ:
		r, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return readAllCapped(r)
	}
	if len(data) > maxNoteBytes {
		return nil, errNoteTooLarge
	}
	return data, nil
}

// encode is decode's opposite: the bytes to put on disk for a note's text.
func (s *Store) encode(c Compression, text []byte) ([]byte, error) {
	switch c {
	case CompressionZstd:
		return s.enc.EncodeAll(text, nil), nil
	case CompressionGzip:
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(text); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case CompressionXZ:
		var buf bytes.Buffer
		w, err := xz.NewWriter(&buf)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(text); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return text, nil
}

// The first bytes of each format, which is how a locked note's payload says
// what it is. A locked note keeps the payload it has always had, zstd inside
// the seal, so that older versions of the app and the phone can still open it.
// A payload of any other format is read too, so nothing that ends up in a seal
// can be unreadable. Text cannot begin with any of these: each starts a
// sequence that is not valid UTF-8.
var (
	zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}
	gzipMagic = []byte{0x1F, 0x8B}
	xzMagic   = []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}
)

// decodePayload reads what a seal held: by its magic bytes when it has them,
// and as the text itself when it does not.
func (s *Store) decodePayload(payload []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(payload, zstdMagic):
		return s.decode(CompressionZstd, payload)
	case bytes.HasPrefix(payload, gzipMagic):
		return s.decode(CompressionGzip, payload)
	case bytes.HasPrefix(payload, xzMagic):
		return s.decode(CompressionXZ, payload)
	}
	return s.decode(CompressionNone, payload)
}

// plainCompression says which format a file name is a note in, by its
// extension. A name that is nothing but the extension is not a note.
func plainCompression(name string) (Compression, bool) {
	for _, c := range Compressions() {
		if strings.HasSuffix(name, c.ext()) && len(name) > len(c.ext()) {
			return c, true
		}
	}
	return "", false
}

// plainFile is one place an unlocked note may be, and the format it is in.
type plainFile struct {
	c    Compression
	path string
}

// plainFiles lists every place an unlocked note could be, the vault's own
// format first. It is the one place that order is decided: a note is normally
// in the vault's format, but a vault that changed format, or one synced from a
// machine that had not, holds notes in the others until they are converted, and
// everything that looks for a note's file has to look there too.
func (s *Store) plainFiles(rel string) ([]plainFile, error) {
	base, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	own := s.Compression()
	files := []plainFile{{own, base + own.ext()}}
	for _, c := range Compressions() {
		if c != own {
			files = append(files, plainFile{c, base + c.ext()})
		}
	}
	return files, nil
}

// existingPlain lists the forms of a note that are on disk, the most recently
// modified first. Forms with the same time keep the order of plainFiles, so the
// vault's own format wins a tie.
func (s *Store) existingPlain(rel string) ([]plainFile, error) {
	files, err := s.plainFiles(rel)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		f   plainFile
		mod time.Time
	}
	var have []candidate
	for _, f := range files {
		info, err := os.Stat(f.path)
		if os.IsNotExist(err) {
			continue
		}
		var mod time.Time
		if err == nil {
			mod = info.ModTime()
		}
		have = append(have, candidate{f, mod})
	}
	sort.SliceStable(have, func(i, j int) bool { return have[i].mod.After(have[j].mod) })
	out := make([]plainFile, len(have))
	for i, c := range have {
		out[i] = c.f
	}
	return out, nil
}

// newestPlain is the path of the form a note is read from, or "" if it has no
// unlocked form.
func (s *Store) newestPlain(rel string) string {
	if have, err := s.existingPlain(rel); err == nil && len(have) > 0 {
		return have[0].path
	}
	return ""
}

// readPlain reads and decodes an unlocked note. When the note has no file it
// fails as a missing file does, so that the caller can go on to look for the
// locked form.
//
// When a sync has left several forms of the note, the most recently modified is
// the one read: it is the one somebody last saved. A form that will not decode
// does not stop the others being tried, and its error is only what is returned
// when none of them can be read.
func (s *Store) readPlain(rel string) ([]byte, error) {
	// Twice, because a conversion can move the note from one form to another
	// between listing the files and reading them, and the note is then still
	// there, just under another name.
	for attempt := 0; attempt < 2; attempt++ {
		have, err := s.existingPlain(rel)
		if err != nil {
			return nil, err
		}
		var first error
		for _, f := range have {
			data, err := readFileCapped(f.path)
			if os.IsNotExist(err) {
				continue
			}
			if err == nil {
				var text []byte
				if text, err = s.decode(f.c, data); err == nil {
					return text, nil
				}
			}
			if first == nil {
				first = err
			}
		}
		if first != nil {
			return nil, first
		}
	}
	return nil, &fs.PathError{Op: "open", Path: s.notePath(rel), Err: fs.ErrNotExist}
}

// removePlain deletes every unlocked form of a note except keep, which may be
// empty to keep none. A form that is not there is already gone. text is what the
// note now says, and key is the vault's key when the note is being written
// locked, so that what is kept of a differing copy is not left in the clear.
//
// A form that says something else is not just deleted: two devices can each have
// saved the note, and the sync leaves both. It is kept as a note of its own
// first, see saveConflict. The exception is base, the form the note was read
// from before this write, which is what the new text is an edit of: saving a
// note that is still in an older format would otherwise make a conflict of every
// edit. A form that cannot be read cannot be kept as text, so it is left where
// it is and reported.
func (s *Store) removePlain(rel, keep, base string, text []byte, key vaultlock.Key) error {
	files, err := s.plainFiles(rel)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.path == keep {
			continue
		}
		data, err := readFileCapped(f.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s cannot be read, so it was not removed: %w", filepath.Base(f.path), err)
		}
		got, err := s.decode(f.c, data)
		if err != nil {
			return fmt.Errorf("%s cannot be read, so it was not removed: %w", filepath.Base(f.path), err)
		}
		if f.path != base && !bytes.Equal(got, text) {
			if err := s.saveConflict(rel, got, key); err != nil {
				return fmt.Errorf("keeping the differing copy in %s: %w", filepath.Base(f.path), err)
			}
		}
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// saveConflict keeps the text of a copy of a note that is about to be removed as
// a note of its own, beside the note: "Plan (conflict 2026-09-29 1405)", or with
// a number after it when that is taken. It is written in the vault's format, or
// sealed when key is set, and indexed so that it is seen. The caller holds
// writeMu.
func (s *Store) saveConflict(rel string, text []byte, key vaultlock.Key) error {
	now := time.Now()
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	conflict := s.UniqueName(folder, path.Base(rel)+" (conflict "+now.Format("2006-01-02 1504")+")")
	if key != nil {
		sealed, err := vaultlock.Seal(key, s.enc.EncodeAll(text, nil))
		if err != nil {
			return err
		}
		abs, err := s.lockedPathSafe(conflict)
		if err != nil {
			return err
		}
		if err := atomicWrite(abs, sealed); err != nil {
			return err
		}
		if err := s.indexNote(conflict, now, true, false); err != nil {
			log.Printf("atlas-notes: indexing %q: %v", conflict, err)
		}
		return nil
	}
	data, err := s.encode(s.Compression(), text)
	if err != nil {
		return err
	}
	abs, err := s.notePathSafe(conflict)
	if err != nil {
		return err
	}
	if err := atomicWrite(abs, data); err != nil {
		return err
	}
	// The copy is saved by now, so failing to index it is not a failure to keep it.
	if err := s.indexNote(conflict, now, false, HasTasks(string(text))); err != nil {
		log.Printf("atlas-notes: indexing %q: %v", conflict, err)
	} else if err := s.indexWritten(conflict, string(text)); err != nil {
		log.Printf("atlas-notes: indexing %q: %v", conflict, err)
	}
	return nil
}

// noteTaken reports whether a name is in use, in any form, locked or not.
func (s *Store) noteTaken(rel string) bool {
	files, err := s.plainFiles(rel)
	if err != nil {
		return false
	}
	for _, f := range files {
		if _, err := os.Lstat(f.path); err == nil {
			return true
		}
	}
	return s.IsNoteLocked(rel)
}

// NoteExists reports whether a note of that name exists, in any form.
func (s *Store) NoteExists(rel string) bool { return s.noteTaken(normalizeRel(rel)) }

// FolderExists reports whether a folder of that name exists in the vault.
func (s *Store) FolderExists(rel string) bool {
	abs, err := s.resolveFolder(rel)
	if err != nil || abs == s.VaultPath {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && info.IsDir()
}

// NoteFileName is the file a note is stored in, relative to the vault, as it is
// now: "Work/Todo.md", or with the extension of whatever format it is in.
func (s *Store) NoteFileName(rel string) string {
	rel = normalizeRel(rel)
	if files, err := s.plainFiles(rel); err == nil {
		for _, f := range files {
			if _, err := os.Stat(f.path); err == nil {
				return rel + f.c.ext()
			}
		}
	}
	if s.IsNoteLocked(rel) {
		return rel + lockedExt
	}
	return rel + s.Compression().ext()
}

// toolDirs are the folders other programs keep inside a vault: Syncthing's old
// versions and its marker, git, the trash folders of file managers, and the
// settings of Obsidian and Resilio Sync. What is in them is a copy or not a
// note, and would show up as a note twice.
var toolDirs = map[string]bool{
	".stversions": true, ".stfolder": true, ".git": true, ".trash": true,
	".Trash": true, ".obsidian": true, ".sync": true,
}

// hiddenDir reports whether a walk of the vault should stay out of a directory:
// one of toolDirs, by name and at any depth, or the top-level folder the app
// keeps images in. Only those: a folder of the user's own that happens to begin
// with a dot is a folder of notes. The vault itself is never skipped, whatever
// it is called.
func (s *Store) hiddenDir(p string, d fs.DirEntry) bool {
	if !d.IsDir() || p == s.VaultPath {
		return false
	}
	name := d.Name()
	if toolDirs[name] || strings.HasPrefix(name, ".Trash-") {
		return true
	}
	return name == attachmentsDir && filepath.Dir(p) == s.VaultPath
}

// isAppleDouble reports whether a file name is macOS's companion to a file on a
// volume that cannot hold its extra data. It carries the note's extension and is
// not a note.
func isAppleDouble(name string) bool { return strings.HasPrefix(name, "._") }

// walkPlain calls fn for every unlocked note file in the vault, without
// opening any of them.
func (s *Store) walkPlain(fn func(p string, c Compression) error) error {
	return filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if s.hiddenDir(p, d) {
			return filepath.SkipDir
		}
		if d.IsDir() || isAppleDouble(d.Name()) {
			return nil
		}
		if _, locked, c, ok := relFromFileName(d.Name()); ok && !locked {
			return fn(p, c)
		}
		return nil
	})
}

// NeedsConversion reports whether any unlocked note is stored in a format other
// than the vault's. It only lists directories and stops at the first such note,
// so it is cheap enough to ask at every launch.
func (s *Store) NeedsConversion() bool {
	own := s.Compression()
	found := false
	s.walkPlain(func(_ string, c Compression) error {
		if c != own {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// ConvertVault rewrites every unlocked note that is not in the vault's format
// into it, and returns how many it converted. progress, when set, is told after
// each note how many are done out of how many there were to do.
//
// The write lock is held for one note at a time, so saving carries on while a
// large vault is converted. A note's modification time is carried over, so the
// index and the Recent list do not see a conversion as an edit. Locked notes are
// skipped: they are one format whatever the vault's is.
//
// A note that cannot be converted is left as it is, and does not stop the rest;
// the first failure is reported once the others are done. Cancelling ctx stops
// between notes and is not an error, as for ResolveContent: what is left is
// found again next time.
func (s *Store) ConvertVault(ctx context.Context, progress func(done, total int)) (int, error) {
	s.refreshCompression()
	own := s.Compression()
	type job struct {
		path string
		from Compression
	}
	var jobs []job
	if err := s.walkPlain(func(p string, c Compression) error {
		if c != own {
			jobs = append(jobs, job{p, c})
		}
		return nil
	}); err != nil {
		return 0, err
	}

	converted, failed := 0, 0
	var first error
	for i, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		ok, err := s.convertNote(j.path, j.from)
		if err != nil {
			failed++
			if first == nil {
				first = fmt.Errorf("%s: %w", j.path, err)
			}
		} else if ok {
			converted++
		}
		if progress != nil {
			progress(i+1, len(jobs))
		}
	}
	if first != nil {
		return converted, fmt.Errorf("%d notes could not be converted, the first: %w", failed, first)
	}
	return converted, nil
}

// convertNote converts one note file, holding the write lock throughout. It
// reports whether it did anything: a note that is locked, gone, or already in
// the vault's format by the time its turn comes is left alone.
func (s *Store) convertNote(old string, from Compression) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Read again under the lock: the format may have been changed, or the note
	// saved, since the list was made.
	to := s.Compression()
	if from == to {
		return false, nil
	}
	base := strings.TrimSuffix(old, from.ext())
	if _, err := os.Stat(base + lockedExt); err == nil {
		return false, nil
	}
	info, err := os.Stat(old)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	data, err := readFileCapped(old)
	if err != nil {
		return false, err
	}
	text, err := s.decode(from, data)
	if err != nil {
		return false, err
	}

	target := base + to.ext()
	if _, err := os.Stat(target); err == nil {
		// The note is already there in the vault's format: a conversion that was
		// interrupted before it removed the old file, or a copy that arrived by
		// sync. It is never written over, since it may be the newer one. If it
		// says the same thing, the old file is all that is left to do. If it
		// says something else the old file is kept as a note of its own before
		// it goes, so that neither text is lost and both notes are not left
		// behind to be converted again.
		if existing, err := readFileCapped(target); err == nil {
			if got, err := s.decode(to, existing); err == nil {
				if !bytes.Equal(got, text) {
					relFile, rerr := filepath.Rel(s.VaultPath, old)
					if rerr != nil {
						return false, rerr
					}
					stem, _, _, _ := relFromFileName(filepath.ToSlash(relFile))
					if err := s.saveConflict(normalizeRel(stem), text, nil); err != nil {
						return false, err
					}
				}
				return true, os.Remove(old)
			}
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	out, err := s.encode(to, text)
	if err != nil {
		return false, err
	}
	if err := atomicWrite(target, out); err != nil {
		return false, err
	}
	// Not being able to set the time is not a reason to keep two copies. Some
	// shared storage will not, and the cost is that the note reads as edited.
	os.Chtimes(target, info.ModTime(), info.ModTime())
	if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}
