package storage

import (
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

// indexNote upserts a note's metadata row.
func (s *Store) indexNote(rel string, modified time.Time) error {
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	_, err := s.db.Exec(`
		INSERT INTO notes(path, folder, title, modified_at, created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			folder      = excluded.folder,
			title       = excluded.title,
			modified_at = excluded.modified_at`,
		rel, folder, deriveTitle(rel), modified.Unix(), modified.Unix())
	return err
}

// ListNotes returns all indexed notes ordered by folder then title.
func (s *Store) ListNotes() ([]NoteMeta, error) {
	rows, err := s.db.Query(`SELECT path, folder, title, modified_at, created_at FROM notes ORDER BY folder, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteMeta
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

// deriveTitle is a note's display title: its file base name. The filename is the
// title (it's what the editor's title field edits and renames); an in-body "# H1"
// is treated as content, not as the title, so the tree label always matches the
// title field above the editor.
func deriveTitle(rel string) string {
	return path.Base(rel)
}
