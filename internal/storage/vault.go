package storage

import (
	"io/fs"
	"os"
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
	return os.MkdirAll(filepath.Join(s.VaultPath, filepath.FromSlash(rel)), 0o755)
}

// DeleteFolder removes a folder and everything under it, pruning the index.
func (s *Store) DeleteFolder(rel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
	abs := filepath.Join(s.VaultPath, filepath.FromSlash(rel))
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM notes WHERE folder = ? OR folder LIKE ?`, rel, rel+"/%")
	return err
}

// RenameFolder renames/moves a folder and everything under it, updating the
// index with a single SQL statement (no file decompression) so it stays fast.
func (s *Store) RenameFolder(oldRel, newRel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	oldRel = strings.TrimPrefix(filepath.ToSlash(oldRel), "/")
	newRel = strings.TrimPrefix(filepath.ToSlash(newRel), "/")
	oldAbs := filepath.Join(s.VaultPath, filepath.FromSlash(oldRel))
	newAbs := filepath.Join(s.VaultPath, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return err
	}

	// Rewrite the oldRel prefix to newRel for every note under the folder.
	oldSlash := oldRel + "/%"
	skip := len(oldRel) + 1 // SQLite substr is 1-based; skip "oldRel"
	_, err := s.db.Exec(`
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

// Reindex rebuilds the index from the vault files: it upserts every note found
// on disk and prunes index rows whose files have disappeared. Safe to call on
// startup; for a personal vault it is effectively instant.
func (s *Store) Reindex() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	seen := make(map[string]bool)
	err := filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, noteExt) {
			return nil
		}
		rel, rerr := filepath.Rel(s.VaultPath, p)
		if rerr != nil {
			return rerr
		}
		rel = normalizeRel(rel)
		if _, cerr := s.ReadNote(rel); cerr != nil {
			return nil // skip unreadable/corrupt files rather than abort
		}
		modified := time.Now()
		if info, ierr := d.Info(); ierr == nil {
			modified = info.ModTime()
		}
		if err := s.indexNote(rel, modified); err != nil {
			return err
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}

	rows, err := s.db.Query(`SELECT path FROM notes`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if !seen[p] {
			stale = append(stale, p)
		}
	}
	rows.Close()
	for _, p := range stale {
		if _, err := s.db.Exec(`DELETE FROM notes WHERE path = ?`, p); err != nil {
			return err
		}
	}
	return nil
}
