package ui

import (
	"context"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// How one row of the vault panel is built and filled in: the list factory, the
// drag-and-drop that moves a note into a folder, the tooltip, and the optional
// one-sentence summary the assistant writes for it.

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
	caption := gtk.NewLabel("")
	caption.AddCSSClass("row-caption")
	caption.SetEllipsize(pango.EllipsizeStart)
	caption.SetVisible(false)
	box.Append(icon)
	box.Append(label)
	box.Append(caption)
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
		if caption, ok := label.NextSibling().(*gtk.Label); ok {
			// In search results the folder is shown beside the name, since the
			// flattened list loses the tree's context.
			if t.query != "" && n.folder != "" {
				caption.SetText(n.folder)
				caption.SetVisible(true)
			} else {
				caption.SetVisible(false)
			}
		}
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
