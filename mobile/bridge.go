// Package bridge is the Go core as Android can call it.
//
// Everything below the interface is shared with the desktop application: the
// same vault, the same compressed Markdown files, the same SQLite index, the
// same encryption. None of it is reimplemented in Kotlin, because two
// implementations of a vault format is how two implementations disagree, and
// the one that disagrees about encryption loses notes.
//
// What gomobile will carry across the boundary is narrow — integers, strings,
// booleans, byte slices, and errors — so anything shaped like a list comes
// back as JSON and is decoded on the other side. That is the whole reason this
// package exists rather than binding the storage package directly.
//
// Every function here is called from the Android main thread or from a
// coroutine, so the store's own locking is what makes this safe; there is no
// extra state here beyond the one open vault.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sync"
	"time"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/storage"
	"atlas-notes/internal/update"
)

var (
	mu    sync.Mutex
	store *storage.Store
)

// ErrLockedMessage is what ReadNote returns when a note is encrypted and the
// vault has not been unlocked. Kotlin matches on it to know it should ask for
// the password rather than show a failure.
const ErrLockedMessage = "locked"

// Open prepares the vault in the directory Android gives the application. It
// is safe to call more than once; the second call is a no-op.
func Open(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if store != nil {
		return nil
	}
	// The storage layer reads these to find the vault. On a phone the system
	// decides where an application's files live and says so at runtime, which
	// is why it is passed in rather than guessed at.
	if err := setEnv(dataDir); err != nil {
		return err
	}
	s, err := storage.Open("", "")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	if s.IsIndexEmpty() {
		if err := s.Reindex(); err != nil {
			return fmt.Errorf("index the vault: %w", err)
		}
		if err := s.EnsureWelcome(); err != nil {
			return fmt.Errorf("write the first note: %w", err)
		}
	}
	store = s
	return nil
}

// Close releases the vault. Android may stop the process without calling it,
// which is safe: every write is already on disk before it returns.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if store != nil {
		store.Close()
		store = nil
	}
}

// noteRow is one row of the vault list, as the interface needs it. It is
// deliberately unexported: it crosses to Kotlin as JSON, so binding it as a
// class as well would put a second, unused shape in the generated interface.
type noteRow struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Folder   string `json:"folder"`
	Modified int64  `json:"modified"` // Unix seconds
	Locked   bool   `json:"locked"`
	HasTasks bool   `json:"hasTasks"`
}

// ListNotes returns every note in the vault as a JSON array, ordered by folder
// and then by name, which is the order the vault itself is in. Sorting it by
// date is the interface's business, not the vault's.
func ListNotes() (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	metas, err := s.ListNotes()
	if err != nil {
		return "", err
	}
	out := make([]noteRow, 0, len(metas))
	for _, m := range metas {
		out = append(out, noteRow{
			Path:     m.Path,
			Name:     path.Base(m.Path),
			Folder:   m.Folder,
			Modified: m.ModifiedAt.Unix(),
			Locked:   m.Locked,
			HasTasks: m.HasTasks,
		})
	}
	return toJSON(out)
}

// ReadNote returns a note's Markdown. A note that is encrypted while the vault
// is locked fails with ErrLockedMessage, which is a request for the password
// rather than a failure.
func ReadNote(rel string) (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	text, err := s.ReadNote(rel)
	if errors.Is(err, storage.ErrLocked) {
		return "", errors.New(ErrLockedMessage)
	}
	return text, err
}

// WriteNote saves a note, encrypting it if it is one of the protected ones.
func WriteNote(rel, content string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.WriteNote(rel, content)
}

// NewNote creates an empty note and returns its path.
func NewNote(title string) (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	if title == "" {
		title = "Untitled"
	}
	rel := s.UniqueName("", title)
	if err := s.WriteNote(rel, "# "+path.Base(rel)+"\n\n"); err != nil {
		return "", err
	}
	return rel, nil
}

// DeleteNote removes a note and its index row.
func DeleteNote(rel string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.DeleteNote(rel)
}

// RenameNote moves a note, which is how it is renamed: the name is the file.
func RenameNote(oldRel, newRel string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.RenameNote(oldRel, newRel)
}

// Password protection. The password is never stored, never logged, and never
// crosses this boundary in any direction but in.

// HasPassword reports whether this vault has ever had a password set.
func HasPassword() bool {
	s, err := vault()
	return err == nil && s.HasPassword()
}

// IsUnlocked reports whether protected notes can currently be read.
func IsUnlocked() bool {
	s, err := vault()
	return err == nil && s.IsUnlocked()
}

// Unlock opens the protected notes for this session.
func Unlock(password string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.Unlock(password)
}

// SetPassword sets the vault's password for the first time.
func SetPassword(password string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.SetPassword(password)
}

// Lock forgets the password until it is given again.
func Lock() {
	if s, err := vault(); err == nil {
		s.Lock()
	}
}

// LockNote encrypts a note; UnlockNote decrypts it. Both need the password to
// have been given already.
func LockNote(rel string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.LockNote(rel)
}

func UnlockNote(rel string) error {
	s, err := vault()
	if err != nil {
		return err
	}
	return s.UnlockNote(rel)
}

// ToggleTask flips the checklist item on one line of a note's text and returns
// the whole text back. The checklist model is shared with the desktop, so a
// task ticked on a phone reads the same on a laptop.
func ToggleTask(content string, line int) (string, error) {
	lines := splitLines(content)
	if line < 0 || line >= len(lines) {
		return content, fmt.Errorf("line %d is not in this note", line)
	}
	it, ok := checklist.ParseLine(lines[line])
	if !ok {
		return content, fmt.Errorf("line %d is not a task", line)
	}
	it.Checked = !it.Checked
	lines[line] = it.Marshal()
	return joinLines(lines), nil
}

// taskRow is one checklist item, with the line it lives on so ticking it can find
// its way back into the text.
type taskRow struct {
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

// Tasks returns the checklist items in a note's text, as JSON.
func Tasks(content string) (string, error) {
	var out []taskRow
	for i, line := range splitLines(content) {
		if it, ok := checklist.ParseLine(line); ok {
			out = append(out, taskRow{Line: i, Text: it.Text, Checked: it.Checked})
		}
	}
	return toJSON(out)
}

// releaseInfo is a newer version, if there is one.
type releaseInfo struct {
	Version string   `json:"version"`
	Notes   []string `json:"notes"`
}

// CheckUpdate asks whether a newer version has been published. It is the same
// check the desktop makes: one anonymous read of a text file, with nothing
// about the device or its notes sent. An empty string means there is nothing
// newer, which is also what being offline looks like.
func CheckUpdate(currentVersion string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := update.New().Check(ctx, "main", currentVersion)
	if err != nil || rel == nil {
		return "", err
	}
	return toJSON(releaseInfo{Version: rel.Version, Notes: rel.Notes})
}

// vault returns the open store, or says it is not open yet.
func vault() (*storage.Store, error) {
	mu.Lock()
	defer mu.Unlock()
	if store == nil {
		return nil, errors.New("the vault is not open")
	}
	return store, nil
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
