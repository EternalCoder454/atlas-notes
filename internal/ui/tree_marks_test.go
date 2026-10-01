package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

func TestMarkRange(t *testing.T) {
	order := []string{"f:A", "n:A/x", "n:A/y", "n:b", "n:c"}
	cases := []struct {
		name     string
		from, to string
		want     []string
	}{
		{"down", "n:A/x", "n:b", []string{"n:A/x", "n:A/y", "n:b"}},
		{"up", "n:c", "n:A/y", []string{"n:A/y", "n:b", "n:c"}},
		{"same", "n:b", "n:b", []string{"n:b"}},
		{"start hidden", "n:gone", "n:b", []string{"n:b"}},
		{"end hidden", "n:b", "n:gone", nil},
	}
	for _, c := range cases {
		if got := markRange(order, c.from, c.to); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCoverMarks(t *testing.T) {
	in := []string{"n:z", "f:A", "n:A/x", "f:A/B", "n:A/B/y", "n:AB", "f:C", "n:C"}
	want := []string{"n:z", "f:A", "n:AB", "f:C", "n:C"}
	if got := coverMarks(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := coverMarks(nil); len(got) != 0 {
		t.Errorf("nothing marked gave %v", got)
	}
}

func TestKeepMarks(t *testing.T) {
	marks := map[string]bool{"n:a": true, "n:b": true, "f:c": true}
	// b is hidden by a search but still in the vault; c is gone.
	dropped := keepMarks(marks, func(m string) bool { return m != "f:c" })
	if dropped != 1 || len(marks) != 2 || !marks["n:a"] || !marks["n:b"] {
		t.Errorf("dropped %d, left %v", dropped, marks)
	}
}

func TestDeleteSummary(t *testing.T) {
	cases := []struct {
		notes, folders, kept int
		perm                 bool
		want                 string
	}{
		{3, 0, 0, false, "Moved 3 notes to the Trash."},
		{1, 1, 0, false, "Moved 1 note and 1 folder to the Trash."},
		{0, 2, 1, false, "Moved 2 folders to the Trash. 1 item was kept."},
		{3, 0, 2, true, "Deleted 3 notes. 2 items were kept."},
		{0, 0, 1, false, "Nothing was deleted. 1 item was kept."},
	}
	for _, c := range cases {
		if got := deleteSummary(c.notes, c.folders, c.kept, c.perm); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// Marking notes and a folder and deleting them sends them to the Trash and
// takes their rows away; a note inside a marked folder is not trashed on its
// own. It needs a display.
func TestMarkAndDelete(t *testing.T) {
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
	var trashed []string
	store.Trash = func(p string) error {
		trashed = append(trashed, filepath.Base(p))
		return os.RemoveAll(p)
	}
	for rel, body := range map[string]string{
		"One": "# One\n", "Two": "# Two\n", "Keep": "# Keep\n",
		"Dir/Inside": "# Inside\n",
	} {
		if err := store.WriteNote(rel, body); err != nil {
			t.Fatal(err)
		}
	}

	win := gtk.NewWindow()
	win.SetDefaultSize(300, 600)
	tree := NewTree(store, win, nil)
	win.SetChild(tree.Widget())
	win.Present()
	defer win.Destroy()
	var deleted []string
	tree.OnDeleted = func(rel string, _ bool) { deleted = append(deleted, rel) }
	pump := func() {
		ctx := glib.MainContextDefault()
		end := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(end) {
			for ctx.Pending() {
				ctx.Iteration(false)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	tree.ForceRefresh()
	pump()
	rowFor(tree, "Dir").SetExpanded(true)
	pump()

	tree.toggleMark(noteMark("One"))
	tree.toggleMark(noteMark("Two"))
	tree.toggleMark(folderMark("Dir"))
	tree.toggleMark(noteMark("Dir/Inside"))
	if !tree.markBar.RevealChild() || tree.markLabel.Text() != "4 Selected" {
		t.Fatalf("bar: revealed %v, %q", tree.markBar.RevealChild(), tree.markLabel.Text())
	}
	got := tree.marked()
	if want := []string{"f:Dir", "n:One", "n:Two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("marked() = %v, want %v", got, want)
	}
	// What the confirmation would run on.
	tree.deleteMany(got, false)
	pump()

	if want := []string{"Dir", "One.md", "Two.md"}; len(trashed) != 3 {
		t.Errorf("trashed %v, want %v", trashed, want)
	}
	if !reflect.DeepEqual(deleted, []string{"Dir", "One", "Two"}) {
		t.Errorf("OnDeleted saw %v", deleted)
	}
	if names := rowNames(tree); !reflect.DeepEqual(names, []string{"Keep"}) {
		t.Errorf("rows left: %v", names)
	}
	if len(tree.marks) != 0 || tree.markBar.RevealChild() {
		t.Errorf("marks left: %v, bar revealed %v", tree.marks, tree.markBar.RevealChild())
	}
}
