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
		if s.hiddenDir(p, d) {
			return filepath.SkipDir
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

// DeleteFolder removes a folder and everything under it, pruning the index,
// into the Trash when the store has one.
func (s *Store) DeleteFolder(rel string) error { return s.deleteFolder(rel, s.Trash) }

// DeleteFolderPermanently removes a folder for good, whatever the store's Trash.
func (s *Store) DeleteFolderPermanently(rel string) error { return s.deleteFolder(rel, nil) }

func (s *Store) deleteFolder(rel string, trash func(string) error) error {
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
	if err := discard(abs, trash, os.RemoveAll); err != nil {
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
		tasks    int // 1, 0, or tasksUnknown for a note that cannot be read
	}
	var changed []entry
	seen := make(map[string]bool, len(known))

	err = filepath.WalkDir(s.VaultPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if s.hiddenDir(p, d) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		// Every form of a note is indexed: plain or compressed in any format,
		// and locked. Which extension a file carries is what says whether it is
		// locked, so the scan reads it off the name rather than opening anything.
		locked := strings.HasSuffix(p, lockedExt)
		if _, plain := plainCompression(d.Name()); !locked && !plain {
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
		// The scan does not open files. A note that changed is marked for
		// ResolveContent to read later; a locked one cannot be read at all.
		tasks := tasksStale
		if locked {
			tasks = tasksUnknown
		}
		changed = append(changed, entry{rel, modified, locked, tasks})
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

	// A first scan inserts rather than upserts (see below), and a plain insert
	// fails on a note seen twice, which happens: a lock interrupted halfway
	// leaves both forms of a note on disk. The upsert kept the last one seen,
	// so that is what is kept here.
	if len(known) == 0 {
		last := make(map[string]int, len(changed))
		for i, e := range changed {
			last[e.rel] = i
		}
		if len(last) < len(changed) {
			kept := make([]entry, 0, len(last))
			for i, e := range changed {
				if last[e.rel] == i {
					kept = append(kept, e)
				}
			}
			changed = kept
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once committed

	if len(changed) > 0 {
		// An empty index cannot conflict with anything, so a first scan
		// inserts rather than upserts. The difference matters because of the
		// search index's triggers: they add about 1.4 µs to every upsert, even
		// one that never updates, which on a first launch with 10,000 notes
		// was 14 ms before the window could open.
		sqlText := upsertNoteSQL
		if len(known) == 0 {
			sqlText = insertNoteSQL
		}
		stmt, err := tx.Prepare(sqlText)
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
			if _, err := stmt.Exec(e.rel, folder, unix, unix,
				boolToInt(e.locked), e.tasks); err != nil {
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
