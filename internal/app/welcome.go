package app

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/ui"
)

// guideNote is the name of the seeded walkthrough note. Vaults created before
// it was renamed still hold "Welcome", so openGuide accepts either.
const guideNote = "Getting Started"

// buildWelcome constructs the home screen shown when no note is open: a
// greeting, the four things a new user actually wants to do, and a way back
// into recent work. It replaces landing on a "Welcome" note, which looked like
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

	subtitle := gtk.NewLabel("Your notes and checklists live on this machine — plain files, no account, no cloud.")
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
		{"document-new-symbolic", "New note", "Start writing straight away", "Ctrl+N", a.actionNewNote},
		{"atlas-task-symbolic", "New checklist", "A note that starts with tasks", "Ctrl+T", a.actionNewChecklist},
		{"system-search-symbolic", "Find a note", "Search titles across the vault", "Ctrl+K", a.actionFocusSearch},
		{"help-about-symbolic", "Read the guide", "Markdown, tasks and the assistant", "", a.openGuide},
	}
	for i, c := range cards {
		icon := c.icon
		if !hasIcon(icon) {
			icon = "text-x-generic-symbolic"
		}
		grid.Attach(a.welcomeCard(icon, c.title, c.subtitle, c.accel, c.action), i%2, i/2, 1, 1)
	}
	content.Append(grid)

	content.Append(a.buildRecents())
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
		icon := gtk.NewImageFromIconName("text-x-generic-symbolic")
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
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(line)
		if b.Len() >= snippetChars {
			break
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > snippetChars {
		out = strings.TrimSpace(out[:snippetChars]) + "…"
	}
	return out
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
// bullets and task boxes at the front, emphasis and code marks throughout.
func stripMarkers(line string) string {
	for _, prefix := range []string{"- [ ] ", "- [x] ", "- [X] ", "> ", "- ", "* ", "+ "} {
		if strings.HasPrefix(line, prefix) {
			line = line[len(prefix):]
			break
		}
	}
	return strings.TrimSpace(strings.NewReplacer(
		"**", "", "*", "", "~~", "", "`", "", "__", "", "_", "",
	).Replace(line))
}

// welcomeFooter states where the notes actually live — the app's main promise,
// spelled out rather than implied.
func (a *App) welcomeFooter() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetHAlign(gtk.AlignCenter)
	box.AddCSSClass("welcome-footer")

	icon := gtk.NewImageFromIconName("folder-symbolic")
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
