package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/diag"
	"atlas-notes/internal/storage"
)

// Picking out several notes and folders at once, to delete them together.
//
// Ctrl and a click marks a row or unmarks it, Shift and a click marks the run
// of rows from the last one marked, and while anything is marked a plain click
// marks too, the way a phone's selection works. A touchscreen gets there with a
// long press or Select on a row's menu, the keyboard with Ctrl+Space or
// Shift and the arrow keys. A bar under the list says how many are marked and
// deletes them; Escape, or the bar's close button, lets them go.
//
// A mark is a string, "n:" and a note's path or "f:" and a folder's, so it
// survives the rows being rebuilt and a search hiding the row it was made on.

const markCheckIcon = "atlasnotes-task-symbolic"

func noteMark(rel string) string   { return "n:" + rel }
func folderMark(rel string) string { return "f:" + rel }

func markOf(n *node) string {
	if n.isFolder {
		return folderMark(n.rel)
	}
	return noteMark(n.rel)
}

// markPath splits a mark into the path it names and whether that is a folder.
func markPath(mark string) (rel string, folder bool) {
	return mark[2:], mark[0] == 'f'
}

// markRange is the marks from one row to another, ends included, in the order
// the rows are listed. A start that is not listed, such as one a search has
// hidden, leaves just the end.
func markRange(order []string, from, to string) []string {
	a, b := indexOfMark(order, from), indexOfMark(order, to)
	if b < 0 {
		return nil
	}
	if a < 0 {
		a = b
	}
	if a > b {
		a, b = b, a
	}
	return append([]string(nil), order[a:b+1]...)
}

func indexOfMark(order []string, mark string) int {
	for i, each := range order {
		if each == mark {
			return i
		}
	}
	return -1
}

// coverMarks drops the marks that a marked folder already covers: deleting the
// folder takes what is in it, and deleting the notes first would only make the
// folder's trip to the Trash a smaller one. The order of the rest is kept.
func coverMarks(marks []string) []string {
	var folders []string
	for _, m := range marks {
		if rel, folder := markPath(m); folder {
			folders = append(folders, rel)
		}
	}
	out := make([]string, 0, len(marks))
next:
	for _, m := range marks {
		rel, _ := markPath(m)
		for _, f := range folders {
			if strings.HasPrefix(rel, f+"/") {
				continue next
			}
		}
		out = append(out, m)
	}
	return out
}

// keepMarks is the marks that are still in the vault. A mark whose row a search
// or a closed folder is hiding is kept: only the vault can say something has
// gone.
func keepMarks(marks map[string]bool, exists func(mark string) bool) (dropped int) {
	for m := range marks {
		if !exists(m) {
			delete(marks, m)
			dropped++
		}
	}
	return dropped
}

// plural is "1 note" or "3 notes".
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// countWords is "2 notes and 1 folder", whichever of them there are.
func countWords(notes, folders int) string {
	switch {
	case folders == 0:
		return plural(notes, "note", "notes")
	case notes == 0:
		return plural(folders, "folder", "folders")
	}
	return plural(notes, "note", "notes") + " and " + plural(folders, "folder", "folders")
}

// deleteSummary is what is said once a batch delete is done. kept is how many
// could not be deleted.
func deleteSummary(notes, folders, kept int, permanently bool) string {
	var s string
	switch {
	case notes+folders == 0:
		s = "Nothing was deleted."
	case permanently:
		s = "Deleted " + countWords(notes, folders) + "."
	default:
		s = "Moved " + countWords(notes, folders) + " to the Trash."
	}
	if kept > 0 {
		s += " " + plural(kept, "item was", "items were") + " kept."
	}
	return s
}

