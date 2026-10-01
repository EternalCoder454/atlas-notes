package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// A folder made while empty, with a note put in it afterwards, opens to show
// that note, and so does a folder inside it. A person could not see their
// notes: the folder had been given no arrow when it was empty, and nothing
// gave it one later. It needs a display.
func TestFoldersOpenToShowNotesAddedLater(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "vault"), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	win := gtk.NewWindow()
	win.SetDefaultSize(300, 600)
	tree := NewTree(store, win, nil)
	win.SetChild(tree.Widget())
	win.Present()
	defer win.Destroy()
	pump := func() {
		ctx := glib.MainContextDefault()
		end := time.Now().Add(400 * time.Millisecond)
		for time.Now().Before(end) {
			for ctx.Pending() {
				ctx.Iteration(false)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// The folder, empty, is on screen first.
	if err := store.CreateFolder("Atlas Commander"); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNote("Welcome", "# Welcome\n"); err != nil {
		t.Fatal(err)
	}
	tree.ForceRefresh()
	pump()

	// Then a note goes into it, and a folder with a note inside that.
	if err := store.WriteNote("Atlas Commander/Plan", "# Plan\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFolder("Atlas Commander/Inner"); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteNote("Atlas Commander/Inner/Deep", "# Deep\n"); err != nil {
		t.Fatal(err)
	}
	tree.ForceRefresh()
	pump()

	open := func(rel string) {
		t.Helper()
		row := rowFor(tree, rel)
		if row == nil {
			t.Fatalf("no row for %q; rows: %v", rel, rowNames(tree))
		}
		if !row.IsExpandable() {
			t.Fatalf("%q has no arrow, so what is in it cannot be shown", rel)
		}
		row.SetExpanded(true)
		pump()
	}
	open("Atlas Commander")
	if rowFor(tree, "Atlas Commander/Plan") == nil {
		t.Fatalf("the note in the folder is not shown; rows: %v", rowNames(tree))
	}
	open("Atlas Commander/Inner")
	if rowFor(tree, "Atlas Commander/Inner/Deep") == nil {
		t.Fatalf("the note in the folder inside it is not shown; rows: %v", rowNames(tree))
	}

	// And one added to a folder that is already open shows up too.
	if err := store.WriteNote("Atlas Commander/Later", "# Later\n"); err != nil {
		t.Fatal(err)
	}
	tree.ForceRefresh()
	pump()
	if rowFor(tree, "Atlas Commander/Later") == nil {
		t.Errorf("a note added to an open folder is not shown; rows: %v", rowNames(tree))
	}
}

// rowFor finds the tree row showing rel, among the rows currently listed.
func rowFor(t *Tree, rel string) *gtk.TreeListRow {
	model := t.selection.Model()
	for i := uint(0); i < model.NItems(); i++ {
		row, ok := model.Item(i).Cast().(*gtk.TreeListRow)
		if !ok {
			continue
		}
		if n := nodeOfRow(row); n != nil && n.rel == rel {
			return row
		}
	}
	return nil
}

func rowNames(t *Tree) []string {
	var out []string
	model := t.selection.Model()
	for i := uint(0); i < model.NItems(); i++ {
		if row, ok := model.Item(i).Cast().(*gtk.TreeListRow); ok {
			if n := nodeOfRow(row); n != nil {
				out = append(out, n.rel)
			}
		}
	}
	return out
}

func nodeOfRow(row *gtk.TreeListRow) *node {
	return gioutil.ObjectValue[*node](row.Item())
}
