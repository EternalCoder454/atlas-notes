package storage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// What "atlas-notes mcp", the server Claude Code talks to, shares with the
// rest of the program: the person's settings for it, the signal that tells a
// running window it has changed the vault, and the writes it makes.

// ClaudeAccess is what Claude Code may do with the vault. Nothing at all until
// Enabled is set; after that, each kind of change separately. Password
// protected notes are out of reach whatever these say: the server never has
// the password, and it does not show that they exist.
type ClaudeAccess struct {
	Enabled bool `json:"enabled"`
	Read    bool `json:"read"`   // list, read and search notes
	Write   bool `json:"write"`  // create, edit, rename and move notes; create folders
	Delete  bool `json:"delete"` // move notes and folders to the Trash
}

// DefaultHistoryDir is where a vault's earlier note versions are kept: in the
// data directory, under a name taken from the vault's path, so two vaults
// opened on the same machine never share one.
func DefaultHistoryDir(vault string) string {
	sum := sha256.Sum256([]byte(vault))
	return filepath.Join(dataDir(), "history", hex.EncodeToString(sum[:])[:12])
}

// ClaudeIndexPath is the index the MCP server keeps. It is not the window's:
// the two are separate processes, and the index is a cache, so each keeps its
// own rather than one waiting on the other's writes.
func ClaudeIndexPath() string { return filepath.Join(dataDir(), "claude-index.db") }

// WriteNoteVersioned is WriteNote that always keeps the text being replaced in
// Version History first, and refuses to write if that fails. It is how a change
// the person did not type is made, so that every one can be undone.
func (s *Store) WriteNoteVersioned(rel, content string) error {
	return s.writeNote(rel, content, true)
}

// KeepCurrentVersion copies what a note says on disk now into its history,
// however recent the newest version is. The window uses it before saving over
// a change made outside it, so the change is not lost.
func (s *Store) KeepCurrentVersion(rel string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.snapshotHistory(rel, true)
}

// ExternalChange is one change made to the vault from outside the window.
type ExternalChange struct {
	Op   string `json:"op"`   // "write", "delete", "rename", "folder"
	Path string `json:"path"` // the note or folder, vault-relative
	To   string `json:"to,omitempty"`
	At   int64  `json:"at"` // Unix milliseconds
}

// Ops an ExternalChange can carry.
const (
	ChangeWrite  = "write"  // a note was created or its text changed
	ChangeDelete = "delete" // a note or folder went to the Trash
	ChangeRename = "rename" // a note or folder moved from Path to To
	ChangeFolder = "folder" // a folder was created
)

// externalChangesMax is the size past which the log starts again. A window
// that was reading it notices it shrink and reads from the top.
const externalChangesMax = 256 << 10

// ExternalChangesPath is the log of changes made from outside the window. A
// running window watches it, and rescans the vault when it grows.
func ExternalChangesPath() string { return filepath.Join(dataDir(), "external-changes.jsonl") }

