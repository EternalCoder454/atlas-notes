//go:build android || gio

package mobile

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"atlas-notes/internal/storage"
)

// screen is which of the three things the app is showing.
type screen int

const (
	screenList screen = iota
	screenEditor
	screenPassword
)

// autosaveDelay is how long after the last keystroke a note is written. The
// same value the desktop app uses, for the same reason: a save should be
// timely without turning every keystroke into a write.
const autosaveDelay = 1500 * time.Millisecond

// App is the whole interface. It holds the vault, which screen is showing, and
// the widget state that has to survive between frames.
type App struct {
	store *storage.Store
	theme *material.Theme
	win   *app.Window

	screen screen
	notes  []storage.NoteMeta
	err    string // shown in a bar along the bottom until the next action

	// The list.
	listState  widget.List
	search     widget.Editor
	searchBtn  widget.Clickable
	searchOpen bool
	rows       []*noteRow
	newBtn     widget.Clickable

	// The editor.
	current    string // vault-relative path of the open note
	title      widget.Editor
	body       widget.Editor
	bodyState  widget.List
	backBtn    widget.Clickable
	deleteBtn  widget.Clickable
	tasks      []taskRow
	dirty      bool
	saveAt     time.Time
	lastSaved  string
	editorLock bool // the open note is encrypted

	// The update banner. The check runs on a goroutine, so what it finds is
	// behind a mutex; everything else here is touched only while laying out a
	// frame, which is one goroutine by construction.
	mu              sync.Mutex
	update          updateState
	updateClose     widget.Clickable
	updateDismissed bool

	// The password prompt.
	password    widget.Editor
	unlockBtn   widget.Clickable
	cancelBtn   widget.Clickable
	afterUnlock func()
}

// noteRow is one row of the vault list. Rows are kept between frames because
// each carries its own click state.
type noteRow struct {
	meta  storage.NoteMeta
	click widget.Clickable
}

// taskRow is one checklist item in the open note, with the line it came from.
type taskRow struct {
	line  int
	text  string
	check widget.Bool
}

// Run opens the vault and runs the interface until the window closes. version
// is what this build calls itself, which the update check compares against
// what has been published.
func Run(version string) error {
	dir, err := app.DataDir()
	if err != nil {
		return fmt.Errorf("no place to keep notes: %w", err)
	}
	// The storage layer reads this to find the vault. On a phone the operating
	// system decides where an application's files go and tells it at runtime,
	// so it is set here rather than guessed at down there.
	if os.Getenv("ATLAS_DATA_HOME") == "" {
		os.Setenv("ATLAS_DATA_HOME", dir)
	}
	if os.Getenv("ATLAS_CONFIG_HOME") == "" {
		os.Setenv("ATLAS_CONFIG_HOME", dir)
	}

	store, err := storage.Open("", "")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer store.Close()
	if store.IsIndexEmpty() {
		if err := store.Reindex(); err != nil {
			log.Printf("atlas-notes: reindex: %v", err)
		}
		if err := store.EnsureWelcome(); err != nil {
			log.Printf("atlas-notes: welcome note: %v", err)
		}
	}

	a := &App{store: store, theme: newTheme()}
	a.search.SingleLine = true
	a.title.SingleLine = true
	a.password.SingleLine = true
	a.password.Mask = '•'
	a.listState.Axis = layout.Vertical
	a.bodyState.Axis = layout.Vertical
	a.reload()

	w := new(app.Window)
	w.Option(app.Title("Atlas Notes"))
	a.win = w
	a.startUpdateCheck(version)

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			a.flush()
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.handleBack(gtx)
			a.tick()
			a.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// handleBack makes the system back gesture leave the note rather than the app,
// which is what it does in every other Android application.
func (a *App) handleBack(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameBack})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press && a.screen != screenList {
			a.closeNote()
		}
	}
}

// tick writes the open note once typing has paused.
func (a *App) tick() {
	if !a.dirty || a.saveAt.IsZero() || time.Now().Before(a.saveAt) {
		if a.dirty && !a.saveAt.IsZero() {
			a.win.Invalidate() // come back when the delay is up
		}
		return
	}
	a.flush()
}

