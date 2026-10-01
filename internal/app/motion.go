package app

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ui"
)

// The center pages play in rather than simply appearing: the home screen's
// column rises into place a piece at a time, and a note's title and text come
// up together as it opens. The animations themselves are in the stylesheet
// (the "rise" and "note-in" entrances); ui.Replay starts them.

// playWelcome plays the home screen in: its mark, greeting and subtitle, then
// each card, then the lists below them.
func (a *App) playWelcome() {
	if a.welcomeContent == nil {
		return
	}
	step := 0
	for c := a.welcomeContent.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		w := gtk.BaseWidget(c)
		if !w.Visible() {
			continue
		}
		if grid, ok := c.(*gtk.Grid); ok {
			step = ui.ReplayChildren(grid, "rise", step)
			continue
		}
		ui.ReplayAt(c, "rise", step)
		step++
	}
}

// playNoteIn plays the note's title row in as a different note opens.
//
// The text is left alone. The first frame after a note loads is the one that
// lays its text out, which for a long note takes long enough that a fade
// started with it is mostly over before it is drawn, and holding the text back
// until then shows an empty page instead. The title row is cheap to draw, so
// it carries the change.
func (a *App) playNoteIn() {
	for i, w := range a.noteIn {
		ui.ReplayAt(w, "note-in", i)
	}
}