// marksNames is the opening of the list shown in the confirmation: the first
// few names, then how many more there are.
func marksNames(marks []string, limit int) string {
	var b strings.Builder
	for i, m := range marks {
		if i == limit {
			fmt.Fprintf(&b, "and %d more", len(marks)-limit)
			break
		}
		rel, folder := markPath(m)
		b.WriteString("• “" + rel + "”")
		if folder {
			b.WriteString(" (folder)")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// rowNode is the note or folder a tree row shows.
func rowNode(row *gtk.TreeListRow) *node { return gioutil.ObjectValue[*node](row.Item()) }

// visibleMarks is the marks of the rows on screen, top to bottom, folders that
// are open included along with what is in them. It is aligned to the list's
// positions: a row with no node leaves an empty string, so an index here is
// the row's position.
func (t *Tree) visibleMarks() []string {
	n := t.selection.NItems()
	out := make([]string, 0, n)
	for i := uint(0); i < n; i++ {
		mark := ""
		if row := t.rowAt(i); row != nil {
			if nd := rowNode(row); nd != nil {
				mark = markOf(nd)
			}
		}
		out = append(out, mark)
	}
	return out
}

// marked is the marks to delete: those a marked folder covers left out, in
// order of path so the same marks always read the same.
func (t *Tree) marked() []string {
	list, _, _ := t.markedInfo()
	return list
}

// markedInfo is marked, and how many marks a marked folder covers, and how
// many marks are on rows that are not in the list at the moment, hidden by a
// search or a closed folder.
func (t *Tree) markedInfo() (list []string, covered, hidden int) {
	if len(t.marks) == 0 {
		return nil, 0, 0
	}
	shown := make(map[string]bool)
	for _, m := range t.visibleMarks() {
		shown[m] = true
	}
	all := make([]string, 0, len(t.marks))
	for m := range t.marks {
		all = append(all, m)
		if !shown[m] {
			hidden++
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, _ := markPath(all[i])
		b, _ := markPath(all[j])
		if a != b {
			return a < b
		}
		return all[i] < all[j]
	})
	list = coverMarks(all)
	return list, len(all) - len(list), hidden
}

// markExists says whether the vault still has what a mark names.
func (t *Tree) markExists() func(string) bool {
	folders := make(map[string]bool, len(t.cachedFolders))
	for _, f := range t.cachedFolders {
		folders[f] = true
	}
	notes := make(map[string]bool, len(t.entries))
	for i := range t.entries {
		notes[t.entries[i].meta.Path] = true
	}
	return func(m string) bool {
		rel, folder := markPath(m)
		if folder {
			return folders[rel]
		}
		return notes[rel]
	}
}

// watchMarks puts on the list the gestures and keys that mark. The click is
// seen before the row sees it, so a click that marks does not also open the
// note.
func (t *Tree) watchMarks() {
	// The click is acted on when it is released, not when it is pressed:
	// claiming a press would take it from a row that is being dragged, and from
	// a finger that is scrolling. A drag or a scroll cancels the gesture, so
	// no release arrives for it.
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.SetPropagationPhase(gtk.PhaseCapture)
	var pending *gtk.TreeExpander
	var pendingArrow bool
	click.ConnectPressed(func(_ int, x, y float64) {
		pending, pendingArrow = t.expanderAt(x, y)
	})
	click.ConnectReleased(func(_ int, _, _ float64) {
		exp, onArrow := pending, pendingArrow
		pending = nil
		if exp == nil {
			diag.Event("tree.release", "row", false)
			return
		}
		n := nodeFromExpander(exp)
		if n == nil {
			diag.Event("tree.release", "row", true, "node", false)
			return
		}
		mods := click.CurrentEventState()
		diag.Event("tree.release", "rel", n.rel, "arrow", onArrow, "marks", len(t.marks),
			"shift", mods&gdk.ShiftMask != 0, "ctrl", mods&gdk.ControlMask != 0)
		switch {
		case mods&gdk.ShiftMask != 0:
			t.markRun(markOf(n))
		case mods&gdk.ControlMask != 0:
			t.toggleMark(markOf(n))
		case len(t.marks) > 0 && !onArrow:
			t.toggleMark(markOf(n))
		case onArrow && n.isFolder:
			// The arrow's own click would flip the folder at once; this one
			// lets it open and close the way a click on its name does.
			if row := exp.ListRow(); row != nil {
				t.toggleFolder(row)
			}
			click.SetState(gtk.EventSequenceClaimed)
			return
		default:
			return // an ordinary click, which opens the note or the folder
		}
		t.cursorTo(exp)
		click.SetState(gtk.EventSequenceClaimed)
	})
	click.ConnectCancel(func(_ *gdk.EventSequence) {
		if pending != nil {
			diag.Event("tree.click_cancelled")
		}
		pending = nil
	})
	t.listView.AddController(click)

	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		ctrl := state&gdk.ControlMask != 0
		shift := state&gdk.ShiftMask != 0
		switch keyval {
		case gdk.KEY_space:
			// Ctrl+Space starts marking from the keyboard; once something is
			// marked a plain Space adds to it.
			if !ctrl && len(t.marks) == 0 {
				return false
			}
			return t.toggleCursor()
		case gdk.KEY_Up, gdk.KEY_KP_Up:
			return shift && !ctrl && t.extend(-1)
		case gdk.KEY_Down, gdk.KEY_KP_Down:
			return shift && !ctrl && t.extend(1)
		}
		return t.markKey(keyval, ctrl)
	})
	t.listView.AddController(keys)
}

// markKey is the keys that work while rows are marked: Escape lets them go,
// Delete deletes them, Ctrl+A marks every row. The bar takes them as well as
// the list, so they work wherever in the panel the focus is.
func (t *Tree) markKey(keyval uint, ctrl bool) bool {
	if len(t.marks) == 0 {
		return false
	}
	switch keyval {
	case gdk.KEY_Escape:
		t.ClearMarks()
	case gdk.KEY_Delete, gdk.KEY_KP_Delete:
		t.deleteMarked()
	case gdk.KEY_a, gdk.KEY_A:
		if !ctrl {
			return false
		}
		t.markAll()
	default:
		return false
	}
	return true
}

// expanderAt finds the row under a point on the list, and whether the point is
// on its disclosure arrow, which keeps opening and closing the folder.
func (t *Tree) expanderAt(x, y float64) (exp *gtk.TreeExpander, onArrow bool) {
	picked := t.listView.Pick(x, y, gtk.PickDefault)
	if picked == nil {
		return nil, false
	}
	for w := gtk.BaseWidget(picked); w != nil; {
		if w.CSSName() == "expander" {
			onArrow = true
		}
		if e, ok := w.Cast().(*gtk.TreeExpander); ok {
			return e, onArrow
		}
		parent := w.Parent()
		if parent == nil {
			break
		}
		w = gtk.BaseWidget(parent)
	}
	return nil, false
}

// cursorTo makes a row the one the keyboard is on, so that Space and Shift and
// the arrows carry on from the row that was just marked.
func (t *Tree) cursorTo(exp *gtk.TreeExpander) {
	if row := exp.ListRow(); row != nil {
		t.selection.SetSelected(row.Position())
	}
	t.listView.GrabFocus()
}

// cursor is the position of the row the keyboard is on.
func (t *Tree) cursor() (uint, *node) {
	pos := t.selection.Selected()
	if pos == gtk.InvalidListPosition {
		return pos, nil
	}
	if row := t.rowAt(pos); row != nil {
		return pos, rowNode(row)
	}
	return pos, nil
}

// toggleCursor marks the row the keyboard is on, or unmarks it.
func (t *Tree) toggleCursor() bool {
	if _, n := t.cursor(); n != nil {
		t.toggleMark(markOf(n))
		return true
	}
	return false
}

// extend is Shift and an arrow key: the marks grow to the row above or below,
// or, when that is back toward where the run began, shrink by the row left.
func (t *Tree) extend(by int) bool {
	pos, n := t.cursor()
	if n == nil {
		return false
	}
	order := t.visibleMarks()
	to := int(pos) + by
	if to < 0 || to >= len(order) {
		return true // at an end of the list; the key is still ours
	}
	here := markOf(n)
	if t.anchor == "" || indexOfMark(order, t.anchor) < 0 {
		t.anchor = here
		t.marks[here] = true
	}
	a := indexOfMark(order, t.anchor)
	if abs(to-a) < abs(int(pos)-a) {
		delete(t.marks, here)
	} else {
		if order[to] != "" {
			t.marks[order[to]] = true
		}
	}
	t.showMarks()
	t.listView.ScrollTo(uint(to), gtk.ListScrollFocus|gtk.ListScrollSelect, nil)
	return true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Mark picks out one row, as Select on its menu does.
func (t *Tree) Mark(n *node) {
	t.marks[markOf(n)] = true
	t.anchor = markOf(n)
	t.showMarks()
	if pos, ok := t.findRow(n.rel, n.isFolder); ok {
		t.selection.SetSelected(pos)
	}
	t.listView.GrabFocus()
}

func (t *Tree) toggleMark(m string) {
	if t.marks[m] {
		delete(t.marks, m)
	} else {
		t.marks[m] = true
	}
	t.anchor = m
	t.showMarks()
}

// markRun marks every row from the last one marked to this one. With nothing
// marked yet it runs from the open note, as a file manager runs from the file
// that is selected.
func (t *Tree) markRun(m string) {
	order := t.visibleMarks()
	from := t.anchor
	if indexOfMark(order, from) < 0 {
		from = noteMark(t.currentRel)
	}
	for _, each := range markRange(order, from, m) {
		t.marks[each] = true
	}
	if t.anchor == "" {
		t.anchor = m
	}
	t.showMarks()
}

// markAll marks every row on screen.
func (t *Tree) markAll() {
	for _, m := range t.visibleMarks() {
		if m != "" {
			t.marks[m] = true
		}
	}
	t.showMarks()
}

// ClearMarks unmarks everything.
func (t *Tree) ClearMarks() {
	if len(t.marks) == 0 {
		return
	}
	clear(t.marks)
	t.anchor = ""
	t.showMarks()
}

// dropMarks is run after the list is drawn again: a marked row whose note or
// folder has gone is forgotten, and the rest stay marked.
func (t *Tree) dropMarks() {
	if len(t.marks) == 0 {
		return
	}
	keepMarks(t.marks, t.markExists())
	t.showMarks()
}

// showMarks brings the rows and the bar into line with what is marked.
func (t *Tree) showMarks() {
	// While marking, every row is checkable: one that is not marked says so,
	// rather than saying nothing. Rows are all redrawn when that changes.
	marking := len(t.marks) > 0
	restate := marking != t.stated
	t.stated = marking
	for c := t.listView.FirstChild(); c != nil; {
		w := gtk.BaseWidget(c)
		c = w.NextSibling()
		exp, ok := w.FirstChild().(*gtk.TreeExpander)
		if !ok {
			continue
		}
		if n := nodeFromExpander(exp); n != nil {
			t.paintMark(exp, n, restate)
		}
	}
	n := len(t.marks)
	if n > 0 {
		t.markLabel.SetText(fmt.Sprintf("%d Selected", n))
	}
	// The count is the one thing on the bar that changes under the reader, so
	// it is said out loud, and so is the selection ending.
	if n != t.announced {
		if n > 0 {
			t.widget.Announce(fmt.Sprintf("%d Selected", n), gtk.AccessibleAnnouncementPriorityLow)
		} else {
			t.widget.Announce("Selection cleared", gtk.AccessibleAnnouncementPriorityLow)
		}
		t.announced = n
	}
	t.markBar.SetRevealChild(n > 0)
	if n > 0 {
		t.widget.AddCSSClass("marking")
	} else {
		t.widget.RemoveCSSClass("marking")
	}
}

// paintMark shows whether a row is marked: the accent fill, a check where the
// icon was, and the state for a screen reader. force draws it even when the
// row already looks as it should, for a row that has just been given a new
// note.
func (t *Tree) paintMark(exp *gtk.TreeExpander, n *node, force bool) {
	on := t.marks[markOf(n)]
	row := rowOf(exp)
	was := row != nil && row.HasCSSClass("marked")
	if was == on && !force {
		return
	}
	if row != nil {
		if on {
			row.AddCSSClass("marked")
		} else {
			row.RemoveCSSClass("marked")
		}
	}
	if box, ok := exp.Child().(*gtk.Box); ok {
		if icon, ok := box.FirstChild().(*gtk.Image); ok {
			if on {
				icon.SetFromIconName(markCheckIcon)
				icon.AddCSSClass("row-check")
			} else {
				icon.SetFromIconName(rowIcon(n))
				icon.RemoveCSSClass("row-check")
			}
		}
	}
	switch {
	case len(t.marks) == 0:
		exp.ResetState(gtk.AccessibleStateChecked)
		exp.ResetProperty(gtk.AccessiblePropertyDescription)
	default:
		state := gtk.AccessibleTristateFalse
		desc := ""
		if on {
			state, desc = gtk.AccessibleTristateTrue, "Selected"
		}
		exp.UpdateState([]gtk.AccessibleState{gtk.AccessibleStateChecked},
			[]coreglib.Value{*coreglib.NewValue(state)})
		exp.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyDescription},
			[]coreglib.Value{*coreglib.NewValue(desc)})
	}
}

