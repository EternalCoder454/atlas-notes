// Package ui holds the folder tree panel, the AI sidebar, and the editor
// toolbar — the GTK widgets composed by package app.
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

// node is one row in the vault tree: a folder or a note.
type node struct {
	name     string // display name
	rel      string // vault-relative path (folder path, or note path without ext)
	isFolder bool
	created  time.Time
	modified time.Time
}

// Tree is the left-panel vault browser: a GtkListView tree plus a button bar.
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

	compact     bool
	buttonBar   *gtk.Box
	currentBox  *gtk.Box
	currentLbl  *gtk.Label
	currentName string

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

	t.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	t.widget.SetVExpand(true)
	t.widget.Append(t.buildCurrentIndicator())
	t.widget.Append(scroll)
	t.buttonBar = t.buildButtonBar()
	t.widget.Append(t.buttonBar)

	t.Refresh()
	return t
}

// Widget returns the root widget of the panel.
func (t *Tree) Widget() gtk.Widgetter { return t.widget }

// Refresh rebuilds the root level of the tree from storage. Expansion state of
// folders is reset (acceptable for now).
func (t *Tree) Refresh() {
	t.reloadCache()
	if n := t.rootModel.Len(); n > 0 {
		t.rootModel.Splice(0, n)
	}
	for _, c := range t.childrenOf("") {
		t.rootModel.Append(c)
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

// SetCompact switches the panel between full rows and an icon-only rail (labels,
// the current-file indicator, and the button bar hidden). Names remain on hover.
func (t *Tree) SetCompact(enabled bool) {
	if t.compact == enabled {
		return
	}
	t.compact = enabled
	if t.buttonBar != nil {
		t.buttonBar.SetVisible(!enabled)
	}
	if t.currentBox != nil {
		t.currentBox.SetVisible(!enabled && t.currentName != "")
	}
	t.Refresh()
}

// SetCurrent updates the "current file" indicator at the top of the sidebar.
func (t *Tree) SetCurrent(name string) {
	t.currentName = name
	if t.currentLbl == nil {
		return
	}
	if name == "" {
		t.currentBox.SetVisible(false)
		return
	}
	t.currentLbl.SetText(name)
	t.currentBox.SetVisible(!t.compact)
}

func (t *Tree) buildCurrentIndicator() *gtk.Box {
	t.currentBox = gtk.NewBox(gtk.OrientationHorizontal, 6)
	t.currentBox.AddCSSClass("current-note-indicator")
	t.currentBox.SetMarginTop(8)
	t.currentBox.SetMarginBottom(4)
	t.currentBox.SetMarginStart(8)
	t.currentBox.SetMarginEnd(8)
	t.currentBox.SetVisible(false)

	icon := gtk.NewImageFromIconName("text-x-generic-symbolic")
	t.currentLbl = gtk.NewLabel("")
	t.currentLbl.SetXAlign(0)
	t.currentLbl.SetEllipsize(pango.EllipsizeEnd)
	t.currentLbl.AddCSSClass("heading")
	t.currentBox.Append(icon)
	t.currentBox.Append(t.currentLbl)
	return t.currentBox
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
		summary, gerr := t.ai.RunAction(ctx, "Summarize this note in one short sentence:\n\n{content}", content)
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
			name := n.Title
			if name == "" {
				name = path.Base(n.Path)
			}
			out = append(out, &node{name: name, rel: n.Path, isFolder: false, created: n.CreatedAt, modified: n.ModifiedAt})
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
		label.SetVisible(!t.compact) // icon-only when the panel is collapsed to a rail
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

// buildButtonBar lays the actions out in a FlowBox so they wrap (instead of
// clipping) as the panel narrows toward the rail.
func (t *Tree) buildButtonBar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationVertical, 0)
	bar.AddCSSClass("tree-toolbar")
	bar.SetMarginTop(6)
	bar.SetMarginBottom(6)
	bar.SetMarginStart(6)
	bar.SetMarginEnd(6)

	flow := gtk.NewFlowBox()
	flow.SetSelectionMode(gtk.SelectionNone)
	flow.SetMaxChildrenPerLine(4)
	flow.SetMinChildrenPerLine(1)
	flow.SetColumnSpacing(2)
	flow.SetRowSpacing(2)
	flow.Append(iconButton("document-new-symbolic", "New Note", t.promptNewNote))
	flow.Append(iconButton("folder-new-symbolic", "New Folder", t.promptNewFolder))
	flow.Append(iconButton("document-edit-symbolic", "Rename", t.promptRename))
	flow.Append(iconButton("user-trash-symbolic", "Delete", t.promptDelete))
	bar.Append(flow)
	return bar
}

func (t *Tree) promptNewNote() {
	folder := t.targetFolder()
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

func (t *Tree) promptNewFolder() {
	folder := t.targetFolder()
	t.promptText("New Folder", "Create", "", func(name string) {
		if err := t.store.CreateFolder(joinRel(folder, name)); err != nil {
			log.Printf("atlas-notes: new folder: %v", err)
			return
		}
		t.Refresh()
	})
}

func (t *Tree) promptRename() {
	n := t.selectedNode()
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
		t.Refresh()
	})
}

func (t *Tree) promptDelete() {
	n := t.selectedNode()
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

// targetFolder returns the folder new items should be created in, based on the
// current selection (the selected folder, or a selected note's folder, or root).
func (t *Tree) targetFolder() string {
	n := t.selectedNode()
	if n == nil {
		return ""
	}
	if n.isFolder {
		return n.rel
	}
	return parentFolder(n.rel)
}

func (t *Tree) selectedNode() *node {
	obj := t.selection.SelectedItem()
	if obj == nil {
		return nil
	}
	row, ok := obj.Cast().(*gtk.TreeListRow)
	if !ok {
		return nil
	}
	return gioutil.ObjectValue[*node](row.Item())
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

func iconButton(iconName, tooltip string, onClick func()) *gtk.Button {
	b := gtk.NewButtonFromIconName(iconName)
	b.SetTooltipText(tooltip)
	b.AddCSSClass("flat")
	b.ConnectClicked(onClick)
	return b
}

func parentFolder(rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return ""
	}
	return d
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
