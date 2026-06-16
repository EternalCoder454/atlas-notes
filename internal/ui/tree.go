// Package ui holds the folder-tree panel and the AI sidebar — the side-panel GTK
// widgets composed by package app.
package ui

import (
	"context"
	"log"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/storage"
)

// indentStep is the per-depth indentation (px) for child rows — about half the
// stock GtkTreeExpander indent, so nested notes sit closer to the panel edge.
const indentStep = 12

// node is one row in the vault tree: a folder or a note.
type node struct {
	name     string // display name
	rel      string // vault-relative path (folder path, or note path without ext)
	isFolder bool
	created  time.Time
	modified time.Time
}

// Tree is the left-panel vault browser: a GtkListView tree. Items are created,
// renamed, and deleted from a right-click context menu (or double-click to
// rename a note); there is no separate toolbar.
type Tree struct {
	store  *storage.Store
	ai     *ai.Client
	parent gtk.Widgetter // dialog parent

	widget    *gtk.Box
	listView  *gtk.ListView
	selection *gtk.SingleSelection
	rootModel *gioutil.ListModel[*node]

	cachedFolders []string
	cachedNotes   []storage.NoteMeta

	summariesEnabled bool
	summaries        map[string]string
	summaryPending   map[string]bool

	currentRel string // the open note, kept selected/highlighted in the list

	// OnOpenNote is invoked when a note row is activated.
	OnOpenNote func(rel string)
	// OnDeleted is invoked after a note or folder is deleted.
	OnDeleted func(rel string, isFolder bool)
	// OnMoved is invoked after a note is dragged into another folder.
	OnMoved func(oldRel, newRel string)
}

// NewTree builds the vault tree panel.
func NewTree(store *storage.Store, parent gtk.Widgetter, aiClient *ai.Client) *Tree {
	t := &Tree{
		store:          store,
		parent:         parent,
		ai:             aiClient,
		summaries:      map[string]string{},
		summaryPending: map[string]bool{},
	}
	t.rootModel = gioutil.NewListModel[*node]()

	treeModel := gtk.NewTreeListModel(t.rootModel, false, false, t.createChildModel)
	t.selection = gtk.NewSingleSelection(treeModel)
	t.selection.SetAutoselect(false)
	t.selection.SetCanUnselect(true)

	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(t.setupItem)
	factory.ConnectBind(t.bindItem)

	t.listView = gtk.NewListView(t.selection, &factory.ListItemFactory)
	t.listView.SetSingleClickActivate(true)
	t.listView.AddCSSClass("navigation-sidebar")
	t.listView.ConnectActivate(t.onActivate)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(t.listView)
	scroll.SetVExpand(true)

	// Right-click on empty space → root-level New Note / New Folder.
	bg := gtk.NewGestureClick()
	bg.SetButton(3)
	bg.ConnectPressed(func(_ int, x, y float64) {
		t.showContextMenu(scroll, x, y, nil)
	})
	scroll.AddController(bg)

	t.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	t.widget.SetVExpand(true)
	t.widget.Append(scroll)

	t.Refresh()
	return t
}

// Widget returns the root widget of the panel.
func (t *Tree) Widget() gtk.Widgetter { return t.widget }

// Refresh rebuilds the root level of the tree from storage, preserving folder
// expansion state and re-selecting the open note (so a rename or autosave no
// longer collapses folders or loses the highlight).
func (t *Tree) Refresh() {
	expanded := t.snapshotExpanded()
	t.reloadCache()
	if n := t.rootModel.Len(); n > 0 {
		t.rootModel.Splice(0, n)
	}
	for _, c := range t.childrenOf("") {
		t.rootModel.Append(c)
	}
	t.restoreExpanded(expanded)
	if t.currentRel != "" {
		t.revealAndSelect(t.currentRel)
	}
}

// reloadCache snapshots the folder and note lists once, so childrenOf (which
// GTK calls per folder while probing expandability) doesn't re-walk the
// filesystem each time — the main cause of slow post-rename refreshes.
func (t *Tree) reloadCache() {
	t.cachedFolders, _ = t.store.ListFolders()
	t.cachedNotes, _ = t.store.ListNotes()
}

