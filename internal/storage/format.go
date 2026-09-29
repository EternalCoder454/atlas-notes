package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/gzip"
	"github.com/ulikunitz/xz"
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
	s.formatMu.Lock()
	s.compression = c
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

// stripNoteExt removes a note's extension, whichever of the plain ones it is.
func stripNoteExt(name string) string {
	if c, ok := plainCompression(name); ok {
		return strings.TrimSuffix(name, c.ext())
	}
	return name
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

// readPlain reads and decodes an unlocked note. When the note has no file it
// fails as a missing file does, so that the caller can go on to look for the
// locked form.
func (s *Store) readPlain(rel string) ([]byte, error) {
	files, err := s.plainFiles(rel)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := readFileCapped(f.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return s.decode(f.c, data)
	}
	return nil, &fs.PathError{Op: "open", Path: files[0].path, Err: fs.ErrNotExist}
}

// removePlain deletes every unlocked form of a note except keep, which may be
// empty to keep none. A form that is not there is already gone.
func (s *Store) removePlain(rel, keep string) error {
	files, err := s.plainFiles(rel)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.path == keep {
			continue
		}
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return err
		}
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

// hiddenDir reports whether a walk of the vault should stay out of a
// directory: one whose name starts with a dot, which is where Syncthing keeps
// old versions (.stversions), and git and trash folders keep theirs. Notes in
// there are copies, and would show up as notes twice. The vault itself is
// never skipped, whatever it is called.
func (s *Store) hiddenDir(p string, d fs.DirEntry) bool {
	return d.IsDir() && p != s.VaultPath && strings.HasPrefix(d.Name(), ".")
}

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
		if d.IsDir() {
			return nil
		}
		if c, ok := plainCompression(d.Name()); ok {
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
		// says the same thing, the old file is all that is left to do.
		if existing, err := readFileCapped(target); err == nil {
			if got, err := s.decode(to, existing); err == nil && bytes.Equal(got, text) {
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
