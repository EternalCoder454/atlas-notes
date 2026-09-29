package app

import (
	"fmt"
	"log"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/ui"
)

// guideNote is the name of the seeded walkthrough note. Vaults created before
// it was renamed still hold "Welcome", so openGuide accepts either.
const guideNote = "Getting Started"

// buildWelcome constructs the home screen shown when no note is open: a
// greeting, the things a new user actually wants to do, what is due, and a way
// back into recent work. It replaces landing on a "Welcome" note, which looked like
// an ordinary document the user had somehow already written.
func (a *App) buildWelcome() *gtk.Widget {
	outer := gtk.NewBox(gtk.OrientationVertical, 0)
	outer.AddCSSClass("welcome-page")
	outer.SetVExpand(true)
	outer.SetHExpand(true)

	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.SetVAlign(gtk.AlignCenter)
	content.SetHAlign(gtk.AlignCenter)
	content.SetVExpand(true)
	content.AddCSSClass("welcome-content")

	orb := ui.NewStaticOrb(76)
	orb.SetMarginBottom(6)
	content.Append(orb)

	title := gtk.NewLabel(greeting())
	title.AddCSSClass("welcome-title")
	content.Append(title)

	subtitle := gtk.NewLabel("Your notes and checklists live on this machine: plain files, no account, no cloud.")
	subtitle.AddCSSClass("welcome-subtitle")
	subtitle.SetWrap(true)
	subtitle.SetJustify(gtk.JustifyCenter)
	subtitle.SetMaxWidthChars(52)
	content.Append(subtitle)

	grid := gtk.NewGrid()
	grid.AddCSSClass("welcome-grid")
	grid.SetColumnSpacing(12)
	grid.SetRowSpacing(12)
	grid.SetColumnHomogeneous(true)
	grid.SetHAlign(gtk.AlignCenter)

	cards := []struct {
		icon, title, subtitle, accel string
		action                       func()
	}{
		{"atlasnotes-note-new-symbolic", "New note", "Start writing straight away", "Ctrl+N", a.actionNewNote},
		{"atlasnotes-checklist-symbolic", "New checklist", "A note that starts with tasks", "Ctrl+T", a.actionNewChecklist},
		// The action is registered with the others; going through it keeps one
		// place that knows what "today's note" means.
		{"atlasnotes-recent-symbolic", "Today's note", "Jot down today's plans", "Ctrl+D", func() { a.adw.ActivateAction("today", nil) }},
		{"atlasnotes-search-symbolic", "Find a note", "Search titles across the vault", "Ctrl+K", a.actionFocusSearch},
		{"atlasnotes-info-symbolic", "Read the guide", "Markdown, tasks and the assistant", "", a.openGuide},
	}
	for i, c := range cards {
		icon := c.icon
		if !hasIcon(icon) {
			icon = "atlasnotes-note-symbolic"
		}
		// With an odd number of cards the last one takes the whole row, rather
		// than leaving a hole beside it.
		span := 1
		if i == len(cards)-1 && len(cards)%2 == 1 {
			span = 2
		}
		grid.Attach(a.welcomeCard(icon, c.title, c.subtitle, c.accel, c.action), i%2, i/2, span, 1)
	}
	content.Append(grid)

	home = a.newHomeLists()
	content.Append(home.dueBox)
	content.Append(a.buildRecents())
	content.Append(home.tagsBox)
	a.refreshRecents()

	content.Append(a.welcomeFooter())

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(content)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	outer.Append(scroll)
	return &outer.Widget
}

