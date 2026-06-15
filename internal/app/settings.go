package app

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// actionRow holds the editable widgets for one AI action in the settings dialog.
type actionRow struct {
	root   *gtk.Box
	name   *gtk.Entry
	prompt *gtk.TextView
	mode   *gtk.DropDown
}

// showSettings opens the settings dialog: a sidebar with a "Model & Prompt"
// section and a "Prompt Shortcuts" section.
func (a *App) showSettings() {
	dialog := adw.NewDialog()
	dialog.SetTitle("Settings")
	dialog.SetContentWidth(680)
	dialog.SetContentHeight(680)

	modelEntry, sysView, summaryToggle, generalPage := a.buildGeneralPage()
	rows, shortcutsPage := a.buildShortcutsPage()

	stack := gtk.NewStack()
	stack.SetHExpand(true)
	stack.SetVExpand(true)
	stack.AddTitled(generalPage, "general", "Model & Prompt")
	stack.AddTitled(shortcutsPage, "shortcuts", "Prompt Shortcuts")
	stack.AddTitled(a.buildAppPage(), "app", "App")

	stackSide := gtk.NewStackSidebar()
	stackSide.SetStack(stack)
	stackSide.SetSizeRequest(170, -1)

	body := gtk.NewBox(gtk.OrientationHorizontal, 0)
	body.Append(stackSide)
	body.Append(gtk.NewSeparator(gtk.OrientationVertical))
	body.Append(stack)

	header := adw.NewHeaderBar()
	saveBtn := gtk.NewButtonWithLabel("Save")
	saveBtn.AddCSSClass("suggested-action")
	saveBtn.ConnectClicked(func() {
		a.applySettings(modelEntry, sysView, summaryToggle, *rows)
		dialog.Close()
	})
	header.PackEnd(saveBtn)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(body)
	dialog.SetChild(tv)
	dialog.Present(a.win)
}

// buildGeneralPage builds the "Model & Prompt" section.
func (a *App) buildGeneralPage() (*gtk.Entry, *gtk.TextView, *gtk.CheckButton, gtk.Widgetter) {
	box := sectionBox()

	modelGroup := groupCard("Ollama model")
	modelEntry := gtk.NewEntry()
	modelEntry.Buffer().SetText(a.cfg.Model, -1)
	modelGroup.Append(modelEntry)
	box.Append(modelGroup)

	promptGroup := groupCard("System prompt")
	sysView, sysFrame := multilineField(a.cfg.SystemPrompt, 6)
	promptGroup.Append(sysFrame)
	box.Append(promptGroup)

	treeGroup := groupCard("Folder tree")
	summary := gtk.NewCheckButtonWithLabel("Show a 1-sentence AI summary on hover")
	summary.SetActive(a.cfg.EnableTreeSummaries)
	treeGroup.Append(summary)
	box.Append(treeGroup)

	return modelEntry, sysView, summary, pageScroll(box)
}

// groupCard returns a titled, card-styled container for a group of settings.
func groupCard(title string) *gtk.Box {
	card := gtk.NewBox(gtk.OrientationVertical, 8)
	card.AddCSSClass("settings-group")
	card.Append(fieldLabel(title))
	return card
}

// buildShortcutsPage builds the editable "Prompt Shortcuts" section. The returned
// slice pointer reflects later add/remove edits.
func (a *App) buildShortcutsPage() (*[]*actionRow, gtk.Widgetter) {
	rows := &[]*actionRow{}

	box := sectionBox()
	hint := gtk.NewLabel("{content} = note text   ·   {items} = checklist (Sort mode)")
	hint.SetXAlign(0)
	hint.AddCSSClass("dim-label")
	box.Append(hint)

	list := gtk.NewBox(gtk.OrientationVertical, 12)
	box.Append(list)

	addRow := func(act storage.AIAction) {
		r := newActionRow(act)
		r.removeBtn().ConnectClicked(func() {
			list.Remove(r.root)
			kept := (*rows)[:0]
			for _, x := range *rows {
				if x != r {
					kept = append(kept, x)
				}
			}
			*rows = kept
		})
		*rows = append(*rows, r)
		list.Append(r.root)
	}
	for _, act := range a.cfg.Actions {
		addRow(act)
	}

	addBtn := gtk.NewButtonWithLabel("Add Action")
	addBtn.ConnectClicked(func() {
		addRow(storage.AIAction{Name: "New Action", Mode: storage.ActionModeShow, Prompt: "{content}"})
	})
	box.Append(addBtn)

	return rows, pageScroll(box)
}

