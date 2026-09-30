package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/editor"
	"atlas-notes/internal/export"
	"atlas-notes/internal/storage"
)

// exportCurrent exports the note that is open, saving it first so the export
// is what is on screen.
func (a *App) exportCurrent() {
	if a.currentNote == "" {
		a.toast("Open a note to export it.")
		return
	}
	a.saveCurrent()
	a.exportNote(a.currentNote)
}

// exportNote asks which format, then where, and writes the note there.
func (a *App) exportNote(rel string) {
	text, err := a.store.ReadNote(rel)
	if errors.Is(err, storage.ErrLocked) {
		a.toast("Unlock this note to export it.")
		return
	}
	if err != nil {
		a.toast("Couldn't read the note: " + err.Error())
		return
	}
	name := path.Base(rel)

	body := "Choose a format. Word and OpenDocument files open in Microsoft Word, " +
		"LibreOffice and Google Docs."
	// An export is a copy outside the vault, and the password does not follow
	// it. Someone exporting a protected note should hear that before, not find
	// out after.
	if a.store.IsNoteLocked(rel) {
		body += "\n\nThis note is protected, but the exported copy will not be: anyone " +
			"who can open the file can read it."
	}
	dialog := adw.NewAlertDialog("Export “"+name+"”", body)
	for _, f := range export.Formats {
		dialog.AddResponse(f.ID, f.Name)
	}
	dialog.AddResponse("cancel", "Cancel")
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("docx")
	dialog.SetResponseAppearance("docx", adw.ResponseSuggested)
	dialog.ConnectResponse(func(id string) {
		f, ok := export.Lookup(id)
		if !ok {
			return
		}
		a.saveExport(rel, name, text, f)
	})
	dialog.Present(a.win)
}

// saveExport renders the note and asks where to put it.
func (a *App) saveExport(rel, name, text string, f export.Format) {
	// The note's pictures go into the document, read as the note names them.
	opt := export.Options{Image: func(p string) ([]byte, error) { return a.store.ReadAttachment(rel, p) },
		Diagram: editor.RenderDiagramPNG}
	data, err := export.RenderWith(f.ID, name, text, opt)
	if err != nil {
		a.toast("Couldn't export: " + err.Error())
		return
	}
	chooser := gtk.NewFileDialog()
	chooser.SetTitle("Export as " + f.Name)
	chooser.SetInitialName(export.FileName(name, f))
	chooser.Save(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := chooser.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		target := file.Path()
		if target == "" {
			a.toast("Couldn't export there: that place has no file path.")
			return
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			log.Printf("atlas-notes: export: %v", err)
			a.toast("Couldn't export: " + err.Error())
			return
		}
		a.toast("Exported “" + path.Base(target) + "”")
	})
}
