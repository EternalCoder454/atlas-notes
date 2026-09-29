package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/vaultlock"
)

// NoteMeta is a row of indexed note metadata. A note's title is its file name,
// which is the tail of Path, so it is not stored separately.
type NoteMeta struct {
	Path       string // vault-relative, no extension, e.g. "Work/Todo"
	Folder     string // parent folder, "" for root
	ModifiedAt time.Time
	CreatedAt  time.Time
	Locked     bool // stored encrypted; its content needs the vault password
	HasTasks   bool // contains checklist items, so it reads as a checklist
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
// leading slash, and, critically, no way out of the vault.
// Segments that would escape it ("..") or mean nothing (".", "") are dropped
// rather than resolved, so a note titled "../../secrets" becomes "secrets"
// inside the vault instead of a file two directories above it. Names reach this
// function straight from the title field, the tree's rename prompt and a
// synced vault's own filenames, so this is the one place that has to hold.
//
// It strips no extension. It is applied to a name again at every step, so one
// that stripped ".md" would turn a note called "foo.md", which is the file
// "foo.md.md", into "foo", and then into another note's name. A file name
// becomes a note name in relFromFileName, once.
func normalizeRel(rel string) string {
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

// relFromFileName is how a file name in the vault becomes a note name. It strips
// exactly one extension, a plain one in any format or the locked one, and says
// which it was; ok is false for a file that is not a note, and for one that is
// nothing but the extension. The name may be a path: only its end is looked at.
func relFromFileName(name string) (rel string, locked bool, c Compression, ok bool) {
	if strings.HasSuffix(name, lockedExt) && len(name) > len(lockedExt) {
		return strings.TrimSuffix(name, lockedExt), true, "", true
	}
	if c, ok := plainCompression(name); ok {
		return strings.TrimSuffix(name, c.ext()), false, c, true
	}
	return "", false, "", false
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

// notePath maps a vault-relative identifier to the absolute path a note is
// written to: its name with the vault's extension. It does not validate
// containment; callers that touch the filesystem go through notePathSafe.
func (s *Store) notePath(rel string) string {
	return filepath.Join(s.VaultPath, filepath.FromSlash(normalizeRel(rel))+s.Compression().ext())
}

// notePathSafe is notePath with the vault-containment check applied.
func (s *Store) notePathSafe(rel string) (string, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	return abs + s.Compression().ext(), nil
}

// WriteNote encodes and atomically writes content, then refreshes the index.
// Safe to call from any goroutine (see Store.writeMu).
//
// The note is written in the vault's format, and any copy of it in another
// format is removed once that has succeeded, so saving a note converts it.
//
// A note that is already locked stays locked, and a new note created inside a
// locked folder is written locked from the start — it must never touch the
// disk in the clear first and be encrypted afterwards, because the plaintext
// would have been there in between.
//
// When the store keeps history, what the note said until now is copied there
// first, if the newest copy is not a recent one; see history.go.
func (s *Store) WriteNote(rel, content string) error {
	return s.writeNote(rel, content, false)
}

// writeNote is WriteNote. forceHistory keeps the current text as a version
// whatever its age, and refuses to go on if that fails: a restore has to be
// undoable, where an ordinary save must not be held up by a full history disk.
func (s *Store) writeNote(rel, content string, forceHistory bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeNoteLocked(rel, content, forceHistory)
}

// createNote writes a note only if no note of that name exists yet, and says
// whether it did. The check and the write are under one hold of writeMu, so a
// note that appears in between, from another save or another goroutine, is never
// written over.
func (s *Store) createNote(rel, content string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.noteTaken(normalizeRel(rel)) {
		return false, nil
	}
	if err := s.writeNoteLocked(rel, content, false); err != nil {
		return false, err
	}
	return true, nil
}

// writeNoteLocked is writeNote for a caller that holds writeMu.
func (s *Store) writeNoteLocked(rel, content string, forceHistory bool) error {
	// Before a file name is chosen: another device may have changed the format.
	s.refreshCompression()
	rel = normalizeRel(rel)
	abs, err := s.notePathSafe(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	locked := s.IsNoteLocked(rel) || s.lockedByFolder(rel)
	// The form the note is read from now, before this save adds another: it is
	// what the new text is an edit of, and is not a conflict with it.
	base := s.newestPlain(rel)
	// A note that is locked has no plain versions in its history. One that a
	// sync brought in from a device that locked it might, and this save is about
	// to add to the history, so it is put right first and a failure stops the
	// save: the alternative is a note that is locked with its old text in the
	// clear beside it.
	if locked && s.HistoryDir != "" {
		if key, kerr := s.key(); kerr == nil {
			if err := s.sealHistory(rel, key); err != nil {
				return fmt.Errorf("its earlier versions could not be sealed: %w", err)
			}
		}
	}
	if err := s.snapshotHistory(rel, forceHistory); err != nil {
		if forceHistory {
			return fmt.Errorf("could not keep the current text as a version: %w", err)
		}
		log.Printf("atlas-notes: keeping a version of %q: %v", rel, err)
	}

	if !locked {
		data, err := s.encode(s.Compression(), []byte(content))
		if err != nil {
			return err
		}
		if err := atomicWrite(abs, data); err != nil {
			return err
		}
		// The note is saved and indexed before the other forms go: a failure to
		// remove one does not undo the save, and is logged rather than reported
		// as though the save had failed. The next save tries again.
		ixErr := s.indexNote(rel, time.Now(), false, HasTasks(content))
		if ixErr == nil {
			// The text is in hand, so the search index is brought up to date now
			// rather than queued. If this fails the note stays queued, and the
			// next content pass picks it up.
			ixErr = s.indexWritten(rel, content)
		}
		if err := s.removePlain(rel, abs, base, []byte(content), nil); err != nil {
			log.Printf("atlas-notes: saved %q, but the old copy is still there: %v", rel, err)
		}
		return ixErr
	}

	key, err := s.key()
	if err != nil {
		return err
	}
	// A locked note's payload is zstd whatever the vault's format is, so that
	// older versions and the phone can still open it.
	sealed, err := vaultlock.Seal(key, s.enc.EncodeAll([]byte(content), nil))
	if err != nil {
		return err
	}
	lockedAbs, err := s.lockedPathSafe(rel)
	if err != nil {
		return err
	}
	if err := atomicWrite(lockedAbs, sealed); err != nil {
		return err
	}
	// A note that was unlocked and has landed in a locked folder leaves its
	// plaintext behind otherwise, in whichever format it was. A copy that says
	// something else is kept, sealed, as a conflict copy.
	if err := s.removePlain(rel, "", base, []byte(content), key); err != nil {
		return fmt.Errorf("saved, but the unencrypted copy is still there: %w", err)
	}
	return s.indexNote(rel, time.Now(), true, HasTasks(content))
}

// ReadNote reads a note's markdown, decrypting it first when it is locked.
// A locked note read without the password fails with ErrLocked, which is the
// interface's cue to ask for it rather than an error to report.
func (s *Store) ReadNote(rel string) (string, error) {
	out, err := s.readPlain(rel)
	if os.IsNotExist(err) {
		return s.readLockedNote(rel)
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// readLockedNote is ReadNote for the encrypted form.
func (s *Store) readLockedNote(rel string) (string, error) {
	abs, err := s.lockedPathSafe(rel)
	if err != nil {
		return "", err
	}
	sealed, err := os.ReadFile(abs)
	if err != nil {
		return "", err // including "not found": the note simply is not there
	}
	key, kerr := s.key()
	if kerr != nil {
		return "", kerr
	}
	payload, err := vaultlock.Open(key, sealed)
	if err != nil {
		return "", err
	}
	out, err := s.decodePayload(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ErrTrashFailed is returned when a delete could not move something to the
// Trash. Nothing has been deleted: the caller can offer to delete it
// permanently instead, which is a different thing to agree to.
var ErrTrashFailed = errors.New("it could not be moved to the Trash")

// DeleteNote removes a note and its index row, into the Trash when the store
// has one.
func (s *Store) DeleteNote(rel string) error { return s.deleteNote(rel, s.Trash) }

// DeleteNotePermanently removes a note for good, whatever the store's Trash.
func (s *Store) DeleteNotePermanently(rel string) error { return s.deleteNote(rel, nil) }

func (s *Store) deleteNote(rel string, trash func(string) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	files, err := s.plainFiles(rel)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := discard(f.path, trash, os.Remove); err != nil {
			return err
		}
	}
	// A locked note lives under the other extension; delete has to find it
	// either way, or "deleting" one would leave the encrypted file behind.
	if lockedAbs, lerr := s.lockedPathSafe(rel); lerr == nil {
		if err := discard(lockedAbs, trash, os.Remove); err != nil {
			return err
		}
	}
	// A note deleted for good takes its history with it. One in the Trash keeps
	// it, so that restoring the note from there restores its versions too.
	if trash == nil {
		s.removeHistory(rel)
	}
	_, err = s.db.Exec(`DELETE FROM notes WHERE path = ?`, rel)
	return err
}

// discard takes one file or folder out of the vault the way a delete asked
// for: into the Trash when there is one, and through remove when there is
// not. A path that is already gone is already out of the vault. remove is
// os.Remove for a note and os.RemoveAll only for a folder, so a note's path
// that turned out to be a directory is refused rather than emptied.
func discard(path string, trash, remove func(string) error) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	if trash != nil {
		if err := trash(path); err != nil {
			return fmt.Errorf("%w: %v", ErrTrashFailed, err)
		}
		return nil
	}
	return remove(path)
}

// ErrNameTaken is what a rename is refused with when another note already has
// the name it was asked to take. Renaming onto a note would replace it.
var ErrNameTaken = errors.New("a note with that name already exists")

// renameFile is os.Rename, a variable so that a test can make one of a note's
// renames fail and see the others put back.
var renameFile = os.Rename

// RenameNote renames/moves a note; newRel may include a different folder. A note
// is never renamed onto another: that fails with ErrNameTaken, unless the two
// names differ only in letter case, which is one note with a new spelling.
//
// Every form of the note moves, the locked one too if both a locked and an
// unlocked one exist. If one of the renames fails, the ones already done are
// undone, so the note is not left split between two names.
func (s *Store) RenameNote(oldRel, newRel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	oldRel = normalizeRel(oldRel)
	newRel = normalizeRel(newRel)
	if !strings.EqualFold(oldRel, newRel) && s.noteTaken(newRel) {
		return ErrNameTaken
	}
	oldFiles, err := s.plainFiles(oldRel)
	if err != nil {
		return err
	}
	newBase, err := s.resolve(newRel)
	if err != nil {
		return err
	}
	oldLocked, err := s.lockedPathSafe(oldRel)
	if err != nil {
		return err
	}
	newLocked, err := s.lockedPathSafe(newRel)
	if err != nil {
		return err
	}
	// Each form of the note that exists keeps its own extension: renaming is
	// not converting. Normally that is one file.
	type step struct{ from, to string }
	var steps []step
	for _, f := range oldFiles {
		if _, err := os.Lstat(f.path); err == nil {
			steps = append(steps, step{f.path, newBase + f.c.ext()})
		}
	}
	if _, err := os.Lstat(oldLocked); err == nil {
		steps = append(steps, step{oldLocked, newLocked})
	}
	if len(steps) == 0 {
		return &fs.PathError{Op: "rename", Path: oldFiles[0].path, Err: fs.ErrNotExist}
	}
	// A file that is already at the new name is only all right if it is the note
	// itself, which is what a change of case looks like on a file system that
	// does not tell the cases apart.
	for _, st := range steps {
		if dst, err := os.Lstat(st.to); err == nil {
			if src, serr := os.Lstat(st.from); serr != nil || !os.SameFile(src, dst) {
				return ErrNameTaken
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(newBase), 0o755); err != nil {
		return err
	}
	var done []step
	for _, st := range steps {
		if err := renameFile(st.from, st.to); err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				if rerr := os.Rename(done[i].to, done[i].from); rerr != nil {
					log.Printf("atlas-notes: putting %s back after a failed rename: %v", done[i].from, rerr)
				}
			}
			return err
		}
		done = append(done, st)
	}
	s.moveHistory(oldRel, newRel)
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
	INSERT INTO notes(path, folder, modified_at, created_at, locked, has_tasks)
	VALUES(?,?,?,?,?,?)
	ON CONFLICT(path) DO UPDATE SET
		folder      = excluded.folder,
		modified_at = excluded.modified_at,
		locked      = excluded.locked,
		-- tasksUnknown means "could not tell", and keeps whatever was known.
		-- Anything else, tasksStale included, is written through.
		has_tasks   = CASE WHEN excluded.has_tasks = -1
		                   THEN notes.has_tasks ELSE excluded.has_tasks END`

// insertNoteSQL is upsertNoteSQL for a scan that knows every row is new.
const insertNoteSQL = `
	INSERT INTO notes(path, folder, modified_at, created_at, locked, has_tasks)
	VALUES(?,?,?,?,?,?)`

// Two negative markers stand in for a checklist flag that is not a yes or a
// no. Both read as "not a checklist" until they are resolved, because the one
// thing the interface must not do is guess.
const (
	// tasksUnknown is a note the scan could not read: a locked one. There is no
	// resolving it without the password, so the index keeps what it knew.
	tasksUnknown = -1
	// tasksStale is a note that changed on disk and has not been read since.
	// The scan never opens files, so it marks them and ResolveTaskFlags does
	// the reading once the window is up.
	tasksStale = -2
)

// indexNote upserts a note's metadata row.
func (s *Store) indexNote(rel string, modified time.Time, locked, hasTasks bool) error {
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	unix := modified.Unix()
	_, err := s.db.Exec(upsertNoteSQL, rel, folder, unix, unix,
		boolToInt(locked), boolToInt(hasTasks))
	return err
}

// HasTasks reports whether a note's text contains checklist items. It is the
// one place that decides, so the write path and the vault scan cannot disagree.
func HasTasks(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if _, ok := checklist.TaskLine(line); ok {
			return true
		}
	}
	return false
}

// setIndexLocked records that a note changed between locked and unlocked,
// without touching its timestamps: locking a note does not edit it.
func (s *Store) setIndexLocked(rel string, locked bool) error {
	_, err := s.db.Exec(`UPDATE notes SET locked = ? WHERE path = ?`, boolToInt(locked), rel)
	return err
}

// LockedNotes lists every note stored encrypted, by vault-relative path.
func (s *Store) LockedNotes() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM notes WHERE locked = 1 ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListNotes returns all indexed notes ordered by folder then title.
func (s *Store) ListNotes() ([]NoteMeta, error) {
	rows, err := s.db.Query(`SELECT path, folder, modified_at, created_at, locked, has_tasks FROM notes ORDER BY folder, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, 256)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		var locked, hasTasks int
		if err := rows.Scan(&m.Path, &m.Folder, &modified, &created, &locked, &hasTasks); err != nil {
			return nil, err
		}
		m.ModifiedAt = time.Unix(modified, 0)
		m.CreatedAt = time.Unix(created, 0)
		m.Locked = locked != 0
		// Greater than zero, not merely non-zero: a locked note the scan could
		// not read is stored as tasksUnknown, and "we could not tell" has to
		// read as "not known to be a checklist" rather than as a checklist.
		m.HasTasks = hasTasks > 0
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
		if !s.noteTaken(rel) {
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
		SELECT path, folder, modified_at, created_at, locked, has_tasks
		FROM notes ORDER BY modified_at DESC, path LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NoteMeta, 0, limit)
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		var locked, hasTasks int
		if err := rows.Scan(&m.Path, &m.Folder, &modified, &created, &locked, &hasTasks); err != nil {
			return nil, err
		}
		m.ModifiedAt = time.Unix(modified, 0)
		m.CreatedAt = time.Unix(created, 0)
		m.Locked = locked != 0
		// Greater than zero, not merely non-zero: a locked note the scan could
		// not read is stored as tasksUnknown, and "we could not tell" has to
		// read as "not known to be a checklist" rather than as a checklist.
		m.HasTasks = hasTasks > 0
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