// SetSummariesEnabled toggles AI hover summaries and rebuilds the rows so
// tooltips reflect the new setting.
func (t *Tree) SetSummariesEnabled(enabled bool) {
	t.summariesEnabled = enabled
	t.Refresh()
}

// SetCurrent highlights the open note by selecting its row (revealing it inside
// collapsed folders first). An empty rel clears the selection.
func (t *Tree) SetCurrent(rel string) {
	t.currentRel = rel
	if rel == "" {
		t.selection.SetSelected(gtk.InvalidListPosition)
		return
	}
	t.revealAndSelect(rel)
}

// rowAt returns the TreeListRow at a flat position in the (expanded) list view.
func (t *Tree) rowAt(pos uint) *gtk.TreeListRow {
	obj := t.selection.Item(pos)
	if obj == nil {
		return nil
	}
	row, _ := obj.Cast().(*gtk.TreeListRow)
	return row
}

// findRow scans the visible rows for the node with the given rel and folder-ness.
func (t *Tree) findRow(rel string, folder bool) (uint, bool) {
	n := t.selection.NItems()
	for i := uint(0); i < n; i++ {
		row := t.rowAt(i)
		if row == nil {
			continue
		}
		if nd := gioutil.ObjectValue[*node](row.Item()); nd != nil && nd.isFolder == folder && nd.rel == rel {
			return i, true
		}
	}
	return 0, false
}

// revealAndSelect expands the ancestor folders of rel and selects its note row.
func (t *Tree) revealAndSelect(rel string) {
	for _, anc := range ancestorFolders(rel) {
		if pos, ok := t.findRow(anc, true); ok {
			if row := t.rowAt(pos); row != nil && !row.Expanded() {
				row.SetExpanded(true)
			}
		}
	}
	if pos, ok := t.findRow(rel, false); ok {
		t.selection.SetSelected(pos)
	}
}

// snapshotExpanded records which folders are currently expanded, by rel.
func (t *Tree) snapshotExpanded() map[string]bool {
	out := map[string]bool{}
	n := t.selection.NItems()
	for i := uint(0); i < n; i++ {
		row := t.rowAt(i)
		if row == nil || !row.Expanded() {
			continue
		}
		if nd := gioutil.ObjectValue[*node](row.Item()); nd != nil && nd.isFolder {
			out[nd.rel] = true
		}
	}
	return out
}