// welcomeCard is one large, obvious entry point on the home screen.
func (a *App) welcomeCard(icon, title, subtitle, accel string, activate func()) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("welcome-card")
	btn.ConnectClicked(activate)

	row := gtk.NewBox(gtk.OrientationHorizontal, 12)
	img := gtk.NewImageFromIconName(icon)
	img.SetPixelSize(22)
	img.AddCSSClass("welcome-card-icon")
	img.SetVAlign(gtk.AlignCenter)
	row.Append(img)

	text := gtk.NewBox(gtk.OrientationVertical, 2)
	text.SetHExpand(true)
	head := gtk.NewLabel(title)
	head.SetXAlign(0)
	head.AddCSSClass("welcome-card-title")
	sub := gtk.NewLabel(subtitle)
	sub.SetXAlign(0)
	sub.AddCSSClass("welcome-card-subtitle")
	text.Append(head)
	text.Append(sub)
	row.Append(text)

	if accel != "" {
		key := gtk.NewLabel(accel)
		key.AddCSSClass("keycap")
		key.SetVAlign(gtk.AlignCenter)
		row.Append(key)
	}
	btn.SetChild(row)
	return btn
}

// recentSlots is how many recent notes the home screen offers.
const recentSlots = 4

// recentRow is one row of the home screen's recent list. The rows are built
// once and re-pointed at different notes, because every widget the bindings
// wrap stays resident once created — rebuilding the page on each visit grew
// memory for the life of the session.
type recentRow struct {
	button  *gtk.Button
	name    *gtk.Label
	when    *gtk.Label
	snippet *gtk.Label
	rel     string
}

// buildRecents creates the fixed set of recent-note rows.
func (a *App) buildRecents() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.AddCSSClass("welcome-recents")

	heading := gtk.NewLabel("Recent")
	heading.SetXAlign(0)
	heading.AddCSSClass("welcome-section")
	box.Append(heading)
	a.recentsHeading = heading

	a.recents = make([]*recentRow, 0, recentSlots)
	for i := 0; i < recentSlots; i++ {
		r := &recentRow{button: gtk.NewButton()}
		r.button.AddCSSClass("welcome-recent")
		r.button.ConnectClicked(func() {
			if r.rel != "" {
				a.openNote(r.rel)
			}
		})

		card := gtk.NewBox(gtk.OrientationVertical, 2)

		line := gtk.NewBox(gtk.OrientationHorizontal, 8)
		icon := gtk.NewImageFromIconName("atlasnotes-note-symbolic")
		icon.AddCSSClass("dim-label")
		line.Append(icon)

		r.name = gtk.NewLabel("")
		r.name.SetXAlign(0)
		r.name.SetHExpand(true)
		r.name.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		r.name.AddCSSClass("welcome-recent-title")
		line.Append(r.name)

		r.when = gtk.NewLabel("")
		r.when.AddCSSClass("welcome-recent-time")
		line.Append(r.when)
		card.Append(line)

		// Two lines of the note itself, so a list of file names becomes a list
		// you can recognise something in.
		r.snippet = gtk.NewLabel("")
		r.snippet.SetXAlign(0)
		r.snippet.SetWrap(true)
		r.snippet.SetLines(2)
		r.snippet.SetEllipsize(3)
		r.snippet.AddCSSClass("welcome-recent-snippet")
		card.Append(r.snippet)

		r.button.SetChild(card)
		box.Append(r.button)
		a.recents = append(a.recents, r)
	}
	return box
}

// refreshRecents re-points the existing rows at the latest notes.
//
// The home screen is refreshed whenever the vault changes underneath it, not
// only when it is opened, so the previews are cached against each note's
// modification time. Without that, a note being saved would re-read and
// decompress the four most recent notes — and a long one costs its whole
// length to read two lines out of.
func (a *App) refreshRecents() {
	if len(a.recents) == 0 {
		return
	}
	a.refreshHomeLists()
	var notes []storage.NoteMeta
	if a.store != nil {
		notes, _ = a.store.RecentNotes(recentSlots)
	}
	fresh := make(map[string]snippet, len(notes))
	for _, n := range notes {
		if cached, ok := a.snippets[n.Path]; ok && cached.modified.Equal(n.ModifiedAt) {
			fresh[n.Path] = cached
			continue
		}
		fresh[n.Path] = snippet{modified: n.ModifiedAt, text: a.noteSnippet(n.Path)}
	}
	a.snippets = fresh // anything no longer recent drops out with the old map
	if a.recentsHeading != nil {
		a.recentsHeading.SetVisible(len(notes) > 0)
	}
	for i, r := range a.recents {
		if i >= len(notes) {
			r.rel = ""
			r.button.SetVisible(false)
			continue
		}
		n := notes[i]
		r.rel = n.Path
		r.name.SetText(path.Base(n.Path))
		r.when.SetText(relativeTime(n.ModifiedAt))
		r.snippet.SetText(a.snippets[n.Path].text)
		r.snippet.SetVisible(r.snippet.Text() != "")
		r.button.SetVisible(true)
	}
}