// applySettings reads the dialog widgets into config, persists, and applies the
// changes to the AI client and sidebar.
func (a *App) applySettings(modelEntry *gtk.Entry, sysView *gtk.TextView, summaryToggle *gtk.CheckButton, rows []*actionRow) {
	a.cfg.Model = strings.TrimSpace(modelEntry.Buffer().Text())
	if a.cfg.Model == "" {
		a.cfg.Model = storage.DefaultModel
	}
	a.cfg.SystemPrompt = strings.TrimSpace(textViewText(sysView))
	a.cfg.EnableTreeSummaries = summaryToggle.Active()

	var actions []storage.AIAction
	for _, r := range rows {
		name := strings.TrimSpace(r.name.Buffer().Text())
		if name == "" {
			continue
		}
		actions = append(actions, storage.AIAction{
			Name:   name,
			Prompt: textViewText(r.prompt),
			Mode:   modeFromIndex(r.mode.Selected()),
		})
	}
	a.cfg.Actions = actions

	if err := storage.SaveConfig(a.cfg); err != nil {
		log.Printf("atlas-notes: save settings: %v", err)
	}
	if a.ai != nil {
		a.ai.Model = a.cfg.Model
		a.ai.SystemPrompt = a.cfg.SystemPrompt
	}
	if a.sidebar != nil {
		a.sidebar.SetModel(a.cfg.Model)
		a.sidebar.SetActions(a.cfg.Actions)
	}
	if a.tree != nil {
		a.tree.SetSummariesEnabled(a.cfg.EnableTreeSummaries)
	}
}

func newActionRow(act storage.AIAction) *actionRow {
	r := &actionRow{}
	r.root = gtk.NewBox(gtk.OrientationVertical, 8)
	r.root.AddCSSClass("ai-action")

	top := gtk.NewBox(gtk.OrientationHorizontal, 8)
	r.name = gtk.NewEntry()
	r.name.SetHExpand(true)
	r.name.SetPlaceholderText("Action name")
	r.name.Buffer().SetText(act.Name, -1)
	r.mode = gtk.NewDropDownFromStrings([]string{"Show result", "Replace note", "Sort checklist"})
	r.mode.SetSelected(modeIndex(act.Mode))
	r.mode.SetVAlign(gtk.AlignCenter)
	trash := gtk.NewButtonFromIconName("user-trash-symbolic")
	trash.AddCSSClass("flat")
	trash.SetVAlign(gtk.AlignCenter)
	trash.SetTooltipText("Remove action")
	top.Append(r.name)
	top.Append(r.mode)
	top.Append(trash)
	r.root.Append(top)

	promptLabel := gtk.NewLabel("Prompt")
	promptLabel.SetXAlign(0)
	promptLabel.AddCSSClass("dim-label")
	promptLabel.AddCSSClass("caption")
	r.root.Append(promptLabel)

	promptView, promptFrame := multilineField(act.Prompt, 3)
	r.prompt = promptView
	r.root.Append(promptFrame)
	return r
}

// removeBtn returns the trash button in the row's top bar.
func (r *actionRow) removeBtn() *gtk.Button {
	top, _ := r.root.FirstChild().(*gtk.Box)
	if top == nil {
		return gtk.NewButton()
	}
	for c := top.FirstChild(); c != nil; {
		if b, ok := c.(*gtk.Button); ok {
			return b
		}
		ns, ok := c.(interface{ NextSibling() gtk.Widgetter })
		if !ok {
			break
		}
		c = ns.NextSibling()
	}
	return gtk.NewButton()
}

func modeIndex(mode string) uint {
	switch mode {
	case storage.ActionModeReplace:
		return 1
	case storage.ActionModeSort:
		return 2
	default:
		return 0
	}
}

func modeFromIndex(i uint) string {
	switch i {
	case 1:
		return storage.ActionModeReplace
	case 2:
		return storage.ActionModeSort
	default:
		return storage.ActionModeShow
	}
}

func sectionBox() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(16)
	box.SetMarginBottom(16)
	box.SetMarginStart(16)
	box.SetMarginEnd(16)
	return box
}

func pageScroll(child gtk.Widgetter) *gtk.ScrolledWindow {
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(child)
	scroll.SetVExpand(true)
	scroll.SetHExpand(true)
	return scroll
}

func fieldLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("heading")
	return l
}

// multilineField returns a bordered multi-line text field that grows to fit its
// content (the dialog scrolls), so prompts are never clipped. minLines sets the
// minimum height.
func multilineField(text string, minLines int) (*gtk.TextView, *gtk.Frame) {
	tv := gtk.NewTextView()
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.SetLeftMargin(6)
	tv.SetRightMargin(6)
	tv.SetTopMargin(6)
	tv.SetBottomMargin(6)
	tv.SetSizeRequest(-1, minLines*24)
	tv.Buffer().SetText(text)

	frame := gtk.NewFrame("")
	frame.SetChild(tv)
	return tv, frame
}

func textViewText(tv *gtk.TextView) string {
	b := tv.Buffer()
	start, end := b.Bounds()
	return b.Text(start, end, true)
}
