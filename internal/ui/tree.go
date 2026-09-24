// Package ui holds the folder-tree panel and the AI sidebar — the side-panel GTK
// widgets composed by package app.
package ui

import (
	"fmt"
	"hash/fnv"
	"log"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/storage"
)

// indentStep is the per-depth indentation (px) for child rows — about half the
// stock GtkTreeExpander indent, so nested notes sit closer to the panel edge.
const indentStep = 12

// vaultEntry is a note as the search field needs it.
type vaultEntry struct {
	meta        storage.NoteMeta
	name        string // base name
	lowerName   string
	lowerFolder string
}

// node is one row in the vault tree: a folder or a note.
type node struct {
	name     string // display name
	rel      string // vault-relative path (folder path, or note path without ext)
	folder   string // parent folder, shown as a caption in search results
	isFolder bool
	rank     int // search-match position; unused outside search results
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
	// entries is the vault's notes in the form the panel needs them: names
	// already split out and lower-cased, so filtering allocates nothing per
	// keystroke.
	entries    []vaultEntry
	cacheValid bool
	// childIndex groups the cached entries by parent folder. GTK asks for a
	// folder's children every time it probes a row for expandability, and
	// scanning the whole vault for each of those calls made opening a large
	// vault quadratic.
	childIndex map[string][]*node
	// childModels caches the GTK list model handed to the tree for each folder,
	// and signature is what the last Refresh rendered. Rebuilding either when
	// nothing changed is not free: every model and every row object the
	// bindings wrap stays resident for the life of the process, so a refresh
	// after each save used to cost about a megabyte.
	childModels map[string]*gioutil.ListModel[*node]
	signature   string

	searchEntry *gtk.SearchEntry
	countLabel  *gtk.Label
	emptyState  *gtk.Box
	emptyTitle  *gtk.Label
	emptyHint   *gtk.Label
	scroll      *gtk.ScrolledWindow
	query       string // current search text, lowercased
	sortRecent  bool   // sort notes by last modified instead of by name

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
	// OnChanged is invoked after the vault's contents change, so the rest of the
	// app (the home screen's recent list) can refresh.
	OnChanged func()
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
	t.scroll = scroll

	// Right-click on empty space → root-level New Note / New Folder.
	bg := gtk.NewGestureClick()
	bg.SetButton(3)
	bg.ConnectPressed(func(_ int, x, y float64) {
		t.showContextMenu(scroll, x, y, nil)
	})
	scroll.AddController(bg)

	t.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	t.widget.SetVExpand(true)
	t.widget.Append(t.buildHeader())
	t.widget.Append(scroll)
	t.emptyState = t.buildEmptyState()
	t.widget.Append(t.emptyState)

	t.Refresh()
	return t
}

// buildHeader is the panel's top bar: the vault's name and note count, a New
// Note button, and the search field that filters the tree as you type. Before
// this, the only way to create or find anything was a right-click.
func (t *Tree) buildHeader() *gtk.Box {
	header := gtk.NewBox(gtk.OrientationVertical, 8)
	header.AddCSSClass("vault-header")

	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	title := gtk.NewLabel("Vault")
	title.SetXAlign(0)
	title.AddCSSClass("vault-title")
	top.Append(title)

	t.countLabel = gtk.NewLabel("")
	t.countLabel.AddCSSClass("vault-count")
	t.countLabel.SetHExpand(true)
	t.countLabel.SetXAlign(0)
	top.Append(t.countLabel)

	sortBtn := gtk.NewButtonFromIconName("view-sort-descending-symbolic")
	sortBtn.AddCSSClass("flat")
	sortBtn.SetTooltipText("Sort by name")
	sortBtn.ConnectClicked(func() {
		t.sortRecent = !t.sortRecent
		if t.sortRecent {
			sortBtn.SetTooltipText("Sort by last edited")
			sortBtn.SetIconName("document-open-recent-symbolic")
		} else {
			sortBtn.SetTooltipText("Sort by name")
			sortBtn.SetIconName("view-sort-descending-symbolic")
		}
		t.Refresh()
	})
	top.Append(sortBtn)

	newBtn := gtk.NewButtonFromIconName("list-add-symbolic")
	newBtn.AddCSSClass("flat")
	newBtn.SetTooltipText("New note here (Ctrl+N)")
	newBtn.ConnectClicked(func() { t.promptNewNote(t.SelectedFolder()) })
	top.Append(newBtn)
	header.Append(top)

	t.searchEntry = gtk.NewSearchEntry()
	t.searchEntry.SetPlaceholderText("Search notes…")
	t.searchEntry.AddCSSClass("vault-search")
	t.searchEntry.ConnectSearchChanged(func() {
		t.query = strings.ToLower(strings.TrimSpace(t.searchText()))
		t.Refresh()
	})
	header.Append(t.searchEntry)
	return header
}