// The two lists between the cards and the footer besides the recents: what is
// due, and the vault's tags. Like the recents they are a fixed set of widgets
// re-pointed at new data, since every widget the bindings wrap stays resident.
const (
	dueSlots      = 8 // rows of due tasks the home screen shows
	dueWindowDays = 7 // how far ahead "due" looks
	tagSlots      = 24
	dateLayout    = "2006-01-02"
)

// The groups a due task falls into, in the order they are listed.
const (
	dueOverdue = iota
	dueToday
	dueSoon
)

var dueGroupTitles = [...]string{"Overdue", "Today", "This week"}

// dueGroup says which group a task due on due (yyyy-mm-dd) belongs to, on the
// day today (also yyyy-mm-dd). Dates in this form order the way they read, so
// no parsing is needed.
func dueGroup(due, today string) int {
	switch {
	case due < today:
		return dueOverdue
	case due == today:
		return dueToday
	}
	return dueSoon
}

// planDue cuts the tasks to the rows there is room for and counts the rest.
func planDue(tasks []storage.DueTask, limit int) (shown []storage.DueTask, more int) {
	if len(tasks) <= limit {
		return tasks, 0
	}
	return tasks[:limit], len(tasks) - limit
}

// topTags is the n tags on the most notes, most first. The index lists them by
// name, and the sort is stable, so tags on as many notes stay alphabetical.
func topTags(tags []storage.TagCount, n int) []storage.TagCount {
	out := slices.Clone(tags)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Notes > out[j].Notes })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// dueSlot is one row of the Due list, with the heading of its group above it
// for the row that starts one.
type dueSlot struct {
	box     *gtk.Box
	heading *gtk.Label
	button  *gtk.Button
	text    *gtk.Label
	note    *gtk.Label
	chip    *gtk.Label
	rel     string
}

// tagPill is one button of the Tags list.
type tagPill struct {
	item   *gtk.FlowBoxChild
	button *gtk.Button
	name   *gtk.Label
	count  *gtk.Label
	tag    string
}

// homeLists are the Due and Tags sections of the home screen. There is one App
// per process and one home screen, so they are held here, rebuilt with the page.
type homeLists struct {
	dueBox  *gtk.Box
	slots   []*dueSlot
	dueMore *gtk.Label
	tagsBox *gtk.Box
	pills   []*tagPill

	gen       uint64 // tells a stale answer from the latest
	shownDue  []storage.DueTask
	shownDay  string // the day the due rows were grouped for
	shownTags []storage.TagCount
}

var home *homeLists

