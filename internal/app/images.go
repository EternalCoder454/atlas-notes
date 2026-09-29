package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/imagefit"
	"atlas-notes/internal/storage"
)

// Inserting an image from a file. The picture is copied into the vault's
// attachments folder by the store, which also shrinks it and strips its
// metadata; the note only gets a Markdown link to the copy.

// maxImageFileBytes caps how much of a chosen file is read. A photo straight
// from a camera is a few megabytes, so a file over this is not one; and it is
// held in memory whole while it is decoded.
const maxImageFileBytes = 50 << 20

// imageSuffixes are the file types offered in the picker. The list is what the
// store's image decoder is expected to take; anything it cannot read is refused
// with a message after the pick, as a file with the wrong extension may be a
// perfectly good picture or none at all.
var imageSuffixes = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp", "tiff", "avif", "heic"}

// errImageFileTooLarge is a chosen file over maxImageFileBytes.
var errImageFileTooLarge = errors.New("the image file is too large")

// imageMarkdown is what goes into the note for an image: the link on a line of
// its own, since an image in the middle of a sentence is not how a note
// reads. It starts a new line unless the caret is already at the start of one.
func imageMarkdown(mdPath string, atLineStart bool) string {
	link := "![](" + mdPath + ")\n"
	if atLineStart {
		return link
	}
	return "\n" + link
}

// insertImage asks for an image file and puts it into the open note.
func (a *App) insertImage() {
	if a.store == nil || a.editor == nil || !a.noteOpen() {
		a.toast("Open a note to add an image to it")
		return
	}
	rel := a.currentNote

	filter := gtk.NewFileFilter()
	filter.SetName("Images")
	for _, s := range imageSuffixes {
		filter.AddSuffix(s) // matched without regard to case
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)

	chooser := gtk.NewFileDialog()
	chooser.SetTitle("Insert Image")
	chooser.SetFilters(filters)
	chooser.SetDefaultFilter(filter)
	chooser.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := chooser.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		target := file.Path()
		if target == "" {
			a.toast("Couldn't add that image: that place has no file path.")
			return
		}
		a.addImage(rel, target)
	})
}

// addImage reads the file and stores it for the note off the main thread, then
// inserts the link on it. Decoding and shrinking a large picture takes long
// enough to be seen, and the window must not stop while it does.
func (a *App) addImage(rel, target string) {
	go func() {
		mdPath, err := a.storeImageFile(rel, target)
		coreglib.IdleAdd(func() bool {
			if a.closing {
				return false
			}
			if err != nil {
				log.Printf("atlas-notes: insert image %q: %v", target, err)
				a.toast(imageErrorText(err))
				return false
			}
			if a.currentNote != rel || a.editor == nil || !a.noteOpen() {
				// The image is in the vault by now; writing its link into some
				// other note would be worse than not writing it.
				a.toast("Added the image, but you had moved to another note")
				return false
			}
			a.editor.InsertAtCursor(imageMarkdown(mdPath, a.caretAtLineStart()))
			a.editor.Focus()
			return false
		})
	}()
}

// storeImageFile reads a picture from disk and saves it as an attachment of the
// note, returning the path to link to.
func (a *App) storeImageFile(rel, target string) (string, error) {
	data, err := readImageFile(target)
	if err != nil {
		return "", err
	}
	return a.store.SaveAttachment(rel, data)
}

// readImageFile reads a regular file of at most maxImageFileBytes. It looks
// before it opens: opening a named pipe waits for a writer that may never come.
func readImageFile(p string) ([]byte, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, imagefit.ErrNotImage
	}
	if info.Size() > maxImageFileBytes {
		return nil, errImageFileTooLarge
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// One byte past the cap tells a file that grew after the stat from one
	// that fits.
	data, err := io.ReadAll(io.LimitReader(f, maxImageFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageFileBytes {
		return nil, errImageFileTooLarge
	}
	return data, nil
}

// imageErrorText is what to say about a failed insert, in plain words.
func imageErrorText(err error) string {
	switch {
	case errors.Is(err, imagefit.ErrNotImage):
		return "That file is not an image that can be added"
	case errors.Is(err, imagefit.ErrTooLarge), errors.Is(err, errImageFileTooLarge):
		return "That image is too large"
	case errors.Is(err, storage.ErrLocked):
		return "Unlock the vault to add an image to a locked note"
	default:
		return err.Error()
	}
}

// caretAtLineStart reports whether the caret, or the start of the selection
// that typing would replace, is at the beginning of a line. The editor keeps
// its text view to itself, so it is found through the widget tree; when it
// cannot be found the answer is no, which costs an empty line and nothing else.
func (a *App) caretAtLineStart() bool {
	if a.editor == nil {
		return false
	}
	scroll, ok := a.editor.Widget().(*gtk.ScrolledWindow)
	if !ok {
		return false
	}
	view, ok := scroll.Child().(*gtk.TextView)
	if !ok {
		return false
	}
	buf := view.Buffer()
	if start, _, ok := buf.SelectionBounds(); ok {
		return start.StartsLine()
	}
	return buf.IterAtMark(buf.GetInsert()).StartsLine()
}