// buildEmptyState explains what to do when the panel has nothing to show,
// rather than leaving a blank column.
func (t *Tree) buildEmptyState() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("vault-empty")
	box.SetVAlign(gtk.AlignCenter)
	box.SetVExpand(true)
	box.SetVisible(false)

	icon := gtk.NewImageFromIconName("folder-symbolic")
	icon.SetPixelSize(28)
	icon.AddCSSClass("dim-label")
	box.Append(icon)

	t.emptyTitle = gtk.NewLabel("No notes yet")
	t.emptyTitle.AddCSSClass("vault-empty-title")
	box.Append(t.emptyTitle)

	t.emptyHint = gtk.NewLabel("Press Ctrl+N to write your first one")
	t.emptyHint.AddCSSClass("vault-empty-hint")
	t.emptyHint.SetWrap(true)
	t.emptyHint.SetJustify(gtk.JustifyCenter)
	t.emptyHint.SetMaxWidthChars(24)
	box.Append(t.emptyHint)
	return box
}

// searchText reads the search field's current contents.
func (t *Tree) searchText() string {
	if t.searchEntry == nil {
		return ""
	}
	return t.searchEntry.Text()
}

// SetSearch runs a search programmatically.
func (t *Tree) SetSearch(q string) {
	if t.searchEntry != nil {
		t.searchEntry.SetText(q)
	}
}

// FocusSearch puts the caret in the search field (Ctrl+K).
func (t *Tree) FocusSearch() {
	if t.searchEntry != nil {
		t.searchEntry.GrabFocus()
		t.searchEntry.SelectRegion(0, -1)
	}
}

// SelectedFolder returns the folder new items should be created in: the
// selected folder, the folder of the selected note, or the vault root.
func (t *Tree) SelectedFolder() string {
	pos := t.selection.Selected()
	if pos == gtk.InvalidListPosition {
		return ""
	}
	row := t.rowAt(pos)
	if row == nil {
		return ""
	}
	n := gioutil.ObjectValue[*node](row.Item())
	if n == nil {
		return ""
	}
	if n.isFolder {
		return n.rel
	}
	return parentFolder(n.rel)
}

// PromptNewFolder asks for a name and creates a folder beside the selection.
func (t *Tree) PromptNewFolder() { t.promptNewFolder(t.SelectedFolder()) }

// Widget returns the root widget of the panel.
func (t *Tree) Widget() gtk.Widgetter { return t.widget }

// Refresh rebuilds the root level of the tree from storage, preserving folder
// expansion state and re-selecting the open note (so a rename or autosave no
// longer collapses folders or loses the highlight).
func (t *Tree) Refresh() {
	t.refresh(false)
}

// ForceRefresh re-reads the vault and rebuilds the tree. Every path that
// changes the vault goes through it, so the cached copy above can be trusted
// in between.
func (t *Tree) ForceRefresh() {
	t.cacheValid = false
	t.refresh(true)
}

func (t *Tree) refresh(force bool) {
	expanded := t.snapshotExpanded()
	t.reloadCache()
	if sig := t.vaultSignature(); !force && sig == t.signature {
		return // nothing the tree shows has changed
	} else {
		t.signature = sig
	}

	if t.query != "" {
		// Searching flattens the tree: every matching note, wherever it lives.
		matches := t.matchingNotes()
		syncModel(t.rootModel, matches)
		t.updateHeader(len(matches))
		t.updateEmptyState(len(matches))
		return
	}

	syncModel(t.rootModel, t.childrenOf(""))
	t.restoreExpanded(expanded)
	if t.currentRel != "" {
		t.revealAndSelect(t.currentRel)
	}
	t.updateHeader(len(t.entries))
	t.updateEmptyState(len(t.entries) + len(t.cachedFolders))
}