// rowIcon is the icon a row has when it is not marked.
func rowIcon(n *node) string {
	switch {
	case n.isFolder:
		return "atlasnotes-folder-symbolic"
	case n.tasks:
		return "atlasnotes-checklist-symbolic"
	}
	return "atlasnotes-note-symbolic"
}

// buildMarkBar is the bar that appears under the list while rows are marked.
func (t *Tree) buildMarkBar() *gtk.Revealer {
	bar := gtk.NewBox(gtk.OrientationVertical, 6)
	bar.AddCSSClass("mark-bar")

	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	t.markLabel = gtk.NewLabel("")
	t.markLabel.SetXAlign(0)
	t.markLabel.SetHExpand(true)
	t.markLabel.AddCSSClass("mark-count")
	top.Append(t.markLabel)
	done := gtk.NewButtonFromIconName("atlasnotes-close-symbolic")
	done.AddCSSClass("flat")
	done.AddCSSClass("mark-close")
	done.SetTooltipText("Stop selecting (Escape)")
	done.ConnectClicked(t.ClearMarks)
	top.Append(done)
	bar.Append(top)

	// Wrapping, not a row of equal buttons: a hidden revealer still asks for
	// its child's width, and the panel could not be dragged narrower than the
	// bar.
	buttons := adw.NewWrapBox()
	buttons.SetChildSpacing(6)
	buttons.SetLineSpacing(6)
	buttons.SetJustify(adw.JustifyFill)
	all := gtk.NewButtonWithLabel("Select All")
	all.SetTooltipText("Select every row in the list (Ctrl+A)")
	all.ConnectClicked(t.markAll)
	buttons.Append(all)
	del := gtk.NewButtonWithLabel("Delete")
	del.AddCSSClass("destructive-action")
	del.SetTooltipText("Delete the selected notes and folders (Delete)")
	del.ConnectClicked(t.deleteMarked)
	buttons.Append(del)
	bar.Append(buttons)

	t.markBar = gtk.NewRevealer()
	t.markBar.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	t.markBar.SetTransitionDuration(140)
	t.markBar.SetChild(bar)
	t.markBar.SetRevealChild(false)
	barKeys := gtk.NewEventControllerKey()
	barKeys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		return t.markKey(keyval, state&gdk.ControlMask != 0)
	})
	t.markBar.AddController(barKeys)
	return t.markBar
}