// restoreExpanded re-expands the folders in set. It loops because expanding a
// parent reveals child folders that may also need expanding.
func (t *Tree) restoreExpanded(set map[string]bool) {
	if len(set) == 0 {
		return
	}
	for pass := 0; pass <= len(set); pass++ {
		changed := false
		n := t.selection.NItems()
		for i := uint(0); i < n; i++ {
			row := t.rowAt(i)
			if row == nil || row.Expanded() {
				continue
			}
			if nd := gioutil.ObjectValue[*node](row.Item()); nd != nil && nd.isFolder && set[nd.rel] {
				row.SetExpanded(true)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// moveInto moves a dragged note into folderRel and returns whether it succeeded.
func (t *Tree) moveInto(srcRel, folderRel string) bool {
	srcRel = strings.TrimSpace(srcRel)
	if srcRel == "" {
		return false
	}
	newRel := joinRel(folderRel, path.Base(srcRel))
	if newRel == srcRel {
		return false // already in this folder
	}
	if err := t.store.RenameNote(srcRel, newRel); err != nil {
		log.Printf("atlas-notes: move note: %v", err)
		return false
	}
	if t.OnMoved != nil {
		t.OnMoved(srcRel, newRel)
	}
	t.Refresh()
	return true
}

// nodeFromExpander returns the node currently bound to a row's expander.
func nodeFromExpander(expander *gtk.TreeExpander) *node {
	row := expander.ListRow()
	if row == nil {
		return nil
	}
	return gioutil.ObjectValue[*node](row.Item())
}

// tooltipFor builds a row's hover tooltip: full name, created/modified dates,
// and (when enabled) the cached one-sentence AI summary.
func (t *Tree) tooltipFor(n *node) string {
	if n.isFolder {
		return n.name
	}
	var b strings.Builder
	b.WriteString(n.name)
	if !n.created.IsZero() && n.created.Unix() > 0 {
		b.WriteString("\nCreated: " + n.created.Format("Jan 2, 2006 3:04 PM"))
	}
	if !n.modified.IsZero() && n.modified.Unix() > 0 {
		b.WriteString("\nModified: " + n.modified.Format("Jan 2, 2006 3:04 PM"))
	}
	if t.summariesEnabled {
		if s := t.summaries[n.rel]; s != "" {
			b.WriteString("\n\n" + s)
		} else {
			b.WriteString("\n\n(generating summary…)")
		}
	}
	return b.String()
}

// ensureSummary lazily generates and caches a one-sentence AI summary for a note
// (off the main thread, once per note per session).
func (t *Tree) ensureSummary(rel string) {
	if t.ai == nil {
		return
	}
	if _, done := t.summaries[rel]; done {
		return
	}
	if t.summaryPending[rel] {
		return
	}
	t.summaryPending[rel] = true
	go func() {
		content, err := t.store.ReadNote(rel)
		if err != nil {
			coreglib.IdleAdd(func() bool { delete(t.summaryPending, rel); return false })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		summary, _, gerr := t.ai.RunAction(ctx, "Summarize this note in one short sentence:\n\n{content}", content, -1, nil)
		coreglib.IdleAdd(func() bool {
			delete(t.summaryPending, rel)
			if gerr == nil && strings.TrimSpace(summary) != "" {
				t.summaries[rel] = strings.TrimSpace(summary)
			}
			return false
		})
	}()
}

// createChildModel supplies the children of an expandable (folder) row.
func (t *Tree) createChildModel(item *coreglib.Object) *gio.ListModel {
	n := gioutil.ObjectValue[*node](item)
	if n == nil || !n.isFolder {
		return nil
	}
	children := t.childrenOf(n.rel)
	if len(children) == 0 {
		return nil
	}
	m := gioutil.NewListModel[*node]()
	for _, c := range children {
		m.Append(c)
	}
	return m.ListModel
}

// childrenOf returns the immediate folders and notes inside a vault folder.
func (t *Tree) childrenOf(folderRel string) []*node {
	var out []*node
	for _, f := range t.cachedFolders {
		if parentFolder(f) == folderRel {
			out = append(out, &node{name: path.Base(f), rel: f, isFolder: true})
		}
	}
	for _, n := range t.cachedNotes {
		if n.Folder == folderRel {
			out = append(out, &node{name: path.Base(n.Path), rel: n.Path, isFolder: false, created: n.CreatedAt, modified: n.ModifiedAt})
		}
	}
	sortNodes(out)
	return out
}

func (t *Tree) setupItem(obj *coreglib.Object) {
	item, ok := obj.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	expander := gtk.NewTreeExpander()
	expander.SetIndentForDepth(false) // depth indent is applied manually (indentStep) in bindItem
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	icon := gtk.NewImage()
	label := gtk.NewLabel("")
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd) // long names truncate with "…" instead of overflowing
	label.SetHExpand(true)
	box.Append(icon)
	box.Append(label)
	expander.SetChild(box)

	// Drag a note row onto a folder row to move it into that folder.
	drag := gtk.NewDragSource()
	drag.SetActions(gdk.ActionMove)
	drag.ConnectPrepare(func(x, y float64) *gdk.ContentProvider {
		n := nodeFromExpander(expander)
		if n == nil || n.isFolder {
			return nil // only notes are draggable
		}
		return gdk.NewContentProviderForValue(coreglib.NewValue(n.rel))
	})
	expander.AddController(drag)

	drop := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	drop.ConnectDrop(func(value *coreglib.Value, x, y float64) bool {
		n := nodeFromExpander(expander)
		if n == nil || !n.isFolder {
			return false // only folders accept drops
		}
		return t.moveInto(value.String(), n.rel)
	})
	expander.AddController(drop)

	// Right-click a row → contextual menu (create / rename / delete).
	menu := gtk.NewGestureClick()
	menu.SetButton(3)
	menu.ConnectPressed(func(_ int, x, y float64) {
		menu.SetState(gtk.EventSequenceClaimed) // don't also trigger the empty-space menu
		t.showContextMenu(expander, x, y, nodeFromExpander(expander))
	})
	expander.AddController(menu)

	// Double-click a note's text to rename it.
	dbl := gtk.NewGestureClick()
	dbl.SetButton(1)
	dbl.ConnectPressed(func(nPress int, x, y float64) {
		if nPress < 2 {
			return
		}
		if n := nodeFromExpander(expander); n != nil && !n.isFolder {
			t.promptRename(n)
		}
	})
	label.AddController(dbl)

	item.SetChild(expander)
}

func (t *Tree) bindItem(obj *coreglib.Object) {
	item, ok := obj.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	row, ok := item.Item().Cast().(*gtk.TreeListRow)
	if !ok {
		return
	}
	expander, ok := item.Child().(*gtk.TreeExpander)
	if !ok {
		return
	}
	expander.SetListRow(row)

	n := gioutil.ObjectValue[*node](row.Item())
	if n == nil {
		return
	}
	box, ok := expander.Child().(*gtk.Box)
	if !ok {
		return
	}
	box.SetMarginStart(int(row.Depth()) * indentStep) // manual, tighter depth indent
	icon, ok := box.FirstChild().(*gtk.Image)
	if !ok {
		return
	}
	if n.isFolder {
		icon.SetFromIconName("folder-symbolic")
	} else {
		icon.SetFromIconName("text-x-generic-symbolic")
	}
	if label, ok := icon.NextSibling().(*gtk.Label); ok {
		label.SetText(n.name)
	}
	expander.SetTooltipText(t.tooltipFor(n))
	if !n.isFolder && t.summariesEnabled {
		t.ensureSummary(n.rel)
	}
}

// onActivate handles single-click: toggle folders, open notes.
func (t *Tree) onActivate(position uint) {
	obj := t.selection.Item(position)
	if obj == nil {
		return
	}
	row, ok := obj.Cast().(*gtk.TreeListRow)
	if !ok {
		return
	}
	n := gioutil.ObjectValue[*node](row.Item())
	if n == nil {
		return
	}
	if n.isFolder {
		row.SetExpanded(!row.Expanded())
		return
	}
	if t.OnOpenNote != nil {
		t.OnOpenNote(n.rel)
	}
}

// showContextMenu pops up the create/rename/delete menu at (x,y) in parent's
// coordinate space. n is the row under the pointer, or nil for empty space.
func (t *Tree) showContextMenu(parent gtk.Widgetter, x, y float64, n *node) {
	pop := gtk.NewPopover()
	pop.SetAutohide(true)
	pop.SetHasArrow(false)
	rect := gdk.NewRectangle(int(x), int(y), 1, 1)
	pop.SetPointingTo(&rect)

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)

	add := func(label string, destructive bool, fn func()) {
		b := gtk.NewButtonWithLabel(label)
		b.AddCSSClass("flat")
		b.SetHAlign(gtk.AlignFill)
		if l, ok := b.Child().(*gtk.Label); ok {
			l.SetXAlign(0)
		}
		if destructive {
			b.AddCSSClass("destructive-action")
		}
		b.ConnectClicked(func() {
			pop.Popdown()
			fn()
		})
		box.Append(b)
	}

	folder := folderFor(n)
	add("New Note", false, func() { t.promptNewNote(folder) })
	add("New Folder", false, func() { t.promptNewFolder(folder) })
	if n != nil {
		box.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
		add("Rename", false, func() { t.promptRename(n) })
		add("Delete", true, func() { t.promptDelete(n) })
	}

	pop.SetChild(box)
	pop.SetParent(parent)
	pop.ConnectClosed(func() { pop.Unparent() })
	pop.Popup()
}

// folderFor returns the folder a new item should be created in for a context
// target: inside a folder, alongside a note, or at the root for empty space.
func folderFor(n *node) string {
	switch {
	case n == nil:
		return ""
	case n.isFolder:
		return n.rel
	default:
		return parentFolder(n.rel)
	}
}

func (t *Tree) promptNewNote(folder string) {
	t.promptText("New Note", "Create", "", func(name string) {
		rel := joinRel(folder, name)
		if err := t.store.WriteNote(rel, "# "+name+"\n\n"); err != nil {
			log.Printf("atlas-notes: new note: %v", err)
			return
		}
		t.Refresh()
		if t.OnOpenNote != nil {
			t.OnOpenNote(rel)
		}
	})
}

func (t *Tree) promptNewFolder(folder string) {
	t.promptText("New Folder", "Create", "", func(name string) {
		if err := t.store.CreateFolder(joinRel(folder, name)); err != nil {
			log.Printf("atlas-notes: new folder: %v", err)
			return
		}
		t.Refresh()
	})
}

func (t *Tree) promptRename(n *node) {
	if n == nil {
		return
	}
	t.promptText("Rename", "Rename", n.name, func(newName string) {
		newRel := joinRel(parentFolder(n.rel), newName)
		var err error
		if n.isFolder {
			err = t.store.RenameFolder(n.rel, newRel)
		} else {
			err = t.store.RenameNote(n.rel, newRel)
		}
		if err != nil {
			log.Printf("atlas-notes: rename: %v", err)
			return
		}
		if !n.isFolder && t.currentRel == n.rel {
			if t.OnMoved != nil {
				t.OnMoved(n.rel, newRel) // keep the open note in sync
			}
		}
		t.Refresh()
	})
}

func (t *Tree) promptDelete(n *node) {
	if n == nil {
		return
	}
	what := "note"
	if n.isFolder {
		what = "folder and all its contents"
	}
	t.confirm("Delete?", "Delete the "+what+" \""+n.name+"\"? This cannot be undone.", "Delete", func() {
		var err error
		if n.isFolder {
			err = t.store.DeleteFolder(n.rel)
		} else {
			err = t.store.DeleteNote(n.rel)
		}
		if err != nil {
			log.Printf("atlas-notes: delete: %v", err)
		} else if t.OnDeleted != nil {
			t.OnDeleted(n.rel, n.isFolder)
		}
		t.Refresh()
	})
}

// promptText shows a single-entry dialog and calls onOK with the trimmed value.
func (t *Tree) promptText(title, okLabel, initial string, onOK func(string)) {
	dialog := adw.NewAlertDialog(title, "")
	entry := gtk.NewEntry()
	if initial != "" {
		entry.Buffer().SetText(initial, -1)
	}
	entry.SetHExpand(true)
	dialog.SetExtraChild(entry)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", okLabel)
	dialog.SetResponseAppearance("ok", adw.ResponseSuggested)
	dialog.SetDefaultResponse("ok")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response != "ok" {
			return
		}
		if name := strings.TrimSpace(entry.Buffer().Text()); name != "" {
			onOK(name)
		}
	})
	dialog.Present(t.parent)
}

// confirm shows a destructive confirmation dialog.
func (t *Tree) confirm(title, body, okLabel string, onOK func()) {
	dialog := adw.NewAlertDialog(title, body)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", okLabel)
	dialog.SetResponseAppearance("ok", adw.ResponseDestructive)
	dialog.SetDefaultResponse("cancel")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response == "ok" {
			onOK()
		}
	})
	dialog.Present(t.parent)
}

func parentFolder(rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// ancestorFolders returns rel's ancestor folder rels, top-down (root first).
func ancestorFolders(rel string) []string {
	var stack []string
	for d := path.Dir(rel); d != "." && d != "/" && d != ""; d = path.Dir(d) {
		stack = append(stack, d)
	}
	out := make([]string, 0, len(stack))
	for i := len(stack) - 1; i >= 0; i-- {
		out = append(out, stack[i])
	}
	return out
}

func joinRel(folder, name string) string {
	name = strings.TrimSpace(name)
	if folder == "" {
		return name
	}
	return folder + "/" + name
}

func sortNodes(ns []*node) {
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].isFolder != ns[j].isFolder {
			return ns[i].isFolder // folders before notes
		}
		return strings.ToLower(ns[i].name) < strings.ToLower(ns[j].name)
	})
}
