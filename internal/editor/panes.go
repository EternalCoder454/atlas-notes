package editor

import (
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/markup"
)

// What the split and side-by-side features need from the editor: the selected
// text as Markdown, replacing it in one undo step, and a way to open a link in
// the second pane. Which note any of it means is the app's business; the editor
// only hands over what was chosen.

// sideHooks is the state behind those features, kept in one place so that
// editor.go carries a single field for it.
type sideHooks struct {
	// openNote is called with a link's target and heading when it is opened to the
	// side, and moveSelection when the context menu asks to move the selection
	// into a new note. Either may be nil, which leaves that menu entry out.
	openNote      func(target, heading string)
	moveSelection func()

	// link is the note link the pointer was over when the context menu was
	// asked for; see installSide.
	link     markup.Span
	haveLink bool

	moveAct, linkAct *gio.SimpleAction
}

// SetSideHandlers connects the context menu's "Move Selection to a New Note" and
// "Open Link to the Side", and Alt+click on a link, to the app. Either may be
// nil.
func (e *Editor) SetSideHandlers(openNote func(target, heading string), moveSelection func()) {
	e.side.openNote, e.side.moveSelection = openNote, moveSelection
	e.syncSideActions()
}

// HasFocus reports whether the caret is in this editor, which is how the app
// tells which of two panes a shortcut is meant for.
func (e *Editor) HasFocus() bool { return e.view.HasFocus() }

// Selection is the selected text as Markdown, and where it is (in characters
// from the start of the note), or ok false when nothing is selected. A task's
// checkbox is written back out as "- [ ] " or "- [x] ", so what comes back is
// what the note's file would hold for those lines.
func (e *Editor) Selection() (from, to int, text string, ok bool) {
	start, end, has := e.buffer.SelectionBounds()
	if !has {
		return 0, 0, "", false
	}
	return start.Offset(), end.Offset(), e.selectionSource(start, end), true
}

// selectionSource is the Markdown between two places in the buffer.
func (e *Editor) selectionSource(start, end *gtk.TextIter) string {
	text := e.sourceSlice(start, end)
	if !strings.Contains(text, anchorChar) {
		return text
	}
	lines := strings.Split(text, "\n")
	first := start.Line()
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, anchorChar); ok {
			box := "[ ]"
			if e.anchorChecked(first + i) {
				box = "[x]"
			}
			lines[i] = "- " + box + " " + rest
		}
	}
	return strings.Join(lines, "\n")
}

// ReplaceSpan replaces the text between two offsets with with, as one step of
// the undo history, and puts the caret after it. It does so only if that text
// is still expect, which is what Selection returned: a dialog was open in
// between, and if anything changed the note meanwhile, replacing a span that is
// no longer the same text would eat the wrong words. It reports whether it
// replaced.
func (e *Editor) ReplaceSpan(from, to int, expect, with string) bool {
	start, end := e.buffer.IterAtOffset(from), e.buffer.IterAtOffset(to)
	if from >= to || e.selectionSource(start, end) != expect {
		return false
	}
	// A link typed for the person would open the suggestion popover on its way
	// in, under text that is already finished.
	e.sg.busy = true
	e.buffer.BeginUserAction()
	e.buffer.Delete(start, end)
	e.buffer.Insert(e.buffer.IterAtOffset(from), with)
	e.buffer.EndUserAction()
	e.sg.busy = false
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(from + len([]rune(with))))
	return true
}

// installSide adds the two context-menu entries and Alt+click. The entries are
// hidden when they do not apply, rather than shown greyed: most right-clicks
// are on plain text, and two dead items on every one of them would be noise.
func (e *Editor) installSide() {
	group := gio.NewSimpleActionGroup()
	e.side.moveAct = gio.NewSimpleAction("move-selection", nil)
	e.side.moveAct.ConnectActivate(func(*glib.Variant) {
		if cb := e.side.moveSelection; cb != nil {
			// From an idle callback: the app opens a dialog, and that should not
			// happen inside the menu's own activation.
			coreglib.IdleAdd(func() bool { cb(); return false })
		}
	})
	e.side.linkAct = gio.NewSimpleAction("open-link-side", nil)
	e.side.linkAct.ConnectActivate(func(*glib.Variant) {
		if cb, l := e.side.openNote, e.side.link; cb != nil && e.side.haveLink {
			coreglib.IdleAdd(func() bool { cb(l.Target, l.Heading); return false })
		}
	})
	group.AddAction(e.side.moveAct)
	group.AddAction(e.side.linkAct)
	e.view.InsertActionGroup("atlas", group)

	menu := gio.NewMenu()
	for _, it := range []struct{ label, action string }{
		{"Open Link to the Side", "atlas.open-link-side"},
		{"Move Selection to a New Note…", "atlas.move-selection"},
	} {
		item := gio.NewMenuItem(it.label, it.action)
		item.SetAttributeValue("hidden-when", glib.NewVariantString("action-disabled"))
		menu.AppendItem(item)
	}
	e.view.SetExtraMenu(menu)

	// The right button is looked at on the way down, before the view builds its
	// menu, to learn whether a link is under the pointer. The gesture never claims
	// the click, so the menu opens as it always did.
	right := gtk.NewGestureClick()
	right.SetButton(3)
	right.SetPropagationPhase(gtk.PhaseCapture)
	right.ConnectPressed(func(_ int, x, y float64) {
		e.side.haveLink = false
		if !e.layoutStale {
			if sp, _, ok := e.linkUnder(x, y); ok && sp.Kind == markup.KindWikiLink {
				e.side.link, e.side.haveLink = sp, true
			}
		}
		e.syncSideActions()
	})
	e.view.AddController(right)
	e.buffer.NotifyProperty("has-selection", e.syncSideActions)
	e.syncSideActions()
}

// syncSideActions enables each entry only when there is something for it to act
// on and someone to act.
func (e *Editor) syncSideActions() {
	if e.side.moveAct == nil {
		return
	}
	e.side.moveAct.SetEnabled(e.side.moveSelection != nil && e.buffer.HasSelection())
	e.side.linkAct.SetEnabled(e.side.openNote != nil && e.side.haveLink)
}

// openLinkSide handles a click that asks for the link to open to the side: Alt
// and click, or Ctrl, Shift and click. Ctrl alone already follows the link in
// this pane. It reports whether it took the click. (Some window managers keep
// Alt and click for moving the window; the context menu and Ctrl+Shift+click
// are there for those.)
func (e *Editor) openLinkSide(p linkPress, state gdk.ModifierType) bool {
	side := state&gdk.AltMask != 0 || state&(gdk.ControlMask|gdk.ShiftMask) == gdk.ControlMask|gdk.ShiftMask
	if !side || p.kind != markup.KindWikiLink || e.side.openNote == nil {
		return false
	}
	cb := e.side.openNote
	coreglib.IdleAdd(func() bool { cb(p.target, p.heading); return false })
	return true
}

// SpanIs reports whether the text between two offsets is still expect.
func (e *Editor) SpanIs(from, to int, expect string) bool {
	if from >= to {
		return false
	}
	return e.selectionSource(e.buffer.IterAtOffset(from), e.buffer.IterAtOffset(to)) == expect
}
