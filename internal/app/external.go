package app

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"atlas-notes/internal/diag"
	"atlas-notes/internal/storage"
)

// Changes made to the vault from outside the window, by Claude Code through
// "atlas-notes mcp". The server writes the notes and appends a line to a log
// for each change; the window watches that log, and when it grows rescans the
// vault, follows the open notes through renames and deletions, and reloads one
// whose text was edited. See storage.ExternalChange.

// externalDelayMs is how long a burst of log writes is let to finish before it
// is read. A tool call that edits several notes writes its lines within a few
// milliseconds of each other, so a short wait is enough to take them together.
const externalDelayMs = 120

// externalRateMs is the most often GIO reports the log changing. Its default
// holds a second write back for most of a second, which made an edit that
// followed another appear late.
const externalRateMs = 100

// externalWatch is the window's watch on the log.
type externalWatch struct {
	monitor gio.FileMonitorrer
	cancel  context.CancelFunc
	offset  int64 // where the log has been read to
	waiting bool  // a read is already scheduled
}

// watchExternalChanges starts watching the log. Entries already in it are
// ignored: the vault scan at startup covers whatever they describe. The file
// may not exist yet; GIO reports it when it appears.
func (a *App) watchExternalChanges() {
	if a.store == nil || a.ext != nil {
		return
	}
	w := &externalWatch{offset: storage.ExternalChangesSize()}
	ctx, cancel := context.WithCancel(context.Background())
	m, err := gio.NewFileForPath(storage.ExternalChangesPath()).MonitorFile(ctx, gio.FileMonitorNone)
	if err != nil {
		cancel()
		log.Printf("atlas-notes: watch external changes: %v", err)
		return
	}
	w.monitor, w.cancel = m, cancel
	gio.BaseFileMonitor(m).SetRateLimit(externalRateMs)
	gio.BaseFileMonitor(m).ConnectChanged(func(_, _ gio.Filer, _ gio.FileMonitorEvent) {
		if a.closing || w.waiting {
			return
		}
		w.waiting = true
		coreglib.TimeoutAdd(externalDelayMs, func() bool {
			w.waiting = false
			if !a.closing {
				a.applyExternalChanges()
			}
			return false
		})
	})
	a.ext = w
}

// stopExternalWatch lets go of the monitor, for shutdown.
func (a *App) stopExternalWatch() {
	if a.ext == nil {
		return
	}
	a.ext.cancel()
	a.ext = nil
}

// applyExternalChanges reads what the log gained and acts on it.
func (a *App) applyExternalChanges() {
	if a.ext == nil || a.store == nil {
		return
	}
	changes, next, err := storage.ReadExternalChanges(a.ext.offset)
	if err != nil {
		log.Printf("atlas-notes: read external changes: %v", err)
		return
	}
	moved := next != a.ext.offset
	a.ext.offset = next
	if !moved && len(changes) == 0 {
		return
	}
	if diag.Enabled() {
		for _, c := range changes {
			diag.Event("external.change", "op", c.Op, "path", c.Path, "to", c.To)
		}
	}
	// A log that started again can leave the offset mid-line, and lose the
	// lines it skipped; the rescan still finds whatever they described.
	a.requestRescan()

	written := map[string]bool{}
	var touched []string
	for _, c := range changes {
		switch c.Op {
		case storage.ChangeRename:
			a.externalRenamed(c.Path, c.To)
			touched = append(touched, c.To)
		case storage.ChangeDelete:
			a.externalDeleted(c.Path)
		case storage.ChangeWrite:
			written[c.Path] = true
			touched = append(touched, c.Path)
		}
	}
	if a.tree != nil {
		a.tree.Touched(touched)
	}
	// After the loop, so a note written and then moved or deleted is looked at
	// where it is now, or not at all.
	if a.currentNote != "" && written[a.currentNote] {
		a.externalWritten(a.currentNote)
	}
	if p := a.side; p != nil && p.rel != "" && written[p.rel] {
		p.externalWritten()
	}
}

// movedPath is where a note at rel ends up when the note or folder at from is
// moved to to: rel itself, or rel under a moved folder with its prefix
// rewritten. ok is false when the move does not touch rel.
func movedPath(rel, from, to string) (string, bool) {
	switch {
	case from == "" || rel == "":
		return rel, false
	case rel == from:
		return to, true
	case strings.HasPrefix(rel, from+"/"):
		return to + strings.TrimPrefix(rel, from), true
	}
	return rel, false
}

