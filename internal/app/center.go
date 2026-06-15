package app

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/editor"
)

// buildCenter assembles the center panel: note header (title + save state),
// formatting toolbar, the editor, and a word/character status bar.
func (a *App) buildCenter() *gtk.Box {
	center := gtk.NewBox(gtk.OrientationVertical, 0)
	center.SetHExpand(true)
	center.AddCSSClass("center-panel")

	center.Append(a.buildNoteHeader())
	center.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	a.editor = editor.New()
	a.editor.OnChanged = a.onEditorChanged
	a.editor.OnReparsed = a.onEditorReparsed
	center.Append(a.editor.Widget())

	center.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	a.statusLabel = gtk.NewLabel("0 words · 0 characters")
	a.statusLabel.SetXAlign(0)
	a.statusLabel.AddCSSClass("status-bar")
	a.statusLabel.SetMarginStart(12)
	a.statusLabel.SetMarginEnd(12)
	a.statusLabel.SetMarginTop(4)
	a.statusLabel.SetMarginBottom(4)
	center.Append(a.statusLabel)

	return center
}

func (a *App) buildNoteHeader() *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.AddCSSClass("note-header")
	row.SetMarginStart(12)
	row.SetMarginEnd(12)
	row.SetMarginTop(8)
	row.SetMarginBottom(4)

	a.titleEntry = gtk.NewEntry()
	a.titleEntry.SetHExpand(true)
	a.titleEntry.SetPlaceholderText("Untitled")
	a.titleEntry.AddCSSClass("note-title")
	a.titleEntry.ConnectActivate(a.onTitleActivate)

	a.saveLabel = gtk.NewLabel("Saved")
	a.saveLabel.AddCSSClass("save-indicator")

	row.Append(a.titleEntry)
	row.Append(a.saveLabel)
	return row
}