// vaultSignature summarizes everything the panel actually displays, so a
// refresh that would render the same rows can skip the rebuild.
//
// Modification times are part of it only when the panel is sorting by them.
// Otherwise every save would change the signature and rebuild the list for a
// change nobody can see — and rebuilding is expensive: GTK recreates the row
// widgets, and every widget the bindings wrap stays resident afterwards.
func (t *Tree) vaultSignature() string {
	h := fnv.New64a()
	fmt.Fprintf(h, "q=%s r=%v\n", t.query, t.sortRecent)
	for _, f := range t.cachedFolders {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	for i := range t.entries {
		e := &t.entries[i]
		h.Write([]byte(e.meta.Path))
		if t.sortRecent {
			fmt.Fprintf(h, "|%d", e.meta.ModifiedAt.Unix())
		}
		h.Write([]byte{'\n'})
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// matchingNotes returns the notes whose name contains the search query, best
// matches first (name prefix, then position in the name).
func (t *Tree) matchingNotes() []*node {
	var out []*node
	for i := range t.entries {
		e := &t.entries[i]
		idx := strings.Index(e.lowerName, t.query)
		if idx < 0 && !strings.Contains(e.lowerFolder, t.query) {
			continue
		}
		out = append(out, &node{
			name: e.name, rel: e.meta.Path, folder: e.meta.Folder,
			created: e.meta.CreatedAt, modified: e.meta.ModifiedAt, rank: idx,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].rank < 0) != (out[j].rank < 0) {
			return out[j].rank < 0
		}
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// syncModel brings a list model in line with want, touching only the rows that
// actually differ.
//
// Replacing the contents wholesale is the obvious way to do this and the
// expensive one: GTK rebuilds a row widget for every entry, and every object
// the bindings wrap stays resident afterwards — a rename in a 300-note vault
// cost megabytes. Matching the unchanged head and tail first means a rename,
// a new note or a deletion splices one row.
func syncModel(m *gioutil.ListModel[*node], want []*node) {
	have := m.Len()
	head := 0
	for head < have && head < len(want) && sameNode(m.At(head), want[head]) {
		head++
	}
	tail := 0
	for tail < have-head && tail < len(want)-head &&
		sameNode(m.At(have-1-tail), want[len(want)-1-tail]) {
		tail++
	}
	removals := have - head - tail
	additions := want[head : len(want)-tail]
	if removals == 0 && len(additions) == 0 {
		return
	}
	m.Splice(head, removals, additions...)
}

// sameNode reports whether two rows show the same thing.
func sameNode(a, b *node) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.rel == b.rel && a.isFolder == b.isFolder && a.name == b.name &&
		a.folder == b.folder && a.modified.Equal(b.modified)
}

// updateHeader keeps the note count beside the panel title current.
func (t *Tree) updateHeader(n int) {
	if t.countLabel == nil {
		return
	}
	switch {
	case t.query != "":
		t.countLabel.SetText(fmt.Sprintf("· %d found", n))
	case n == 1:
		t.countLabel.SetText("· 1 note")
	default:
		t.countLabel.SetText(fmt.Sprintf("· %d notes", n))
	}
}

// updateEmptyState swaps the list for an explanation when there is nothing to
// show, and tailors the wording to an empty vault or an empty search.
func (t *Tree) updateEmptyState(n int) {
	if t.emptyState == nil || t.scroll == nil {
		return
	}
	empty := n == 0
	t.emptyState.SetVisible(empty)
	t.scroll.SetVisible(!empty)
	if !empty {
		return
	}
	if t.query != "" {
		t.emptyTitle.SetText("No matches")
		t.emptyHint.SetText("Nothing in the vault matches “" + t.searchText() + "”")
		return
	}
	t.emptyTitle.SetText("No notes yet")
	t.emptyHint.SetText("Press Ctrl+N to write your first one")
}

// reloadCache snapshots the folder and note lists once, so childrenOf (which
// GTK calls per folder while probing expandability) doesn't re-walk the
// reloadCache re-reads the vault, unless the copy in hand is still good. The
// vault only changes when the app changes it, so typing in the search field —
// which refreshes on every keystroke — reuses this instead of walking the
// filesystem and re-querying the index each time.
func (t *Tree) reloadCache() {
	if t.cacheValid {
		return
	}
	t.cacheValid = true
	t.cachedFolders, _ = t.store.ListFolders()
	notes, _ := t.store.ListNotes()

	t.entries = make([]vaultEntry, 0, len(notes))
	t.childIndex = make(map[string][]*node, len(t.cachedFolders)+1)
	for _, f := range t.cachedFolders {
		parent := parentFolder(f)
		t.childIndex[parent] = append(t.childIndex[parent], &node{
			name: path.Base(f), rel: f, isFolder: true,
		})
	}
	for _, n := range notes {
		base := path.Base(n.Path)
		t.entries = append(t.entries, vaultEntry{
			meta:        n,
			name:        base,
			lowerName:   strings.ToLower(base),
			lowerFolder: strings.ToLower(n.Folder),
		})
		t.childIndex[n.Folder] = append(t.childIndex[n.Folder], &node{
			name: base, rel: n.Path, folder: n.Folder,
			created: n.CreatedAt, modified: n.ModifiedAt,
		})
	}
	for _, children := range t.childIndex {
		t.sortNodes(children)
	}
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
	t.ForceRefresh()
	return true
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
	m, ok := t.childModels[n.rel]
	if !ok {
		m = gioutil.NewListModel[*node]()
		if t.childModels == nil {
			t.childModels = map[string]*gioutil.ListModel[*node]{}
		}
		t.childModels[n.rel] = m
	}
	syncModel(m, children)
	return m.ListModel
}

// childrenOf returns the immediate folders and notes inside a vault folder,
// straight out of the index built by reloadCache.
func (t *Tree) childrenOf(folderRel string) []*node {
	return t.childIndex[folderRel]
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

// sortNodes orders a folder's children: folders first, then notes by name or,
// when the panel is in "recently edited" mode, by modification time.
func (t *Tree) sortNodes(ns []*node) {
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].isFolder != ns[j].isFolder {
			return ns[i].isFolder // folders before notes
		}
		if t.sortRecent && !ns[i].isFolder {
			return ns[i].modified.After(ns[j].modified)
		}
		return strings.ToLower(ns[i].name) < strings.ToLower(ns[j].name)
	})
}