// flush writes the open note now.
func (a *App) flush() {
	if !a.dirty || a.current == "" {
		return
	}
	a.dirty = false
	a.saveAt = time.Time{}
	content := a.body.Text()
	if content == a.lastSaved {
		return
	}
	if err := a.store.WriteNote(a.current, content); err != nil {
		a.fail("Couldn't save", err)
		return
	}
	a.lastSaved = content
	a.reload()
}

// markDirty schedules a save.
func (a *App) markDirty() {
	a.dirty = true
	a.saveAt = time.Now().Add(autosaveDelay)
	a.win.Invalidate()
}

// reload re-reads the note list from the index.
func (a *App) reload() {
	notes, err := a.store.ListNotes()
	if err != nil {
		a.fail("Couldn't read the vault", err)
		return
	}
	a.notes = notes
	a.rebuildRows()
}

// fail puts a message along the bottom of the screen.
func (a *App) fail(what string, err error) {
	a.err = what + ": " + err.Error()
	log.Printf("atlas-notes: %s: %v", what, err)
}

// openNote shows a note, asking for the password first when it needs one.
func (a *App) openNote(rel string) {
	content, err := a.store.ReadNote(rel)
	if errors.Is(err, storage.ErrLocked) {
		a.askPassword(func() { a.openNote(rel) })
		return
	}
	if err != nil {
		a.fail("Couldn't open that note", err)
		return
	}
	a.current = rel
	a.lastSaved = content
	a.dirty = false
	a.editorLock = a.store.IsNoteLocked(rel)
	a.title.SetText(filepath.Base(rel))
	a.body.SetText(content)
	a.rebuildTasks()
	a.screen = screenEditor
	a.err = ""
}

// closeNote saves and goes back to the list.
func (a *App) closeNote() {
	a.flush()
	a.current = ""
	a.screen = screenList
	a.reload()
}

// newNote creates one and opens it.
func (a *App) newNote() {
	rel := a.store.UniqueName("", "Untitled")
	if err := a.store.WriteNote(rel, "# "+filepath.Base(rel)+"\n\n"); err != nil {
		a.fail("Couldn't create a note", err)
		return
	}
	a.reload()
	a.openNote(rel)
}

// deleteCurrent removes the open note.
func (a *App) deleteCurrent() {
	if a.current == "" {
		return
	}
	rel := a.current
	a.dirty = false
	a.current = ""
	if err := a.store.DeleteNote(rel); err != nil {
		a.fail("Couldn't delete that note", err)
	}
	a.screen = screenList
	a.reload()
}

// askPassword shows the prompt and runs next once the vault is open.
func (a *App) askPassword(next func()) {
	a.afterUnlock = next
	a.password.SetText("")
	a.screen = screenPassword
	a.err = ""
}

// submitPassword unlocks, or sets the password if there is not one yet.
func (a *App) submitPassword() {
	pw := a.password.Text()
	if pw == "" {
		return
	}
	var err error
	if a.store.HasPassword() {
		err = a.store.Unlock(pw)
	} else {
		err = a.store.SetPassword(pw)
	}
	if err != nil {
		a.err = "That password didn't work."
		return
	}
	next := a.afterUnlock
	a.afterUnlock = nil
	a.screen = screenList
	a.err = ""
	if next != nil {
		next()
	}
}

// layout draws whichever screen is showing, inset for the system bars so
// nothing sits under a notch or a gesture handle.
func (a *App) layout(gtx layout.Context) layout.Dimensions {
	paintBackground(gtx, colorSurface)
	return layout.Inset{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				switch a.screen {
				case screenEditor:
					return a.layoutEditor(gtx)
				case screenPassword:
					return a.layoutPassword(gtx)
				default:
					return a.layoutList(gtx)
				}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.layoutError(gtx)
			}),
		)
	})
}

// layoutError is the bar along the bottom, drawn only when there is something
// to say.
func (a *App) layoutError(gtx layout.Context) layout.Dimensions {
	if a.err == "" {
		return layout.Dimensions{}
	}
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(a.theme, a.err)
		lbl.Color = colorDanger
		return lbl.Layout(gtx)
	})
}