// pathAffected is whether a note at rel is the one at p or inside the folder
// at p, and whether p must therefore be a folder.
func pathAffected(rel, p string) (affected, isFolder bool) {
	switch {
	case p == "" || rel == "":
		return false, false
	case rel == p:
		return true, false
	case strings.HasPrefix(rel, p+"/"):
		return true, true
	}
	return false, false
}

// externalRenamed follows the open notes through a move made outside.
func (a *App) externalRenamed(from, to string) {
	if cur, ok := movedPath(a.currentNote, from, to); ok {
		wasSaved := a.savedNote == a.currentNote
		a.currentNote = cur
		a.cfg.LastNote = cur
		if wasSaved {
			a.savedNote = cur
		}
		a.bindImages(cur) // its pictures are relative to it, wherever it is now
		a.refreshHeader()
		if a.tree != nil {
			a.tree.SetCurrent(cur)
		}
	}
	a.onRenamed(from, to, true) // the side pane, which takes a note or a folder alike
}

// externalDeleted lets go of the open notes that were moved to the Trash.
func (a *App) externalDeleted(p string) {
	if ok, folder := pathAffected(a.currentNote, p); ok {
		a.onDeleted(p, folder)
	}
	if a.side != nil {
		if ok, folder := pathAffected(a.side.rel, p); ok {
			a.onDeleted(p, folder)
		}
	}
}

// changedWhileEditing is what the person is told when their text and the
// outside change both exist.
const changedWhileEditing = "Claude Code changed this note while you were editing. " +
	"Its version is in Version History."

// claudeEdited tells the person, as it happens, that Claude Code has just
// changed a note they have open. Edits that come while the message is still up
// add to it instead of stacking up messages: Claude Code often makes several
// in a row. For the main pane's note the message offers the changes, which the
// version history shows against the text from before the edit.
func (a *App) claudeEdited(name string, main bool) {
	if a.toastOverlay == nil {
		return
	}
	title := "Claude Code edited this note"
	if !main {
		title = "Claude Code edited “" + name + "”"
	}
	if t := a.claudeToast; t != nil && a.claudeToastMain == main && a.claudeToastNote == name {
		a.claudeEdits++
		t.SetTitle(fmt.Sprintf("%s · %d changes", title, a.claudeEdits))
		diag.Event("toast", "text", t.Title())
		return
	}
	if a.claudeToast != nil {
		a.claudeToast.Dismiss()
	}
	t := adw.NewToast(title)
	t.SetUseMarkup(false) // a note's name may hold an & or a <
	t.SetTimeout(5)
	if main {
		t.SetButtonLabel("Show Changes")
		t.ConnectButtonClicked(a.showChangesSinceEdit)
	}
	t.ConnectDismissed(func() {
		if a.claudeToast == t {
			a.claudeToast = nil
		}
	})
	a.claudeToast, a.claudeToastMain, a.claudeToastNote, a.claudeEdits = t, main, name, 1
	diag.Event("toast", "text", title)
	a.toastOverlay.AddToast(t)
}

// showChangesSinceEdit opens the open note's version history on the page that
// compares it with an earlier version, which after an outside edit is the text
// from before it.
func (a *App) showChangesSinceEdit() {
	historyOpensOnChanges = true
	defer func() { historyOpensOnChanges = false }()
	a.showHistory()
}

