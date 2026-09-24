package storage

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListFolders walks the vault and returns all subfolders (vault-relative,
// forward-slash), sorted. Empty folders are included.
func (s *Store) ListFolders() ([]string, error) {
	var folders []string
	err := filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(s.VaultPath, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		folders = append(folders, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(folders)
	return folders, nil
}

// CreateFolder creates a vault-relative folder (and any parents).
func (s *Store) CreateFolder(rel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	abs, err := s.resolveFolder(rel)
	if err != nil {
		return err
	}
	if abs == s.VaultPath {
		return errors.New("empty folder name")
	}
	return os.MkdirAll(abs, 0o755)
}

// DeleteFolder removes a folder and everything under it, pruning the index.
func (s *Store) DeleteFolder(rel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	abs, err := s.resolveFolder(rel)
	if err != nil {
		return err
	}
	if abs == s.VaultPath {
		return errors.New("refusing to delete the vault itself")
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM notes WHERE folder = ? OR folder LIKE ?`, rel, rel+"/%")
	return err
}

// RenameFolder renames/moves a folder and everything under it, updating the
// index with a single SQL statement (no file decompression) so it stays fast.
func (s *Store) RenameFolder(oldRel, newRel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	oldRel = normalizeRel(oldRel)
	newRel = normalizeRel(newRel)
	oldAbs, err := s.resolveFolder(oldRel)
	if err != nil {
		return err
	}
	newAbs, err := s.resolveFolder(newRel)
	if err != nil {
		return err
	}
	if oldAbs == s.VaultPath || newAbs == s.VaultPath {
		return errors.New("empty folder name")
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return err
	}

	// Rewrite the oldRel prefix to newRel for every note under the folder.
	oldSlash := oldRel + "/%"
	skip := len(oldRel) + 1 // SQLite substr is 1-based; skip "oldRel"
	_, err = s.db.Exec(`
		UPDATE notes
		SET path = ? || substr(path, ?),
		    folder = CASE
		        WHEN folder = ? THEN ?
		        WHEN folder LIKE ? THEN ? || substr(folder, ?)
		        ELSE folder
		    END
		WHERE path LIKE ?`,
		newRel, skip,
		oldRel, newRel,
		oldSlash, newRel, skip,
		oldSlash)
	return err
}

// Reindex brings the index in line with what is on disk: it adds notes that
// appeared, refreshes ones whose file changed, and drops rows whose file is
// gone.
//
// It is deliberately cheap, because it runs at every launch:
//
//   - the existing index is read once into a map, and a note whose file has the
//     same modification time is skipped entirely;
//   - files are never decompressed (the old version read and decompressed every
//     note just to check it was readable, which dominated startup on a large
//     vault and read the whole vault off disk);
//   - every write happens inside a single transaction against one prepared
//     statement, instead of one implicit transaction per note.
func (s *Store) Reindex() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	known, err := s.indexedModTimes()
	if err != nil {
		return err
	}

	type entry struct {
		rel      string
		modified time.Time
		locked   bool
	}
	var changed []entry
	seen := make(map[string]bool, len(known))

	err = filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Both forms of a note are indexed. Which extension a file carries is
		// what says whether it is locked, so the scan reads it off the name
		// rather than opening anything.
		locked := strings.HasSuffix(p, lockedExt)
		if !locked && !strings.HasSuffix(p, noteExt) {
			return nil
		}
		rel, rerr := filepath.Rel(s.VaultPath, p)
		if rerr != nil {
			return rerr
		}
		rel = normalizeRel(rel)
		seen[rel] = true
		modified := time.Now()
		if info, ierr := d.Info(); ierr == nil {
			modified = info.ModTime()
		}
		if prev, ok := known[rel]; ok && prev == modified.Unix() {
			return nil // unchanged since the last run
		}
		changed = append(changed, entry{rel, modified, locked})
		return nil
	})
	if err != nil {
		return err
	}

	var stale []string
	for rel := range known {
		if !seen[rel] {
			stale = append(stale, rel)
		}
	}
	if len(changed) == 0 && len(stale) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once committed

	if len(changed) > 0 {
		stmt, err := tx.Prepare(upsertNoteSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range changed {
			folder := path.Dir(e.rel)
			if folder == "." {
				folder = ""
			}
			unix := e.modified.Unix()
			if _, err := stmt.Exec(e.rel, folder, unix, unix, boolToInt(e.locked)); err != nil {
				return err
			}
		}
	}
	if len(stale) > 0 {
		del, err := tx.Prepare(`DELETE FROM notes WHERE path = ?`)
		if err != nil {
			return err
		}
		defer del.Close()
		for _, rel := range stale {
			if _, err := del.Exec(rel); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// indexedModTimes returns the modification time recorded for every indexed note.
func (s *Store) indexedModTimes() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT path, modified_at FROM notes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64, 128)
	for rows.Next() {
		var rel string
		var modified int64
		if err := rows.Scan(&rel, &modified); err != nil {
			return nil, err
		}
		out[rel] = modified
	}
	return out, rows.Err()
}

// IsIndexEmpty reports whether the index holds no notes, which is the one case
// where the app must reindex before it can show anything.
func (s *Store) IsIndexEmpty() bool {
	n, err := s.CountNotes()
	return err != nil || n == 0
}
