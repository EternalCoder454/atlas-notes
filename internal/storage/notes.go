package storage

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const noteExt = ".md.zst"

// NoteMeta is a row of indexed note metadata. A note's title is its file name,
// which is the tail of Path, so it is not stored separately.
type NoteMeta struct {
	Path       string // vault-relative, no extension, e.g. "Work/Todo"
	Folder     string // parent folder, "" for root
	ModifiedAt time.Time
	CreatedAt  time.Time
}

// atomicWrite writes data to path durably: temp file in the same directory,
// fsync, then rename over the target.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atlas-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// normalizeRel cleans a vault-relative note identifier: forward slashes, no
// leading slash, no extension — and, critically, no way out of the vault.
// Segments that would escape it ("..") or mean nothing (".", "") are dropped
// rather than resolved, so a note titled "../../secrets" becomes "secrets"
// inside the vault instead of a file two directories above it. Names reach this
// function straight from the title field, the tree's rename prompt and a
// synced vault's own filenames, so this is the one place that has to hold.
func normalizeRel(rel string) string {
	rel = strings.TrimSuffix(rel, noteExt)
	rel = filepath.ToSlash(rel)
	var segments []string
	for _, seg := range strings.Split(rel, "/") {
		switch strings.TrimSpace(seg) {
		case "", ".", "..":
			continue
		}
		segments = append(segments, strings.TrimSpace(seg))
	}
	return strings.Join(segments, "/")
}

// errOutsideVault is returned when a path would land outside the vault. After
// normalizeRel this should be unreachable; it is kept as a second line of
// defense, because the cost of being wrong here is writing to arbitrary files.
var errOutsideVault = errors.New("path is outside the vault")

// resolve maps a vault-relative identifier to an absolute path and verifies the
// result really is inside the vault.
func (s *Store) resolve(rel string) (string, error) {
	rel = normalizeRel(rel)
	if rel == "" {
		return "", errors.New("empty note name")
	}
	abs := filepath.Join(s.VaultPath, filepath.FromSlash(rel))
	if !withinVault(s.VaultPath, abs) {
		return "", fmt.Errorf("%q: %w", rel, errOutsideVault)
	}
	return abs, nil
}

// resolveFolder is resolve for folders (which have no file extension). An empty
// folder is the vault root, which is valid.
func (s *Store) resolveFolder(rel string) (string, error) {
	rel = normalizeRel(rel)
	abs := filepath.Join(s.VaultPath, filepath.FromSlash(rel))
	if !withinVault(s.VaultPath, abs) {
		return "", fmt.Errorf("%q: %w", rel, errOutsideVault)
	}
	return abs, nil
}

// withinVault reports whether abs is the vault directory or something under it.
func withinVault(vault, abs string) bool {
	rel, err := filepath.Rel(vault, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// notePath maps a vault-relative identifier to its absolute .md.zst file path.
// It does not validate containment; callers that touch the filesystem go
// through notePathSafe.
func (s *Store) notePath(rel string) string {
	return filepath.Join(s.VaultPath, filepath.FromSlash(normalizeRel(rel))+noteExt)
}

// notePathSafe is notePath with the vault-containment check applied.
func (s *Store) notePathSafe(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	return abs + noteExt, nil
}

// WriteNote compresses and atomically writes content, then refreshes the index.
// Safe to call from any goroutine (see Store.writeMu).
func (s *Store) WriteNote(rel, content string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	abs, err := s.notePathSafe(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	compressed := s.enc.EncodeAll([]byte(content), nil)
	if err := atomicWrite(abs, compressed); err != nil {
		return err
	}
	return s.indexNote(rel, time.Now())
}

// ReadNote reads and decompresses a note's markdown.
func (s *Store) ReadNote(rel string) (string, error) {
	abs, err := s.notePathSafe(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	out, err := s.dec.DecodeAll(data, nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DeleteNote removes the file and its index row (cascading its checklist items).
func (s *Store) DeleteNote(rel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	abs, err := s.notePathSafe(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM notes WHERE path = ?`, rel)
	return err
}

// RenameNote renames/moves a note; newRel may include a different folder.
func (s *Store) RenameNote(oldRel, newRel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	oldRel = normalizeRel(oldRel)
	newRel = normalizeRel(newRel)
	oldAbs, err := s.notePathSafe(oldRel)
	if err != nil {
		return err
	}
	newAbs, err := s.notePathSafe(newRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return err
	}
	folder := path.Dir(newRel)
	if folder == "." {
		folder = ""
	}
	_, err = s.db.Exec(`UPDATE notes SET path = ?, folder = ? WHERE path = ?`, newRel, folder, oldRel)
	return err
}

// upsertNoteSQL inserts or refreshes one note's metadata row. Reindex prepares
// it once and reuses it for the whole vault.
const upsertNoteSQL = `
	INSERT INTO notes(path, folder, modified_at, created_at)
	VALUES(?,?,?,?)
	ON CONFLICT(path) DO UPDATE SET
		folder      = excluded.folder,
		modified_at = excluded.modified_at`

// indexNote upserts a note's metadata row.
func (s *Store) indexNote(rel string, modified time.Time) error {
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	unix := modified.Unix()
	_, err := s.db.Exec(upsertNoteSQL, rel, folder, unix, unix)
	return err
}

// ListNotes returns all indexed notes ordered by folder then title.
func (s *Store) ListNotes() ([]NoteMeta, error) {
	rows, err := s.db.Query(`SELECT path, folder, modified_at, created_at FROM notes ORDER BY folder, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, 256)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		if err := rows.Scan(&m.Path, &m.Folder, &modified, &created); err != nil {
			return nil, err
		}
		m.ModifiedAt = time.Unix(modified, 0)
		m.CreatedAt = time.Unix(created, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// UniqueName returns a vault-relative path for a new note in folder, appending
// a counter when the plain name is taken, so creating notes never overwrites
// one and never needs to ask for a name up front.
func (s *Store) UniqueName(folder, base string) string {
	candidate := base
	for i := 2; i < 1000; i++ {
		rel := candidate
		if folder != "" {
			rel = folder + "/" + candidate
		}
		if _, err := os.Stat(s.notePath(rel)); os.IsNotExist(err) {
			return rel
		}
		candidate = fmt.Sprintf("%s %d", base, i)
	}
	return base
}

// RecentNotes returns the most recently modified notes, newest first. The
// welcome screen uses it to offer a way straight back into recent work.
func (s *Store) RecentNotes(limit int) ([]NoteMeta, error) {
	rows, err := s.db.Query(`
		SELECT path, folder, modified_at, created_at
		FROM notes ORDER BY modified_at DESC, path LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, limit)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		if err := rows.Scan(&m.Path, &m.Folder, &modified, &created); err != nil {
			return nil, err
		}
		m.ModifiedAt = time.Unix(modified, 0)
		m.CreatedAt = time.Unix(created, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountNotes returns how many notes the index holds.
func (s *Store) CountNotes() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&n)
	return n, err
}