// externalWritten deals with the main pane's note having been written outside.
func (a *App) externalWritten(rel string) {
	if a.editor == nil {
		return
	}
	if a.saveInFlight {
		diag.Event("external.main", "rel", rel, "result", "waiting for a save")
		// Looked at again once the save has landed, so its idea of what is on
		// disk is settled first.
		coreglib.TimeoutAdd(externalDelayMs, func() bool {
			if !a.closing && a.currentNote == rel {
				a.externalWritten(rel)
			}
			return false
		})
		return
	}
	text, err := a.store.ReadNote(rel)
	if err != nil || a.contentUnchanged(rel, text) {
		diag.Event("external.main", "rel", rel, "result", "unchanged or unreadable", "err", err)
		return
	}
	if a.dirty {
		// Keep the outside version before the person's next save replaces it.
		// Their text stays; a save of it is what the vault ends up with.
		if err := a.store.KeepCurrentVersion(rel); err != nil {
			log.Printf("atlas-notes: keep %q: %v", rel, err)
		}
		a.rememberSaved(rel, text)
		diag.Event("external.main", "rel", rel, "result", "kept typing, outside text to history")
		a.toast(changedWhileEditing)
		return
	}
	// Not through openNote: that would take focus and play the open animation.
	// The caret and the scroll stay where they were, so a note can be watched
	// while Claude Code works on it.
	a.rememberSaved(rel, text)
	before := a.editor.Content()
	a.editor.ReplaceContent(text)
	a.editor.FlashLines(changedRuns(before, text))
	a.refreshBacklinks()
	a.refreshEmbeds()
	a.updateStats()
	diag.Event("external.main", "rel", rel, "result", "reloaded")
	a.claudeEdited(path.Base(rel), true)
}

// externalWritten is externalWritten for the side pane.
func (p *sidePane) externalWritten() {
	a := p.a
	rel := p.rel
	if p.inFlight {
		coreglib.TimeoutAdd(externalDelayMs, func() bool {
			if !a.closing && a.side == p && p.rel == rel {
				p.externalWritten()
			}
			return false
		})
		return
	}
	text, err := a.store.ReadNote(rel)
	if err != nil || p.unchanged(rel, text) {
		diag.Event("external.side", "rel", rel, "result", "unchanged or unreadable", "err", err)
		return
	}
	if p.dirty {
		if err := a.store.KeepCurrentVersion(rel); err != nil {
			log.Printf("atlas-notes: keep %q: %v", rel, err)
		}
		p.remember(rel, text)
		diag.Event("external.side", "rel", rel, "result", "kept typing, outside text to history")
		a.toast(changedWhileEditing)
		return
	}
	p.remember(rel, text)
	before := p.ed.Content()
	p.ed.ReplaceContent(text)
	p.ed.FlashLines(changedRuns(before, text))
	a.refreshEmbeds()
	diag.Event("external.side", "rel", rel, "result", "reloaded")
	a.claudeEdited(path.Base(rel), false)
}

// maxDiffCells bounds the table changedRuns fills to match lines: past it, the
// lines between the first and last change are all taken as changed.
const maxDiffCells = 1 << 20

// changedRuns says which lines of after differ from before, as runs [from, to]
// counted from 0, for the tint that shows where an outside edit landed. Lines
// only removed are shown by the line that now stands where they were.
func changedRuns(before, after string) [][2]int {
	if before == after {
		return nil
	}
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	am, bm := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(bm) == 0 {
		at := min(head, len(b)-1)
		return [][2]int{{at, at}} // only removed: where they were
	}
	if len(am) == 0 || (len(am)+1)*(len(bm)+1) > maxDiffCells {
		return [][2]int{{head, head + len(bm) - 1}}
	}
	// The longest run of lines the two share, so that two edits far apart in a
	// note light up as two places rather than everything between them.
	n, m := len(am), len(bm)
	lcs := make([]int32, (n+1)*(m+1))
	at := func(i, j int) int32 { return lcs[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			v := at(i+1, j)
			if am[i] == bm[j] {
				v = at(i+1, j+1) + 1
			} else if w := at(i, j+1); w > v {
				v = w
			}
			lcs[i*(m+1)+j] = v
		}
	}
	var runs [][2]int
	mark := func(line int) {
		if k := len(runs) - 1; k >= 0 && runs[k][1] >= line-1 {
			runs[k][1] = max(runs[k][1], line)
			return
		}
		runs = append(runs, [2]int{line, line})
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case am[i] == bm[j]:
			i, j = i+1, j+1
		case at(i+1, j) >= at(i, j+1):
			mark(head + j) // a line removed: shown by the one now in its place
			i++
		default:
			mark(head + j)
			j++
		}
	}
	for ; j < m; j++ {
		mark(head + j)
	}
	if i < n && j == m {
		mark(min(head+m, len(b)-1)) // removed at the end of the changed part
	}
	return runs
}