// deleteMarked asks, once, about everything that is marked. There is no way to
// bring a note back from the system Trash from here, so unlike a chat's
// delete it asks first rather than offering an Undo.
func (t *Tree) deleteMarked() {
	marks, covered, hidden := t.markedInfo()
	if len(marks) == 0 {
		return
	}
	notes, folders := 0, 0
	for _, m := range marks {
		if _, folder := markPath(m); folder {
			folders++
		} else {
			notes++
		}
	}
	body := marksNames(marks, 5)
	if covered > 0 {
		body += "\n\n" + plural(covered, "more selected item is", "more selected items are") +
			" inside the marked folders."
	}
	if hidden > 0 {
		body += "\n\n" + plural(hidden, "selected item is", "selected items are") + " not shown in the list."
	}
	what := countWords(notes, folders)
	if t.store.Trash != nil {
		body += "\n\n"
		if folders > 0 {
			body += "Everything in a folder goes with it. "
		}
		body += "You can restore them from the Trash."
		t.confirm("Move "+what+" to the Trash?", body, "Move to Trash",
			func() { t.deleteMany(marks, false) })
		return
	}
	t.confirm("Delete "+what+"?", body+"\n\nThis cannot be undone.", "Delete",
		func() { t.deleteMany(marks, true) })
}

