package app

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// The Tasks page: every unfinished task in the vault, grouped by when it is
// due. It reads the index rather than the notes, so it opens as fast for a
// vault of thousands of notes as for ten. Like the home screen it is one page
// built the first time it is shown, and afterwards only its contents change:
// rows are kept in a pool and re-dressed, because every widget the bindings
// wrap stays resident, so building rows per refresh would grow without bound.

// The groups a task falls into, in the order they are listed.
const (
	taskOverdue = iota
	taskToday
	taskWeek
	taskLater
	taskSomeday
	taskGroups
)

var taskGroupTitles = [taskGroups]string{"Overdue", "Today", "This week", "Later", "No date"}

// tasksShownMax bounds the rows on the page; a vault with more open tasks than
// this is not helped by a longer list, and says how many are not shown.
const tasksShownMax = 300

// taskGroup says which group a task due on due (yyyy-mm-dd, or empty) belongs
// to, given today and the last day of "this week" (both yyyy-mm-dd). Dates in
// this form order the way they read, so no parsing is needed.
func taskGroup(due, today, weekEnd string) int {
	switch {
	case due == "":
		return taskSomeday
	case due < today:
		return taskOverdue
	case due == today:
		return taskToday
	case due <= weekEnd:
		return taskWeek
	}
	return taskLater
}

// groupTasks sorts tasks into their groups, keeping the order they came in.
func groupTasks(tasks []storage.DueTask, today, weekEnd string) [taskGroups][]storage.DueTask {
	var out [taskGroups][]storage.DueTask
	for _, t := range tasks {
		g := taskGroup(t.Due, today, weekEnd)
		out[g] = append(out[g], t)
	}
	return out
}

// taskRowWidgets is one row of the page, reused for different tasks.
type taskRowWidgets struct {
	box    *gtk.Box
	check  *gtk.CheckButton
	text   *gtk.Label
	note   *gtk.Label
	pri    *gtk.Label
	chip   *gtk.Label
	parent *gtk.Box // the group's list it is in now, nil when in none
	task   storage.DueTask
	busy   bool // set while the row is being dressed, so it is not a click
}

// tasksPage holds the page's widgets. One App, one page.
type tasksPage struct {
	root     *gtk.Box
	subtitle *gtk.Label
	empty    *gtk.Box
	groups   [taskGroups]struct {
		box     *gtk.Box // heading and list
		heading *gtk.Label
		list    *gtk.Box
	}
	more *gtk.Label
	pool []*taskRowWidgets
	gen  uint64 // tells a stale answer from the latest
}

var tasksUI *tasksPage

// buildTasks constructs the page.
func (a *App) buildTasks() *gtk.Widget {
	p := &tasksPage{}
	tasksUI = p
	p.root = gtk.NewBox(gtk.OrientationVertical, 0)
	p.root.AddCSSClass("tasks-page")
	p.root.SetVExpand(true)
	p.root.SetHExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.AddCSSClass("tasks-content")

	title := gtk.NewLabel("Tasks")
	title.SetXAlign(0)
	title.AddCSSClass("tasks-title")
	content.Append(title)
	p.subtitle = gtk.NewLabel("")
	p.subtitle.SetXAlign(0)
	p.subtitle.AddCSSClass("tasks-subtitle")
	content.Append(p.subtitle)

	for g := range p.groups {
		box := gtk.NewBox(gtk.OrientationVertical, 4)
		box.AddCSSClass("tasks-group")
		heading := gtk.NewLabel("")
		heading.SetXAlign(0)
		heading.AddCSSClass("welcome-section")
		heading.AddCSSClass("tasks-heading-" + strings.ToLower(strings.ReplaceAll(taskGroupTitles[g], " ", "-")))
		list := gtk.NewBox(gtk.OrientationVertical, 4)
		box.Append(heading)
		box.Append(list)
		box.SetVisible(false)
		content.Append(box)
		p.groups[g].box, p.groups[g].heading, p.groups[g].list = box, heading, list
	}

	p.more = gtk.NewLabel("")
	p.more.SetXAlign(0)
	p.more.SetMarginTop(8)
	p.more.AddCSSClass("dim-label")
	p.more.SetVisible(false)
	content.Append(p.more)

	p.empty = gtk.NewBox(gtk.OrientationVertical, 8)
	p.empty.AddCSSClass("tasks-empty")
	p.empty.SetVisible(false)
	icon := gtk.NewImageFromIconName("atlasnotes-tasks-board-symbolic")
	icon.AddCSSClass("tasks-empty-icon")
	p.empty.Append(icon)
	none := gtk.NewLabel("Nothing to do")
	none.AddCSSClass("tasks-empty-title")
	p.empty.Append(none)
	hint := gtk.NewLabel("Every open task in your notes shows up here. Start a line with - [ ] to add one.")
	hint.SetWrap(true)
	hint.SetJustify(gtk.JustifyCenter)
	hint.AddCSSClass("dim-label")
	p.empty.Append(hint)
	content.Append(p.empty)

	clamp := adw.NewClamp()
	clamp.SetMaximumSize(760)
	clamp.SetTighteningThreshold(640)
	clamp.SetChild(content)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(clamp)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	p.root.Append(scroll)
	return &p.root.Widget
}

