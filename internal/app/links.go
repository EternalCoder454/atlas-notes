package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/markup"
	"atlas-notes/internal/storage"
)

// The app's side of links between notes: what clicking a link, a tag or a
// web address in the editor does, where the editor's suggestions come from,
// the "Linked from" bar under a note, and keeping the open note right when a
// rename rewrites links on disk.

// wireEditorLinks connects the editor's links, tags and suggestions to the
// vault. The editor knows how to draw and click them; what they lead to is
// the app's business.
func (a *App) wireEditorLinks() {
	e := a.editor
	e.OnOpenNote = a.openLinkedNote
	e.OnOpenTag = a.showTagged
	e.OnOpenURL = a.openURL
	e.NoteNames = a.noteNames
	e.TagNames = a.tagNames
	e.OnImageError = func(err error) { a.toast(imageErrorText(err)) }
}

// bindImages points the editor's pictures at the note being opened: a
// picture's path is relative to the note that names it, so reading and saving
// them has to know which note that is. It is set before the note's text goes
// in, so the pictures load for the right one; the loads themselves run on the
// editor's goroutines and only read rel, which this closure owns.
func (a *App) bindImages(rel string) {
	if a.editor == nil || a.store == nil {
		return
	}
	store := a.store
	a.editor.LoadImage = func(p string) ([]byte, error) { return store.ReadAttachment(rel, p) }
	a.editor.SaveImage = func(data []byte) (string, error) { return store.SaveAttachment(rel, data) }
	a.editor.OnOpenImage = func(p string) {
		file, err := store.AttachmentFile(rel, p)
		switch {
		case errors.Is(err, storage.ErrSealedAttachment):
			a.toast("Pictures in a protected note open only here, so no unencrypted copy is made")
			return
		case err != nil:
			a.toast("Couldn't find that picture")
			return
		}
		if devRun() { // see openURL: nothing leaves a test run for the desktop
			log.Printf("atlas-notes: dev run, not opening %s", file)
			return
		}
		gtk.NewFileLauncher(gio.NewFileForPath(file)).Launch(context.Background(), &a.win.Window, nil)
	}
}

// noteNamesTTL is how long the note list behind the [[ suggestions is reused.
// The suggestions ask on every keystroke of a query, and listing a large vault
// is milliseconds each time; a note made in the last two seconds is rare, and
// following a link to it still works, because following checks the store.
const noteNamesTTL = 2 * time.Second

// noteNames is every note's path, for the [[ suggestions and for resolving a
// link.
func (a *App) noteNames() []string {
	if a.store == nil {
		return nil
	}
	if a.noteNamesCache != nil && time.Since(a.noteNamesAt) < noteNamesTTL {
		return a.noteNamesCache
	}
	notes, err := a.store.ListNotes()
	if err != nil {
		return nil
	}
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = n.Path
	}
	a.noteNamesCache, a.noteNamesAt = out, time.Now()
	return out
}

// tagNames is every tag in the vault, for the # suggestions.
func (a *App) tagNames() []string {
	if a.store == nil {
		return nil
	}
	tags, err := a.store.Tags()
	if err != nil {
		return nil
	}
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.Tag
	}
	return out
}

// openLinkedNote follows a [[link]]. A link to a note that does not exist yet
// makes it, as in every app with links like these: writing [[Idea]] and
// clicking it is how a new note gets started from inside another.
func (a *App) openLinkedNote(target, _ string) {
	if a.store == nil {
		return
	}
	notes := a.noteNames()
	// A link may name the file rather than the note, as other Markdown apps
	// write them: [[Idea.md]] means Idea.
	for _, t := range []string{target, strings.TrimSuffix(target, ".md")} {
		if rel := markup.Resolve(t, notes); rel != "" {
			a.openNote(rel)
			return
		}
	}
	// The name is cleaned as the store cleans it before anything is decided,
	// and a note of that name is opened, never written over: the list the
	// link was resolved against can lag behind a sync.
	rel := storage.CleanNoteName(strings.TrimSuffix(target, ".md"))
	if rel == "" {
		return
	}
	if a.store.NoteExists(rel) {
		a.openNote(rel)
		return
	}
	a.flushDirty()
	if err := a.store.WriteNote(rel, "# "+path.Base(rel)+"\n\n"); err != nil {
		log.Printf("atlas-notes: create linked note: %v", err)
		a.toast("Couldn't create that note: " + err.Error())
		return
	}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.openNote(rel)
	a.toast("Created " + path.Base(rel))
}

// showTagged lists the notes that carry a tag, in the vault panel.
func (a *App) showTagged(tag string) {
	if a.tree == nil {
		return
	}
	if a.leftToggle != nil && !a.leftToggle.Active() {
		a.leftToggle.SetActive(true)
	}
	a.tree.SetSearch("#" + tag)
}