// RecordExternalChange appends a change to the log, one JSON object a line.
// Each line goes out in one write to a file opened for appending, so two
// servers recording at once do not interleave.
func RecordExternalChange(c ExternalChange) error {
	if c.At == 0 {
		c.At = time.Now().UnixMilli()
	}
	line, err := json.Marshal(c)
	if err != nil {
		return err
	}
	p := ExternalChangesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if info, err := os.Stat(p); err == nil && info.Size() > externalChangesMax {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(p, flags, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// ReadExternalChanges returns the changes logged from offset on, and the
// offset to read from next time. A log shorter than offset has started again,
// and is read from the top. A line still being written is left for next time.
func ReadExternalChanges(offset int64) ([]ExternalChange, int64, error) {
	f, err := os.Open(ExternalChangesPath())
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	var out []ExternalChange
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF, or a line without its newline yet
		}
		offset += int64(len(line))
		var c ExternalChange
		if json.Unmarshal(line, &c) == nil {
			out = append(out, c)
		}
	}
	return out, offset, nil
}

// ExternalChangesSize is the log's current length: where a window that has
// just opened starts reading, since the vault scan covers everything before.
func ExternalChangesSize() int64 {
	info, err := os.Stat(ExternalChangesPath())
	if err != nil {
		return 0
	}
	return info.Size()
}

// FolderIsProtected reports whether a folder has anything password protected
// to do with it: it is locked itself, it is inside a locked folder, or a locked
// note or folder is somewhere beneath it. It answers from the disk, and says
// yes whenever it cannot tell, because the cost of a wrong no is a protected
// note renamed or deleted by something that was never meant to see it.
func (s *Store) FolderIsProtected(rel string) bool {
	rel = normalizeRel(rel)
	if rel == "" {
		return true // the vault root is never handed over whole
	}
	if s.lockedByFolder(rel + "/-") {
		return true
	}
	abs, err := s.resolveFolder(rel)
	if err != nil {
		return true
	}
	found := false
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Anything sealed counts, not only notes: a locked note's pictures are
		// ".enc" files too, and so is its place in a sync conflict.
		name := d.Name()
		if name == folderMarkerName || name == lockFileName ||
			(!d.IsDir() && strings.HasSuffix(name, sealedExt)) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found || (err != nil && !os.IsNotExist(err))
}

// ProtectedPaths walks the vault for what is password protected, as the disk
// has it now rather than as the index last saw it: the folders marked locked
// (everything beneath one is protected too) and the notes stored encrypted.
func (s *Store) ProtectedPaths() (folders []string, notes map[string]bool, err error) {
	notes = map[string]bool{}
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
		rel, rerr := filepath.Rel(s.VaultPath, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		switch name := d.Name(); {
		case name == folderMarkerName:
			if dir := path.Dir(rel); dir != "." {
				folders = append(folders, dir)
			}
		case strings.HasSuffix(name, lockedExt) && len(name) > len(lockedExt):
			notes[strings.TrimSuffix(rel, lockedExt)] = true
		}
		return nil
	})
	return folders, notes, err
}

// ReservedPath reports whether a vault-relative path is in a folder that is not
// the person's notes: the pictures folder, or one another program keeps in the
// vault. The vault scan stays out of them, and so does anything that writes,
// moves or deletes on someone else's behalf.
func ReservedPath(rel string) bool {
	segs := strings.Split(normalizeRel(rel), "/")
	if segs[0] == attachmentsDir {
		return true
	}
	for _, seg := range segs {
		if toolDirs[seg] || strings.HasPrefix(seg, ".Trash-") {
			return true
		}
	}
	return false
}

// InLockedFolder reports whether a folder is locked or is inside one.
func (s *Store) InLockedFolder(rel string) bool {
	rel = normalizeRel(rel)
	return rel != "" && s.lockedByFolder(rel+"/-")
}

// TaggedNote is one note and one tag it carries, exactly as written.
type TaggedNote struct {
	Tag  string
	Note NoteMeta
}

// AllTagged lists every (tag, note) pair for unlocked notes in one query.
func (s *Store) AllTagged() ([]TaggedNote, error) {
	rows, err := s.db.Query(`
		SELECT t.tag, n.path, n.folder, n.modified_at, n.created_at, n.locked, n.has_tasks
		FROM note_tags t JOIN notes n ON n.id = t.note_id
		WHERE n.locked = 0 ORDER BY t.tag, n.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaggedNote
	for rows.Next() {
		var t TaggedNote
		var modified, created int64
		var locked, hasTasks int
		if err := rows.Scan(&t.Tag, &t.Note.Path, &t.Note.Folder, &modified, &created, &locked, &hasTasks); err != nil {
			return nil, err
		}
		t.Note.ModifiedAt = time.Unix(modified, 0)
		t.Note.CreatedAt = time.Unix(created, 0)
		t.Note.Locked = locked != 0
		t.Note.HasTasks = hasTasks > 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// Checkpoint folds the index's write-ahead log into the database and empties
// it, so the file does not stay large after a run of writes.
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
