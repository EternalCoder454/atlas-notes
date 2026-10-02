package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"atlas-notes/internal/storage"
)

// refreshGap is how long the server trusts its index before looking at the
// vault again. The window, a sync client or the person can change notes while
// it runs; a scan reads directory entries only, so this is cheap, but a model
// making ten calls in a row does not need ten.
const refreshGap = 2 * time.Second

// settingsPoll is how often the server looks for a change to what it is
// allowed to do, so that the tools it offers follow the switches in Settings.
const settingsPoll = 2 * time.Second

type server struct {
	opts Options
	w    *writer

	// store is opened on the first call that needs it, so a server that is
	// switched off never touches the vault at all.
	store       *storage.Store
	lastRefresh time.Time

	// cfg is the last settings read, valid while the file's time and size hold.
	cfgMu   sync.Mutex
	cfgInfo [2]int64
	cfgOK   bool
	cfgAcc  storage.ClaudeAccess
	cfgVlt  string

	// offered is the access the tool list was last built from, so the watcher
	// can tell the client when it would be different.
	mu       sync.Mutex
	offered  storage.ClaudeAccess
	watching bool
	stop     chan struct{}
}

func newServer(opts Options) *server {
	return &server{opts: opts, w: &writer{out: opts.Out}, stop: make(chan struct{})}
}

func (s *server) close() {
	s.mu.Lock()
	if s.watching {
		close(s.stop)
		s.watching = false
	}
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.Checkpoint(); err != nil {
			log.Printf("emptying the index log: %v", err)
		}
		s.store.Close()
	}
}

// invalidate makes the next call look at the vault afresh.
func (s *server) invalidate() {
	s.lastRefresh = time.Time{}
}

// settings reads what the person has allowed, and which vault. A config that
// is missing or cannot be read allows nothing: it is never written from here,
// and never guessed at. The parse is kept while the file's modification time
// and size stay the same.
func (s *server) settings() (storage.ClaudeAccess, string) {
	info, err := os.Stat(storage.ConfigPath())
	if err != nil {
		return storage.ClaudeAccess{}, ""
	}
	key := [2]int64{info.ModTime().UnixNano(), info.Size()}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	// A file changed within the last moment may change again without its time
	// moving, so only an older one is trusted.
	if s.cfgOK && s.cfgInfo == key && time.Since(info.ModTime()) > 2*time.Second {
		return s.cfgAcc, s.cfgVlt
	}
	cfg, err := storage.LoadConfig()
	if err != nil {
		log.Printf("reading settings: %v", err)
		s.cfgOK = false
		return storage.ClaudeAccess{}, ""
	}
	s.cfgInfo, s.cfgOK, s.cfgAcc, s.cfgVlt = key, true, cfg.Claude, cfg.VaultPath
	return cfg.Claude, cfg.VaultPath
}

// effective is the access that counts: none of it while the master switch is
// off.
func effective(a storage.ClaudeAccess) storage.ClaudeAccess {
	if !a.Enabled {
		return storage.ClaudeAccess{}
	}
	return a
}

// startWatching begins following the settings once the client is ready for
// notifications.
func (s *server) startWatching() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watching {
		return
	}
	s.watching = true
	go s.watchSettings(s.stop)
}

func (s *server) watchSettings(stop chan struct{}) {
	t := time.NewTicker(settingsPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		a, _ := s.settings()
		a = effective(a)
		s.mu.Lock()
		changed := a != s.offered
		s.mu.Unlock()
		if changed {
			// The client asks for the list again, and listTools records
			// what it was given.
			s.w.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
			s.mu.Lock()
			s.offered = a
			s.mu.Unlock()
		}
	}
}

// listTools is the tools the person currently allows, and atlas_status, which
// is always there so that a model with nothing else can say why.
func (s *server) listTools() []map[string]any {
	a, _ := s.settings()
	a = effective(a)
	s.mu.Lock()
	s.offered = a
	s.mu.Unlock()
	var out []map[string]any
	for _, t := range tools {
		if allowed(a, t.needs) {
			out = append(out, t.describe())
		}
	}
	return out
}

// permission is what a tool needs the person to have allowed.
type permission int

const (
	needsNothing permission = iota
	needsRead
	needsWrite
	needsDelete
)

func allowed(a storage.ClaudeAccess, p permission) bool {
	switch p {
	case needsNothing:
		return true
	case needsRead:
		return a.Enabled && a.Read
	case needsWrite:
		return a.Enabled && a.Write
	case needsDelete:
		return a.Enabled && a.Delete
	}
	return false
}

// refusal says which switch would allow what was refused.
func refusal(a storage.ClaudeAccess, p permission) error {
	if !a.Enabled {
		return errors.New(`Atlas Notes has not given Claude Code access to its notes. ` +
			`The person can turn it on in Atlas Notes, under Settings, Claude Code, "Let Claude Code use your notes".`)
	}
	setting := map[permission]string{
		needsRead:   "Read and search notes",
		needsWrite:  "Create and edit notes",
		needsDelete: "Delete notes",
	}[p]
	return fmt.Errorf(`Atlas Notes does not allow this. The person can turn on %q in Atlas Notes, under Settings, Claude Code.`, setting)
}

