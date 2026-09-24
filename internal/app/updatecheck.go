package app

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/update"
)

// Checking for a new version on launch.
//
// The check runs once, on a background thread, after the window is on screen —
// it must never be something the user waits for, and a machine that is offline
// must behave exactly like one that is up to date: silently.
//
// It is a plain read of a text file from the project's repository. Nothing
// about the machine or its notes is sent, and the whole thing can be turned off
// in Settings.

// updateCheckDelay gives the window a moment to settle before the check runs,
// so a launch is never competing with a network request.
const updateCheckDelay = 1500

// maybeCheckForUpdate starts the launch-time update check, unless the user has
// switched it off or this is a measurement run.
func (a *App) maybeCheckForUpdate() {
	if !a.cfg.CheckUpdates || os.Getenv("ATLAS_NO_UPDATE_CHECK") != "" {
		return
	}
	// A local notes file can be pointed at to exercise the whole path without
	// waiting for a release to be published.
	notesURL := os.Getenv("ATLAS_UPDATE_NOTES_URL")
	// Measurement and screenshot runs stay off the network — unless the run is
	// here to test this path, which pointing it at notes of its own asks for.
	if devRun() && notesURL == "" {
		return
	}
	branch := channelBranch(a.cfg.UpdateChannel)
	checker := update.New()
	if notesURL != "" {
		checker.NotesURL = notesURL
	}
	coreglib.TimeoutAdd(updateCheckDelay, func() bool {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rel, err := checker.Check(ctx, branch, version)
			coreglib.IdleAdd(func() bool {
				switch {
				case err != nil:
					// Offline, or GitHub is having a day. Not worth a dialog.
					log.Printf("atlas-notes: update check: %v", err)
				case rel != nil:
					a.showUpdateFound(rel)
				}
				return false
			})
		}()
		return false
	})
}

// showUpdateFound presents what the new version brings, and offers to install
// it now or leave it for later.
func (a *App) showUpdateFound(rel *update.Release) {
	if a.win == nil {
		return
	}
	dialog := adw.NewAlertDialog("Update Found — v"+rel.Version, "")
	dialog.SetExtraChild(updateNotes(rel))
	dialog.AddResponse("later", "Update Later")
	dialog.AddResponse("now", "Update Now")
	dialog.SetResponseAppearance("now", adw.ResponseSuggested)
	dialog.SetDefaultResponse("now")
	dialog.SetCloseResponse("later")
	dialog.ConnectResponse(func(response string) {
		if response == "now" {
			a.startUpdate()
		}
	})
	dialog.Present(a.win)
}

// notesWidthChars is the widest a release note line may run before it wraps.
// It is an upper bound, not the dialog's width: AdwAlertDialog has a maximum
// of its own and usually wins. It exists so a long line cannot stretch the
// dialog across the screen.
const notesWidthChars = 56

// updateNotes renders the release's notes as the short bullet list the dialog
// shows. The lines come from the notes written for people using Atlas Notes,
// not from the developers' changelog.
//
// AdwAlertDialog already scrolls its extra child when a window is too short for
// it, against the height actually available, so the list is a plain box: a
// scroller of its own could only cap it lower and scroll where there was room.
func updateNotes(rel *update.Release) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.AddCSSClass("update-notes")

	intro := gtk.NewLabel("What's new in this version:")
	intro.SetXAlign(0)
	intro.AddCSSClass("update-notes-intro")
	box.Append(intro)

	if len(rel.Notes) == 0 {
		// A release that says nothing about itself still deserves an honest
		// line rather than an empty dialog.
		line := gtk.NewLabel("Small fixes and improvements.")
		line.SetXAlign(0)
		line.SetWrap(true)
		box.Append(line)
		return box
	}
	for _, note := range rel.Notes {
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		bullet := gtk.NewLabel("•")
		bullet.SetVAlign(gtk.AlignStart)
		bullet.AddCSSClass("update-bullet")
		row.Append(bullet)

		text := gtk.NewLabel(note)
		text.SetXAlign(0)
		text.SetWrap(true)
		text.SetMaxWidthChars(notesWidthChars)
		row.Append(text)
		box.Append(row)
	}
	return box
}

// startUpdate installs the new version and restarts into it, reporting progress
// in a dialog of its own. It is the same install the Settings page runs.
func (a *App) startUpdate() {
	progress := adw.NewAlertDialog("Updating Atlas Notes", "")
	body := gtk.NewBox(gtk.OrientationVertical, 12)

	spinner := gtk.NewSpinner()
	spinner.SetSizeRequest(28, 28)
	spinner.Start()
	body.Append(spinner)

	status := gtk.NewLabel("Downloading and building the new version…\nThis takes a minute or two.")
	status.SetJustify(gtk.JustifyCenter)
	status.SetWrap(true)
	status.SetMaxWidthChars(42)
	body.Append(status)

	progress.SetExtraChild(body)
	progress.AddResponse("close", "Close")
	progress.SetCloseResponse("close")
	progress.Present(a.win)

	a.installUpdate(channelBranch(a.cfg.UpdateChannel), func(text string, done bool) {
		status.SetText(text)
		if done {
			spinner.Stop()
		}
	})
}
