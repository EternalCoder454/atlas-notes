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
		{"object-select-symbolic", "New checklist", "A note that starts with tasks", "Ctrl+T", a.actionNewChecklist},
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

	if recents := a.recentRows(); recents != nil {
		content.Append(recents)
	}

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

// recentRows lists the notes touched most recently, or nil in a fresh vault.
func (a *App) recentRows() *gtk.Box {
	if a.store == nil {
		return nil
	}
	notes, err := a.store.RecentNotes(4)
	if err != nil || len(notes) == 0 {
		return nil
	}
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.AddCSSClass("welcome-recents")

	heading := gtk.NewLabel("Recent")
	heading.SetXAlign(0)
	heading.AddCSSClass("welcome-section")
	box.Append(heading)

	for _, n := range notes {
		note := n
		row := gtk.NewButton()
		row.AddCSSClass("welcome-recent")
		row.ConnectClicked(func() { a.openNote(note.Path) })

		line := gtk.NewBox(gtk.OrientationHorizontal, 8)
		icon := gtk.NewImageFromIconName("text-x-generic-symbolic")
		icon.AddCSSClass("dim-label")
		line.Append(icon)

		name := gtk.NewLabel(path.Base(note.Path))
		name.SetXAlign(0)
		name.SetHExpand(true)
		name.SetEllipsize(3) // PANGO_ELLIPSIZE_END
		line.Append(name)

		when := gtk.NewLabel(relativeTime(note.ModifiedAt))
		when.AddCSSClass("welcome-recent-time")
		line.Append(when)

		row.SetChild(line)
		box.Append(row)
	}
	return box
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
			a.tree.Refresh()
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