// deleteMany deletes marked notes and folders, to the Trash unless permanently
// is set, and says what became of them. One that cannot be deleted stays
// marked and does not stop the others.
func (t *Tree) deleteMany(marks []string, permanently bool) {
	if t.OnBeforeDelete != nil {
		t.OnBeforeDelete()
	}
	var noTrash []string // could not be moved to the Trash
	var failed []string
	notes, folders := 0, 0
	var firstErr, trashErr error
	for _, m := range marks {
		rel, folder := markPath(m)
		err := t.deleteOne(rel, folder, permanently)
		switch {
		case err == nil:
			if folder {
				folders++
			} else {
				notes++
			}
			if t.OnDeleted != nil {
				t.OnDeleted(rel, folder)
			}
		case !permanently && isTrashFailure(err):
			noTrash = append(noTrash, m)
			if trashErr == nil {
				trashErr = err
			}
		default:
			failed = append(failed, m)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	t.ForceRefresh()
	// The list keeps its place when rows go, which would leave the highlight on
	// whatever note slid up into it.
	t.selection.SetSelected(gtk.InvalidListPosition)
	if t.currentRel != "" {
		t.revealAndSelect(t.currentRel)
	}
	t.notifyChanged()
	// Those that could not go to the Trash are asked about below, so they are
	// not yet ones that were kept.
	if notes+folders+len(failed) > 0 {
		msg := deleteSummary(notes, folders, len(failed), permanently)
		if firstErr != nil {
			msg += " " + firstErr.Error()
		}
		t.message(msg)
	}
	if len(noTrash) > 0 {
		// Some places have no Trash, and deleting for good is a different thing
		// to agree to, so ask about those on their own.
		body := marksNames(noTrash, 5) + "\n\nThey couldn't be moved to the Trash (" + trashErr.Error() + "), so they would be deleted for good. "
		for _, m := range noTrash {
			if _, folder := markPath(m); folder {
				body += "Everything in a folder goes with it. "
				break
			}
		}
		t.confirm("Delete permanently?", body+"This cannot be undone.",
			"Delete Permanently", func() { t.deleteMany(noTrash, true) })
	}
}

func isTrashFailure(err error) bool { return errors.Is(err, storage.ErrTrashFailed) }

// deleteOne deletes one note or folder.
func (t *Tree) deleteOne(rel string, folder, permanently bool) error {
	switch {
	case folder && permanently:
		return t.store.DeleteFolderPermanently(rel)
	case folder:
		return t.store.DeleteFolder(rel)
	case permanently:
		return t.store.DeleteNotePermanently(rel)
	}
	return t.store.DeleteNote(rel)
}