// newHomeLists builds both sections, hidden until there is something to show.
func (a *App) newHomeLists() *homeLists {
	h := &homeLists{}

	h.dueBox = gtk.NewBox(gtk.OrientationVertical, 2)
	h.dueBox.AddCSSClass("welcome-recents")
	h.dueBox.SetVisible(false)
	due := gtk.NewLabel("Due")
	due.SetXAlign(0)
	due.AddCSSClass("welcome-section")
	h.dueBox.Append(due)

	for i := 0; i < dueSlots; i++ {
		s := &dueSlot{box: gtk.NewBox(gtk.OrientationVertical, 2)}
		s.heading = gtk.NewLabel("")
		s.heading.SetXAlign(0)
		s.heading.SetMarginTop(6)
		s.heading.SetMarginStart(4)
		s.heading.AddCSSClass("caption-heading")
		s.heading.AddCSSClass("dim-label")
		s.box.Append(s.heading)

		s.button = gtk.NewButton()
		s.button.AddCSSClass("welcome-recent")
		s.button.ConnectClicked(func() {
			if s.rel != "" {
				a.openNote(s.rel)
			}
		})
		line := gtk.NewBox(gtk.OrientationHorizontal, 8)
		s.text = gtk.NewLabel("")
		s.text.SetXAlign(0)
		s.text.SetHExpand(true)
		s.text.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		// Ellipsizing alone leaves the label asking for its full length, and a
		// long task would widen the whole page.
		s.text.SetMaxWidthChars(44)
		s.text.AddCSSClass("welcome-recent-title")
		line.Append(s.text)
		s.note = gtk.NewLabel("")
		s.note.SetEllipsize(3)
		s.note.SetMaxWidthChars(20)
		s.note.AddCSSClass("dim-label")
		line.Append(s.note)
		s.chip = gtk.NewLabel("")
		s.chip.AddCSSClass("due-chip")
		line.Append(s.chip)
		s.button.SetChild(line)
		s.box.Append(s.button)

		s.box.SetVisible(false)
		h.dueBox.Append(s.box)
		h.slots = append(h.slots, s)
	}
	h.dueMore = gtk.NewLabel("")
	h.dueMore.SetXAlign(0)
	h.dueMore.SetMarginStart(4)
	h.dueMore.SetMarginTop(2)
	h.dueMore.AddCSSClass("dim-label")
	h.dueMore.SetVisible(false)
	h.dueBox.Append(h.dueMore)

	h.tagsBox = gtk.NewBox(gtk.OrientationVertical, 2)
	h.tagsBox.AddCSSClass("welcome-recents")
	h.tagsBox.SetVisible(false)
	tags := gtk.NewLabel("Tags")
	tags.SetXAlign(0)
	tags.AddCSSClass("welcome-section")
	h.tagsBox.Append(tags)

	flow := gtk.NewFlowBox()
	flow.SetSelectionMode(gtk.SelectionNone)
	flow.SetColumnSpacing(6)
	flow.SetRowSpacing(6)
	flow.SetHAlign(gtk.AlignStart)
	// Wraps at about the width of the cards above rather than running the page
	// out to one long line.
	flow.SetMaxChildrenPerLine(6)
	for i := 0; i < tagSlots; i++ {
		p := &tagPill{item: gtk.NewFlowBoxChild(), button: gtk.NewButton()}
		p.button.AddCSSClass("ai-chip") // the small rounded button the assistant's suggestions use
		p.button.ConnectClicked(func() {
			if p.tag != "" {
				a.showTag(p.tag)
			}
		})
		line := gtk.NewBox(gtk.OrientationHorizontal, 6)
		p.name = gtk.NewLabel("")
		p.name.SetEllipsize(3)
		p.name.SetMaxWidthChars(24)
		line.Append(p.name)
		p.count = gtk.NewLabel("")
		p.count.AddCSSClass("dim-label")
		line.Append(p.count)
		p.button.SetChild(line)
		p.item.SetChild(p.button)
		p.item.SetVisible(false)
		flow.Append(p.item)
		h.pills = append(h.pills, p)
	}
	h.tagsBox.Append(flow)
	return h
}

// showTag searches the vault for a tag, showing the vault panel if it is away.
func (a *App) showTag(tag string) { a.showTagged(tag) }

// refreshHomeLists brings the Due and Tags sections up to date.
//
// The queries run off the main thread, and only while the home screen is where
// the user is: the vault changes on every save, and a note being written has
// no use for a list nobody can see. Going home refreshes it. The answer is
// dropped if a newer question has been asked since.
func (a *App) refreshHomeLists() {
	h := home
	if h == nil || a.store == nil || a.currentNote != "" {
		return
	}
	h.gen++
	gen := h.gen
	now := time.Now()
	today := now.Format(dateLayout)
	through := now.AddDate(0, 0, dueWindowDays).Format(dateLayout)
	go func() {
		due, derr := a.store.DueTasks(through)
		tags, terr := a.store.Tags()
		coreglib.IdleAdd(func() bool {
			if a.closing || home != h || gen != h.gen {
				return false
			}
			if derr != nil {
				log.Printf("atlas-notes: due tasks: %v", derr)
			} else {
				h.showDue(due, today)
			}
			if terr != nil {
				log.Printf("atlas-notes: tags: %v", terr)
			} else {
				h.showTags(tags)
			}
			return false
		})
	}()
}