// newTaskRow builds one row: the box that completes the task, and a button
// holding its text, its note and its pills that opens the note at that task.
func (a *App) newTaskRow() *taskRowWidgets {
	r := &taskRowWidgets{}
	r.box = gtk.NewBox(gtk.OrientationHorizontal, 8)
	r.box.AddCSSClass("tasks-row")

	r.check = gtk.NewCheckButton()
	r.check.SetVAlign(gtk.AlignCenter)
	r.check.SetTooltipText("Mark as done")
	r.check.ConnectToggled(func() {
		if r.busy || !r.check.Active() {
			return
		}
		a.completeTask(r.task)
	})
	r.box.Append(r.check)

	btn := gtk.NewButton()
	btn.AddCSSClass("welcome-recent")
	btn.AddCSSClass("tasks-open")
	btn.SetHExpand(true)
	btn.ConnectClicked(func() { a.openNoteAtLine(r.task.Path, r.task.Line) })
	line := gtk.NewBox(gtk.OrientationHorizontal, 8)

	words := gtk.NewBox(gtk.OrientationVertical, 1)
	words.SetHExpand(true)
	r.text = gtk.NewLabel("")
	r.text.SetXAlign(0)
	r.text.SetEllipsize(3) // PANGO_ELLIPSIZE_END
	r.text.SetMaxWidthChars(60)
	r.text.AddCSSClass("welcome-recent-title")
	words.Append(r.text)
	r.note = gtk.NewLabel("")
	r.note.SetXAlign(0)
	r.note.SetEllipsize(3)
	r.note.SetMaxWidthChars(60)
	r.note.AddCSSClass("tasks-note")
	words.Append(r.note)
	line.Append(words)

	r.pri = gtk.NewLabel("")
	r.pri.SetVAlign(gtk.AlignCenter)
	r.pri.AddCSSClass("tasks-priority")
	line.Append(r.pri)
	r.chip = gtk.NewLabel("")
	r.chip.SetVAlign(gtk.AlignCenter)
	r.chip.AddCSSClass("due-chip")
	line.Append(r.chip)
	btn.SetChild(line)
	r.box.Append(btn)
	return r
}

// dress points a row at a task.
func (r *taskRowWidgets) dress(t storage.DueTask, group int) {
	r.task = t
	r.busy = true
	r.check.SetActive(false)
	r.busy = false
	r.text.SetText(t.Text)
	r.text.SetTooltipText(t.Text)
	folder := path.Dir(t.Path)
	if folder == "." {
		r.note.SetText(path.Base(t.Path))
	} else {
		r.note.SetText(path.Base(t.Path) + " · " + folder)
	}
	for _, c := range []string{"priority-high", "priority-medium", "priority-low"} {
		r.pri.RemoveCSSClass(c)
	}
	switch t.Priority {
	case "high", "medium", "low":
		r.pri.SetText(strings.ToUpper(t.Priority[:1]) + t.Priority[1:])
		r.pri.AddCSSClass("priority-" + t.Priority)
		r.pri.SetVisible(true)
	default:
		r.pri.SetVisible(false)
	}
	if t.Due == "" {
		r.chip.SetVisible(false)
		return
	}
	r.chip.SetVisible(true)
	dressHomeChip(r.chip, t.Due, homeGroup(group))
}

// homeGroup maps a page group onto the home screen's three, which is what its
// chip colouring knows: red when overdue, amber today, plain otherwise.
func homeGroup(g int) int {
	switch g {
	case taskOverdue:
		return dueOverdue
	case taskToday:
		return dueToday
	}
	return dueSoon
}

