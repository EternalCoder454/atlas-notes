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

// showSettings opens the settings dialog: a sidebar with "Model & Prompt",
// "Prompt Shortcuts" and "App" sections.
func (a *App) showSettings() { a.showSettingsPage("") }

// showSettingsPage opens it on a named section. Everything but the screenshot
// tooling passes an empty string and gets the first.
func (a *App) showSettingsPage(page string) {
	dialog := adw.NewDialog()
	dialog.SetTitle("Settings")
	dialog.SetContentWidth(680)
	dialog.SetContentHeight(680)

	fields := a.buildGeneralPage()
	rows, shortcutsPage := a.buildShortcutsPage()

	stack := gtk.NewStack()
	stack.SetHExpand(true)
	stack.SetVExpand(true)
	stack.AddTitled(fields.page, "general", "Model & Prompt")
	stack.AddTitled(shortcutsPage, "shortcuts", "Prompt Shortcuts")
	stack.AddTitled(a.buildAppPage(), "app", "App")

	if page != "" {
		stack.SetVisibleChildName(page)
	}

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
		a.applySettings(fields, *rows)
		dialog.Close()
	})
	header.PackEnd(saveBtn)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(body)
	dialog.SetChild(tv)
	dialog.Present(a.win)
}

// generalFields holds the editable widgets of the "Model & Prompt" section.
type generalFields struct {
	name    *gtk.Entry
	model   *gtk.Entry
	system  *gtk.TextView
	summary *gtk.CheckButton
	toolbar *gtk.CheckButton
	fonts   *gtk.DropDown
	page    gtk.Widgetter
}

// fontRenderingModes are the dropdown entries, in the order they appear.
var fontRenderingModes = []string{
	storage.FontRenderingAuto, storage.FontRenderingCrisp, storage.FontRenderingSmooth,
}

// buildGeneralPage builds the "Model & Prompt" section.
func (a *App) buildGeneralPage() generalFields {
	box := sectionBox()

	nameGroup := groupCard("Assistant name")
	nameEntry := gtk.NewEntry()
	nameEntry.Buffer().SetText(a.cfg.AssistantName, -1)
	nameGroup.Append(nameEntry)
	box.Append(nameGroup)

	modelGroup := groupCard("Ollama model")
	modelEntry := gtk.NewEntry()
	modelEntry.Buffer().SetText(a.cfg.Model, -1)
	modelGroup.Append(modelEntry)
	box.Append(modelGroup)

	promptGroup := groupCard("System prompt")
	sysView, sysFrame := multilineField(a.cfg.SystemPrompt, 6)
	promptGroup.Append(sysFrame)
	box.Append(promptGroup)

	treeGroup := groupCard("Hover previews")
	summary := wrappingCheck("Show a 1-sentence AI summary when you hover a note")
	summary.SetActive(a.cfg.EnableTreeSummaries)
	treeGroup.Append(summary)
	box.Append(treeGroup)

	editorGroup := groupCard("Editor")
	toolbar := wrappingCheck("Show the formatting toolbar above notes")
	toolbar.SetActive(a.cfg.ShowFormatBar)
	toolbar.SetTooltipText("The toolbar shows what the editor understands. " +
		"Every command it offers also has a keyboard shortcut.")
	editorGroup.Append(toolbar)
	box.Append(editorGroup)

	// Text rendering: the right choice depends on the screen, so it is a
	// setting rather than a guess. See internal/app/fonts.go.
	fontGroup := groupCard("Text rendering")
	fonts := gtk.NewDropDownFromStrings([]string{
		"Automatic (recommended)",
		"Crisp — best on 1080p",
		"Smooth — best on HiDPI",
	})
	fonts.SetSelected(uint(fontModeIndex(a.cfg.FontRendering)))
	fontGroup.Append(fonts)
	fontHint := gtk.NewLabel("Automatic picks per screen. Takes effect on the next launch.")
	fontHint.SetXAlign(0)
	fontHint.SetWrap(true)
	fontHint.AddCSSClass("dim-label")
	fontHint.AddCSSClass("caption")
	fontGroup.Append(fontHint)
	box.Append(fontGroup)

	return generalFields{
		name: nameEntry, model: modelEntry, system: sysView,
		summary: summary, toolbar: toolbar, fonts: fonts, page: pageScroll(box),
	}
}

// fontModeIndex maps a stored font-rendering mode to its dropdown position.
func fontModeIndex(mode string) int {
	for i, m := range fontRenderingModes {
		if m == mode {
			return i
		}
	}
	return 0
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
func (a *App) applySettings(f generalFields, rows []*actionRow) {
	a.cfg.AssistantName = strings.TrimSpace(f.name.Buffer().Text())
	if a.cfg.AssistantName == "" {
		a.cfg.AssistantName = storage.DefaultAssistantName
	}
	a.cfg.Model = strings.TrimSpace(f.model.Buffer().Text())
	if a.cfg.Model == "" {
		a.cfg.Model = storage.DefaultModel
	}
	a.cfg.SystemPrompt = strings.TrimSpace(textViewText(f.system))
	a.cfg.EnableTreeSummaries = f.summary.Active()
	a.cfg.ShowFormatBar = f.toolbar.Active()
	if i := int(f.fonts.Selected()); i >= 0 && i < len(fontRenderingModes) {
		a.cfg.FontRendering = fontRenderingModes[i]
	}

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
		a.sidebar.SetName(a.cfg.AssistantName)
		a.sidebar.SetModel(a.cfg.Model)
		a.sidebar.SetActions(a.cfg.Actions)
	}
	if a.tree != nil {
		a.tree.SetSummariesEnabled(a.cfg.EnableTreeSummaries)
	}
	a.applyFormatBarVisibility()
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
	trash := gtk.NewButtonFromIconName("atlas-trash-symbolic")
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

// pageScroll wraps a settings page so it scrolls vertically only. Horizontal
// scrolling is switched off deliberately: with it on, the page is as wide as
// its widest unwrappable label, and anything past the dialog's edge is simply
// cut off rather than reachable. Held to the viewport's width, labels that can
// wrap do, and the page fits.
func pageScroll(child gtk.Widgetter) *gtk.ScrolledWindow {
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(child)
	scroll.SetVExpand(true)
	scroll.SetHExpand(true)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	return scroll
}

// wrappingCheck is a check button whose label wraps. The stock one's label
// does not, so a sentence long enough to explain the setting sets a minimum
// width for the whole page.
func wrappingCheck(text string) *gtk.CheckButton {
	check := gtk.NewCheckButton()
	label := gtk.NewLabel(text)
	label.SetWrap(true)
	label.SetXAlign(0)
	check.SetChild(label)
	return check
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