// showDue points the Due rows at tasks, which the index lists by date, so each
// group is one run. Nothing is touched when neither the tasks nor the day have
// changed.
func (h *homeLists) showDue(tasks []storage.DueTask, today string) {
	if today == h.shownDay && slices.Equal(tasks, h.shownDue) {
		return
	}
	h.shownDay, h.shownDue = today, tasks
	shown, more := planDue(tasks, len(h.slots))
	h.dueBox.SetVisible(len(shown) > 0)
	group := -1
	for i, s := range h.slots {
		if i >= len(shown) {
			s.rel = ""
			s.box.SetVisible(false)
			continue
		}
		t := shown[i]
		g := dueGroup(t.Due, today)
		s.heading.SetText(dueGroupTitles[g])
		s.heading.SetVisible(g != group)
		group = g
		s.rel = t.Path
		s.text.SetText(t.Text)
		s.note.SetText(path.Base(t.Path))
		dressHomeChip(s.chip, t.Due, g)
		s.box.SetVisible(true)
	}
	h.dueMore.SetText(fmt.Sprintf("and %d more", more))
	h.dueMore.SetVisible(more > 0)
}

// dressHomeChip writes a due date on a chip the way the checklist rows do: as
// "Jan 2", red once overdue and amber on the day.
func dressHomeChip(chip *gtk.Label, due string, group int) {
	chip.RemoveCSSClass("due-today")
	chip.RemoveCSSClass("due-overdue")
	t, err := time.Parse(dateLayout, due)
	if err != nil {
		chip.SetText(due)
		return
	}
	chip.SetText(t.Format("Jan 2"))
	switch group {
	case dueOverdue:
		chip.AddCSSClass("due-overdue")
		chip.SetTooltipText("Overdue · " + t.Format("Mon, Jan 2 2006"))
	case dueToday:
		chip.AddCSSClass("due-today")
		chip.SetTooltipText("Due today")
	default:
		chip.SetTooltipText("Due " + t.Format("Mon, Jan 2 2006"))
	}
}

// showTags points the pills at the vault's most used tags.
func (h *homeLists) showTags(tags []storage.TagCount) {
	top := topTags(tags, len(h.pills))
	if slices.Equal(top, h.shownTags) {
		return
	}
	h.shownTags = top
	h.tagsBox.SetVisible(len(top) > 0)
	for i, p := range h.pills {
		if i >= len(top) {
			p.tag = ""
			p.item.SetVisible(false)
			continue
		}
		p.tag = top[i].Tag
		p.name.SetText("#" + top[i].Tag)
		p.count.SetText(strconv.Itoa(top[i].Notes))
		p.button.SetTooltipText(plural(top[i].Notes, "note"))
		p.item.SetVisible(true)
	}
}

// snippet is a note's cached preview, valid while the note is untouched.
type snippet struct {
	modified time.Time
	text     string
}

// snippetChars is how much of a note the home screen shows. Two lines' worth at
// the card's width, with a little to spare for the ellipsis to eat.
const snippetChars = 140

// noteSnippet is the opening of a note as prose: no title, no markdown markers,
// no blank lines. It reads the note — four of them, only when the home screen
// is built or the vault changes underneath it, on files of a few KB.
func (a *App) noteSnippet(rel string) string {
	if a.store == nil {
		return ""
	}
	content, err := a.store.ReadNote(rel)
	if err != nil {
		return ""
	}
	return summarizeOpening(content)
}