// showTasks switches the center panel to the Tasks page. No note is open while
// it shows, exactly as on the home screen, so a note ticked from here is never
// one the editor holds a stale copy of.
func (a *App) showTasks() {
	if a.centerStack == nil {
		return
	}
	a.closeFind(false)
	a.flushDirty() // a note edited just before must be on disk before it is read
	a.backlinksGen++
	a.currentNote = ""
	a.dirty = false
	if a.editor != nil {
		a.editor.SetContent("")
	}
	if a.tree != nil {
		a.tree.SetCurrent("")
	}
	if a.centerStack.ChildByName("tasks") == nil {
		a.centerStack.AddNamed(a.buildTasks(), "tasks")
	}
	a.refreshTasks()
	a.centerStack.SetVisibleChildName("tasks")
	a.setWindowSubtitle("")
	a.syncNoteActions()
	if a.backlinksBar != nil {
		a.backlinksBar.SetVisible(false)
	}
}

// tasksShowing says whether the Tasks page is what the center panel shows.
func (a *App) tasksShowing() bool {
	return a.centerStack != nil && a.centerStack.VisibleChildName() == "tasks"
}

// refreshTasks asks the index for the open tasks, off the main thread, and
// dresses the page with the answer. An answer to a question since replaced is
// dropped.
func (a *App) refreshTasks() {
	p := tasksUI
	if p == nil || a.store == nil {
		return
	}
	p.gen++
	gen := p.gen
	go func() {
		tasks, err := a.store.OpenTasks()
		coreglib.IdleAdd(func() bool {
			if gen != p.gen {
				return false
			}
			if err != nil {
				a.toast("Couldn't read your tasks: " + err.Error())
				return false
			}
			now := time.Now()
			p.show(a, tasks, now.Format(dateLayout), now.AddDate(0, 0, dueWindowDays).Format(dateLayout))
			return false
		})
	}()
}

// show dresses the page with tasks.
func (p *tasksPage) show(a *App, tasks []storage.DueTask, today, weekEnd string) {
	// Every row goes back to the pool first; the ones still needed are taken
	// out again below.
	for _, r := range p.pool {
		if r.parent != nil {
			r.parent.Remove(r.box)
			r.parent = nil
		}
	}
	more := 0
	if len(tasks) > tasksShownMax {
		more = len(tasks) - tasksShownMax
		tasks = tasks[:tasksShownMax]
	}
	grouped := groupTasks(tasks, today, weekEnd)
	next := 0
	for g := range grouped {
		list := grouped[g]
		p.groups[g].box.SetVisible(len(list) > 0)
		p.groups[g].heading.SetText(fmt.Sprintf("%s · %d", taskGroupTitles[g], len(list)))
		for _, t := range list {
			if next == len(p.pool) {
				p.pool = append(p.pool, a.newTaskRow())
			}
			r := p.pool[next]
			next++
			r.dress(t, g)
			p.groups[g].list.Append(r.box)
			r.parent = p.groups[g].list
		}
	}
	total := len(tasks) + more
	switch total {
	case 0:
		p.subtitle.SetText("")
	case 1:
		p.subtitle.SetText("1 open task")
	default:
		p.subtitle.SetText(fmt.Sprintf("%d open tasks", total))
	}
	p.empty.SetVisible(total == 0)
	p.more.SetText(fmt.Sprintf("and %d more", more))
	p.more.SetVisible(more > 0)
}

// completeTask ticks a task in its note through the store, so locking, history
// and saving all apply, then brings the page up to date. The write is off the
// main thread, as a note save is.
func (a *App) completeTask(t storage.DueTask) {
	if a.store == nil {
		return
	}
	go func() {
		err := a.store.CompleteTask(t.Path, t.Line, t.Text)
		coreglib.IdleAdd(func() bool {
			switch err {
			case nil:
			case storage.ErrLocked:
				a.toast("That note is locked. Unlock it to tick its tasks.")
			case storage.ErrTaskMoved:
				a.toast("That note changed. The list has been refreshed.")
			default:
				a.toast("Couldn't tick that task: " + err.Error())
			}
			a.refreshTasks()
			return false
		})
	}()
}

// openNoteAtLine opens a note with the caret on a line of it.
func (a *App) openNoteAtLine(rel string, line int) {
	a.openNote(rel)
	if a.currentNote == rel && a.editor != nil {
		a.editor.RevealLine(line)
	}
}
