package storage

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const noteExt = ".md.zst"

// NoteMeta is a row of indexed note metadata.
type NoteMeta struct {
	Path       string // vault-relative, no extension, e.g. "Work/Todo"
	Folder     string // parent folder, "" for root
	Title      string
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
// leading slash, no extension.
func normalizeRel(rel string) string {
	rel = strings.TrimSuffix(rel, noteExt)
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	rel = path.Clean(rel)
	if rel == "." {
		rel = ""
	}
	return rel
}

// notePath maps a vault-relative identifier to its absolute .md.zst file path.
func (s *Store) notePath(rel string) string {
	return filepath.Join(s.VaultPath, filepath.FromSlash(normalizeRel(rel))+noteExt)
}

// WriteNote compresses and atomically writes content, then refreshes the index.
// Safe to call from any goroutine (see Store.writeMu).
func (s *Store) WriteNote(rel, content string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	abs := s.notePath(rel)
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
	data, err := os.ReadFile(s.notePath(rel))
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
	if err := os.Remove(s.notePath(rel)); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM notes WHERE path = ?`, rel)
	return err
}

// RenameNote renames/moves a note; newRel may include a different folder.
func (s *Store) RenameNote(oldRel, newRel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	oldRel = normalizeRel(oldRel)
	newRel = normalizeRel(newRel)
	newAbs := s.notePath(newRel)
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	if err := os.Rename(s.notePath(oldRel), newAbs); err != nil {
		return err
	}
	folder := path.Dir(newRel)
	if folder == "." {
		folder = ""
	}
	_, err := s.db.Exec(`UPDATE notes SET path = ?, folder = ? WHERE path = ?`, newRel, folder, oldRel)
	return err
}

// upsertNoteSQL inserts or refreshes one note's metadata row. Reindex prepares
// it once and reuses it for the whole vault.
const upsertNoteSQL = `
	INSERT INTO notes(path, folder, title, modified_at, created_at)
	VALUES(?,?,?,?,?)
	ON CONFLICT(path) DO UPDATE SET
		folder      = excluded.folder,
		title       = excluded.title,
		modified_at = excluded.modified_at`

// indexNote upserts a note's metadata row.
func (s *Store) indexNote(rel string, modified time.Time) error {
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	unix := modified.Unix()
	_, err := s.db.Exec(upsertNoteSQL, rel, folder, deriveTitle(rel), unix, unix)
	return err
}

// ListNotes returns all indexed notes ordered by folder then title.
func (s *Store) ListNotes() ([]NoteMeta, error) {
	rows, err := s.db.Query(`SELECT path, folder, title, modified_at, created_at FROM notes ORDER BY folder, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, 256)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		if err := rows.Scan(&m.Path, &m.Folder, &m.Title, &modified, &created); err != nil {
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
		SELECT path, folder, title, modified_at, created_at
		FROM notes ORDER BY modified_at DESC, path LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, limit)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		if err := rows.Scan(&m.Path, &m.Folder, &m.Title, &modified, &created); err != nil {
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

// deriveTitle is a note's display title: its file base name. The filename is the
// title (it's what the editor's title field edits and renames); an in-body "# H1"
// is treated as content, not as the title, so the tree label always matches the
// title field above the editor.
func deriveTitle(rel string) string {
	return path.Base(rel)
}