// summarizeOpening turns the start of a markdown note into a flat line of
// prose. It skips the title heading (the card already shows the name) and
// anything that is punctuation rather than words.
func summarizeOpening(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isRule(line) {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue // headings, including the note's own title
		}
		line = stripMarkers(line)
		if line == "" {
			continue
		}
		// Separate the lines rather than running them together: a preview made
		// of three bullets joined by spaces reads as one broken sentence.
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(line)
		if b.Len() >= snippetChars {
			break
		}
	}
	return truncate(strings.TrimSpace(b.String()), snippetChars)
}

// truncate shortens s to at most n bytes and appends an ellipsis.
//
// It cuts on a character boundary, and then back to a word boundary. Slicing
// the string at n directly splits whatever character spans that byte, and the
// preview ends in a replacement glyph: the separator this builds previews with
// is itself two bytes wide, so it happened readily.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if space := strings.LastIndexByte(s[:cut], ' '); space > n/2 {
		cut = space
	}
	return strings.TrimRight(strings.TrimSpace(s[:cut]), "·,;:") + "…"
}

// isRule reports whether a line is a horizontal rule, which carries no words.
func isRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	c := line[0]
	return (c == '-' || c == '*' || c == '_') && strings.Count(line, string(c)) == len(line)
}

// stripMarkers removes the markdown a snippet has no way to render: list
// bullets and task boxes at the front, emphasis and code marks throughout, and
// the HTML comment a task line carries its priority and due date in. That last
// one is not decoration the reader can ignore — left in, the home screen shows
// "Renew the travel insurance <!-- priority:high due:2026-10-05 -->".
func stripMarkers(line string) string {
	for _, prefix := range []string{"- [ ] ", "- [x] ", "- [X] ", "> ", "- ", "* ", "+ "} {
		if strings.HasPrefix(line, prefix) {
			line = line[len(prefix):]
			break
		}
	}
	line = stripComments(line)
	return strings.TrimSpace(strings.NewReplacer(
		"**", "", "*", "", "~~", "", "`", "", "__", "", "_", "",
	).Replace(line))
}

// stripComments removes every "<!-- … -->" span from a line. An unterminated
// one takes the rest of the line with it: whatever it was meant to hide is not
// something to show by accident.
func stripComments(line string) string {
	for {
		open := strings.Index(line, "<!--")
		if open < 0 {
			return line
		}
		rest := line[open+len("<!--"):]
		close := strings.Index(rest, "-->")
		if close < 0 {
			return strings.TrimSpace(line[:open])
		}
		line = line[:open] + rest[close+len("-->"):]
	}
}

// welcomeFooter states where the notes actually live — the app's main promise,
// spelled out rather than implied.
func (a *App) welcomeFooter() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetHAlign(gtk.AlignCenter)
	box.AddCSSClass("welcome-footer")

	icon := gtk.NewImageFromIconName("atlasnotes-folder-symbolic")
	icon.AddCSSClass("dim-label")
	box.Append(icon)

	vault := a.cfg.VaultPath
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(vault, home) {
		vault = "~" + strings.TrimPrefix(vault, home)
	}
	label := gtk.NewLabel(vault)
	label.AddCSSClass("welcome-footer-text")
	label.SetEllipsize(3)
	label.SetTooltipText("Every note is a plain, compressed Markdown file in this folder")
	box.Append(label)
	return box
}

// openGuide opens the seeded walkthrough note, seeding it again if the user has
// since deleted it.
func (a *App) openGuide() {
	if a.store == nil {
		return
	}
	for _, name := range []string{guideNote, "Welcome"} {
		if _, err := a.store.ReadNote(name); err == nil {
			a.openNote(name)
			return
		}
	}
	if err := a.store.WriteNote(guideNote, storage.GuideMarkdown()); err == nil {
		if a.tree != nil {
			a.tree.ForceRefresh()
		}
		a.openNote(guideNote)
	}
}

// greeting is a small, time-of-day welcome rather than a fixed banner.
func greeting() string {
	switch h := time.Now().Hour(); {
	case h < 5:
		return "Still up?"
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	default:
		return "Good evening"
	}
}

// relativeTime renders a timestamp the way a human would say it.
func relativeTime(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