// call runs one tool, after checking the person allows it now: the list the
// client holds may be older than the switch.
func (s *server) call(t *tool, args []byte) (res toolResult) {
	a, vault := s.settings()
	if !allowed(a, t.needs) {
		return errorResult(refusal(a, t.needs))
	}
	if t.needs != needsNothing || a.Enabled {
		if err := s.open(vault); err != nil {
			return errorResult(fmt.Errorf("the vault could not be opened: %w", err))
		}
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("%s: %v", t.name, p)
			res = errorResult(fmt.Errorf("%s failed unexpectedly", t.name))
		}
	}()
	if len(args) == 0 || string(args) == "null" {
		args = []byte("{}")
	}
	text, err := t.run(s, a, args)
	if err != nil {
		return errorResult(err)
	}
	return textResult(text)
}

// open makes sure the store is the vault the settings name, opening it, or
// opening it again if the person has moved their vault since.
func (s *server) open(vault string) error {
	if vault == "" {
		vault = storage.DefaultVaultPath()
	}
	if s.store != nil && s.store.VaultPath == vault {
		return nil
	}
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
	if _, err := os.Stat(vault); err != nil {
		return err // never create a vault the person has not got
	}
	st, err := storage.Open(vault, storage.ClaudeIndexPath())
	if err != nil {
		return err
	}
	st.Trash = s.opts.Trash
	st.HistoryDir = storage.DefaultHistoryDir(st.VaultPath)
	s.store = st
	s.lastRefresh = time.Time{}
	return nil
}

// refresh brings the server's index up to date with the vault, at most every
// refreshGap. The window and other devices write to the vault too.
func (s *server) refresh() {
	if time.Since(s.lastRefresh) < refreshGap {
		return
	}
	if err := s.store.Reindex(); err != nil {
		log.Printf("scanning the vault: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.store.ResolveContent(ctx); err != nil {
		log.Printf("reading notes: %v", err)
	}
	s.lastRefresh = time.Now()
}

// changed tells a running window what happened, so it can show it.
func changed(c storage.ExternalChange) {
	if err := storage.RecordExternalChange(c); err != nil {
		log.Printf("telling the window about %s %q: %v", c.Op, c.Path, err)
	}
}

// --- what is and is not visible ---------------------------------------------

// noteName is the note a path given by the model means. Paths arrive in every
// spelling: with a leading slash, with ".md" because the model thinks of
// files. The ".md" goes unless a note really has it in its name.
func (s *server) noteName(p string) (string, error) {
	rel := storage.CleanNoteName(p)
	if rel == "" {
		return "", errors.New("a note path is needed, such as \"Work/Spec\"")
	}
	if lower := strings.ToLower(rel); strings.HasSuffix(lower, ".md") && !s.store.NoteExists(rel) {
		if trimmed := storage.CleanNoteName(rel[:len(rel)-3]); trimmed != "" {
			rel = trimmed
		}
	}
	return rel, nil
}

func folderName(p string) string { return storage.CleanNoteName(p) }

// hidden reports whether a note is password protected, itself or by its
// folder. A hidden note is answered for as though it did not exist.
// So is anything in a folder that is not the person's notes, such as the one
// holding the pictures, where a protected note's images are sealed.
func (s *server) hidden(rel string) bool {
	return storage.ReservedPath(rel) || s.store.NoteIsProtected(rel)
}

// errNoNote is what a missing note and a protected one both get.
func errNoNote(rel string) error {
	return fmt.Errorf("there is no note called %q. list_notes or search_notes shows what there is", rel)
}

func errNoFolder(rel string) error {
	return fmt.Errorf("there is no folder called %q. list_folders shows what there is", rel)
}

// existingNote resolves a path to a note that is there and visible.
func (s *server) existingNote(p string) (string, error) {
	rel, err := s.noteName(p)
	if err != nil {
		return "", err
	}
	if s.hidden(rel) || !s.store.NoteExists(rel) {
		return "", s.errMissing(rel)
	}
	return rel, nil
}

// visibleFolder resolves a path to a folder that is there and not locked.
func (s *server) visibleFolder(p string) (string, error) {
	rel := folderName(p)
	if rel == "" {
		return "", nil
	}
	if storage.ReservedPath(rel) || s.store.InLockedFolder(rel) || !s.store.FolderExists(rel) {
		return "", errNoFolder(rel)
	}
	return rel, nil
}

// visibility answers "may this note be shown" for a whole listing at once,
// from one walk of the disk rather than a look at each note's folders. It does
// not trust the index's locked flag alone: a note locked in the window since
// the last scan is still a row the index calls unlocked.
type visibility struct {
	locked      []string
	lockedNotes map[string]bool
}

func (s *server) visibility() (*visibility, error) {
	// Not kept between calls: a note locked in the window a moment ago must
	// not be listed, and only the walk can tell.
	folders, notes, err := s.store.ProtectedPaths()
	if err != nil {
		return nil, err
	}
	return &visibility{locked: folders, lockedNotes: notes}, nil
}

func (v *visibility) note(m storage.NoteMeta) bool {
	return !m.Locked && !v.lockedNotes[m.Path] && !v.under(m.Path) && !storage.ReservedPath(m.Path)
}

func (v *visibility) folder(rel string) bool {
	return !v.under(rel+"/-") && !storage.ReservedPath(rel)
}

// under reports whether a path is inside a locked folder.
func (v *visibility) under(rel string) bool {
	for _, f := range v.locked {
		if strings.HasPrefix(rel, f+"/") {
			return true
		}
	}
	return false
}