// openURL opens a web address in the default browser. Only web and mail
// addresses are followed: a note can come from anywhere, and a link to a
// file:// path or an unfamiliar scheme could start something that is not a
// browser.
func (a *App) openURL(raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
	default:
		a.toast("Only web and email links are opened")
		return
	}
	// A benchmark, a screenshot or a test run drives the window on a virtual
	// display, but a launcher hands the address to the real desktop's browser
	// through the session bus. A run that clicked a link opened a browser on
	// the desktop of whoever ran it. Such runs open nothing.
	if devRun() {
		log.Printf("atlas-notes: dev run, not opening %s", u.Redacted())
		return
	}
	gtk.NewURILauncher(u.String()).Launch(context.Background(), &a.win.Window, nil)
}

// onBeforeRename saves the open note before a rename: the rename rewrites
// links in other notes' files, the open note may be one of them, and its
// unsaved text would otherwise be written back over the rewrite.
func (a *App) onBeforeRename() { a.flushDirty() }

// onLinksChanged runs after a rename, when links in other notes may have been
// rewritten on disk. If the open note was one of them, the editor is holding
// the old text, so it is read again. Nothing is lost: it was saved first.
func (a *App) onLinksChanged() {
	a.refreshBacklinks()
	if a.store == nil || a.currentNote == "" || a.dirty {
		return
	}
	text, err := a.store.ReadNote(a.currentNote)
	if err != nil || text == a.editorContent() {
		return
	}
	a.rememberSaved(a.currentNote, text)
	a.editor.SetContent(text)
}

// buildBacklinks is the bar under a note that lists the notes linking to it.
// It is hidden while there are none, which is most of the time.
func (a *App) buildBacklinks() *gtk.Box {
	a.backlinksBar = gtk.NewBox(gtk.OrientationHorizontal, 4)
	a.backlinksBar.AddCSSClass("backlinks-bar")
	a.backlinksBar.SetMarginStart(16)
	a.backlinksBar.SetMarginEnd(16)
	a.backlinksBar.SetMarginBottom(4)
	a.backlinksBar.SetVisible(false)
	return a.backlinksBar
}

// refreshBacklinks fills the bar for the open note. The query runs off the
// main thread; a note opened meanwhile makes its answer stale, so it is
// dropped.
func (a *App) refreshBacklinks() {
	if a.backlinksBar == nil || a.store == nil {
		return
	}
	rel := a.currentNote
	if rel == "" {
		a.backlinksBar.SetVisible(false)
		return
	}
	a.backlinksGen++
	gen := a.backlinksGen
	go func() {
		links, err := a.store.Backlinks(rel)
		coreglib.IdleAdd(func() bool {
			if gen != a.backlinksGen || a.closing {
				return false
			}
			if err != nil {
				log.Printf("atlas-notes: backlinks: %v", err)
			}
			a.showBacklinks(links)
			return false
		})
	}()
}

// backlinksShown caps the bar; the rest are counted.
const backlinksShown = 6

func (a *App) showBacklinks(links []string) {
	bar := a.backlinksBar
	for c := bar.FirstChild(); c != nil; c = bar.FirstChild() {
		bar.Remove(c)
	}
	if len(links) == 0 {
		bar.SetVisible(false)
		return
	}
	label := gtk.NewLabel("Linked from")
	label.AddCSSClass("dim-label")
	label.AddCSSClass("caption")
	bar.Append(label)
	for i, rel := range links {
		if i == backlinksShown {
			more := gtk.NewLabel(fmt.Sprintf("and %d more", len(links)-backlinksShown))
			more.AddCSSClass("dim-label")
			more.AddCSSClass("caption")
			bar.Append(more)
			break
		}
		rel := rel
		btn := gtk.NewButtonWithLabel(path.Base(rel))
		btn.AddCSSClass("flat")
		btn.AddCSSClass("caption")
		btn.SetTooltipText(rel)
		btn.ConnectClicked(func() { a.openNote(rel) })
		bar.Append(btn)
	}
	bar.SetVisible(true)
}

// changeNoteFormat switches the vault to another note format and converts
// every note to it, in the background: a large vault takes a while, and
// saving must carry on meanwhile. ConvertVault takes the write lock one note
// at a time, so it does.
func (a *App) changeNoteFormat(c storage.Compression) {
	if a.store == nil || a.store.Compression() == c {
		return
	}
	a.flushDirty()
	if err := a.store.SetCompression(c); err != nil {
		log.Printf("atlas-notes: set note format: %v", err)
		a.toast("Couldn't change the note format: " + err.Error())
		return
	}
	a.toast("Converting notes to " + formatName(c) + "…")
	go func() {
		n, err := a.store.ConvertVault(context.Background(), nil)
		coreglib.IdleAdd(func() bool {
			if a.closing {
				return false
			}
			switch {
			case err != nil:
				log.Printf("atlas-notes: converting notes: %v", err)
				a.toast(fmt.Sprintf("Converted %s; some could not be: %v", plural(n, "note"), err))
			default:
				a.toast(fmt.Sprintf("%s now stored as %s", plural(n, "note"), formatName(c)))
			}
			a.refreshHeader() // the title's tooltip names the file
			return false
		})
	}()
}
