package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/storage"
)

// aiTimeout bounds a single (streaming) AI call. Ollama is local but a long
// reply on a small machine can take a while.
const aiTimeout = 3 * time.Minute

// Sidebar is the always-visible right-hand AI assistant panel, modeled on the
// Atlas Monitor assistant: an animated orb, the (editable) name, a model /
// throughput caption, a Markdown-rendered answer that streams in, and an input
// bar whose menu runs the configured note actions. A setup card appears when
// Ollama is unreachable or the model isn't installed. Every AI call runs on a
// goroutine and marshals back to the GTK main thread via glib.IdleAdd.
type Sidebar struct {
	client *ai.Client

	widget     *gtk.Box
	orb        *Orb
	nameLabel  *gtk.Label
	statusDot  *gtk.Box
	statsLabel *gtk.Label
	answer     *gtk.Label
	askEntry   *gtk.Entry
	sendBtn    *gtk.Button
	menuBtn    *gtk.MenuButton
	actionsPop *gtk.Popover
	editToggle *gtk.ToggleButton

	suggestions *gtk.FlowBox

	setupSlot  *gtk.Box
	setupCard  *gtk.Box
	setupTitle *gtk.Label
	setupBody  *gtk.Label
	setupCmd   *gtk.Label
	copyBtn    *gtk.Button

	model   string
	name    string
	actions []storage.AIAction

	busy         bool
	ready        bool
	probing      bool
	pollSeconds  int
	streamStart  time.Time
	streamTokens int
	respBuilder  strings.Builder
	lastStats    ai.Stats

	// GetContent returns the current note's markdown.
	GetContent func() string
	// SetContent replaces the note's content (used by replace/sort actions).
	SetContent func(string)
}

// NewSidebar builds the AI panel and starts the Ollama readiness poll.
func NewSidebar(client *ai.Client) *Sidebar {
	s := &Sidebar{client: client, model: client.Model, name: storage.DefaultAssistantName, ready: true}

	s.widget = gtk.NewBox(gtk.OrientationVertical, 10)
	s.widget.AddCSSClass("ai-sidebar")
	s.widget.SetVExpand(true)
	s.widget.SetMarginTop(16)
	s.widget.SetMarginBottom(12)
	s.widget.SetMarginStart(12)
	s.widget.SetMarginEnd(12)

	// Animated avatar orb (Cairo; eases between idle and generating).
	s.orb = NewOrb(86)
	s.widget.Append(s.orb)

	// Name (editable via Settings).
	s.nameLabel = gtk.NewLabel(s.name)
	s.nameLabel.SetHAlign(gtk.AlignCenter)
	s.nameLabel.AddCSSClass("assistant-name")
	s.widget.Append(s.nameLabel)

	// Status dot + model/throughput caption, centered.
	statsRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	statsRow.SetHAlign(gtk.AlignCenter)
	s.statusDot = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.statusDot.SetSizeRequest(8, 8)
	s.statusDot.SetVAlign(gtk.AlignCenter)
	s.statusDot.AddCSSClass("status-dot")
	s.statusDot.AddCSSClass("offline")
	s.statsLabel = gtk.NewLabel(s.model)
	s.statsLabel.AddCSSClass("assistant-stats")
	s.statsLabel.SetWrap(true)
	s.statsLabel.SetJustify(gtk.JustifyCenter)
	statsRow.Append(s.statusDot)
	statsRow.Append(s.statsLabel)
	s.widget.Append(statsRow)

	// The setup card is built on demand: it only appears when a probe finds
	// Ollama unreachable or the model missing.
	s.setupSlot = gtk.NewBox(gtk.OrientationVertical, 0)
	s.widget.Append(s.setupSlot)

	s.widget.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// Answer: a Markdown-rendered label that streams in, in a scroller.
	s.answer = gtk.NewLabel("")
	s.answer.SetWrap(true)
	s.answer.SetXAlign(0)
	s.answer.SetYAlign(0)
	s.answer.SetVAlign(gtk.AlignStart)
	s.answer.SetSelectable(true)
	s.answer.SetCanFocus(false) // selectable text, but it must not steal focus
	s.answer.AddCSSClass("ai-answer")
	s.setIdleAnswer()
	// The chips come first. They are the actionable half of an idle panel, and
	// when the setup card is taking room there is only so much scroller left —
	// better to cut the sentence explaining the panel than the buttons that
	// use it. They hide themselves as soon as there is a real answer.
	answerBox := gtk.NewBox(gtk.OrientationVertical, 10)
	answerBox.Append(s.buildSuggestions())
	answerBox.Append(s.answer)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(answerBox)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	s.widget.Append(scroll)

	// Input bar: actions menu · question entry · send.
	bar := gtk.NewBox(gtk.OrientationHorizontal, 6)
	bar.AddCSSClass("ai-input-bar")
	s.menuBtn = gtk.NewMenuButton()
	s.menuBtn.SetIconName("atlas-prompts-symbolic")
	s.menuBtn.SetTooltipText("Note actions")
	s.menuBtn.AddCSSClass("flat")
	// The menu's contents are built the first time it is opened: at startup
	// every widget costs layout and paint time for something most launches
	// never show.
	s.menuBtn.SetCreatePopupFunc(func(*gtk.MenuButton) {
		if s.actionsPop == nil {
			s.actionsPop = gtk.NewPopover()
			s.menuBtn.SetPopover(s.actionsPop)
		}
		s.rebuildActionsMenu()
	})
	s.editToggle = gtk.NewToggleButton()
	s.editToggle.SetIconName("atlas-edit-symbolic")
	s.editToggle.SetTooltipText("Edit mode — apply the reply to the note instead of answering")
	s.editToggle.AddCSSClass("flat")
	s.editToggle.ConnectToggled(s.onModeToggled)
	s.askEntry = gtk.NewEntry()
	s.askEntry.SetHExpand(true)
	s.askEntry.SetPlaceholderText(s.placeholder())
	s.askEntry.ConnectActivate(s.onSend)
	s.sendBtn = gtk.NewButtonFromIconName("atlas-send-symbolic")
	s.sendBtn.AddCSSClass("suggested-action")
	s.sendBtn.AddCSSClass("circular")
	s.sendBtn.SetTooltipText("Send (Enter)")
	s.sendBtn.ConnectClicked(s.onSend)
	bar.Append(s.menuBtn)
	bar.Append(s.editToggle)
	bar.Append(s.askEntry)
	bar.Append(s.sendBtn)
	s.widget.Append(bar)

	s.refreshStats()
	s.startStatusPoll()
	return s
}

// Widget returns the panel's root widget.
func (s *Sidebar) Widget() gtk.Widgetter { return s.widget }

// SetActions stores the configured note actions. The menu itself is rebuilt
// the next time it is opened.
func (s *Sidebar) SetActions(actions []storage.AIAction) {
	s.actions = actions
	if s.actionsPop != nil {
		s.rebuildActionsMenu()
	}
}

// SetModel updates the model shown in the caption after a settings change.
func (s *Sidebar) SetModel(model string) {
	s.model = model
	s.refreshStats()
}

// SetName updates the assistant's display name (and the input placeholder).
func (s *Sidebar) SetName(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = storage.DefaultAssistantName
	}
	s.name = name
	if s.nameLabel != nil {
		s.nameLabel.SetText(name)
	}
	if s.askEntry != nil {
		s.askEntry.SetPlaceholderText(s.placeholder())
	}
}

// placeholder is the input hint, which reflects Ask vs Edit mode.
func (s *Sidebar) placeholder() string {
	if s.editToggle != nil && s.editToggle.Active() {
		return "Tell " + s.name + " how to edit this note…"
	}
	return "Ask " + s.name + " about this note…"
}

// onModeToggled updates the input hint and Send label for Ask vs Edit mode.
func (s *Sidebar) onModeToggled() {
	if s.askEntry != nil {
		s.askEntry.SetPlaceholderText(s.placeholder())
	}
	if s.sendBtn != nil {
		if s.editToggle.Active() {
			s.sendBtn.SetTooltipText("Apply this instruction to the note")
			s.sendBtn.SetIconName("atlas-edit-symbolic")
		} else {
			s.sendBtn.SetTooltipText("Send (Enter)")
			s.sendBtn.SetIconName("atlas-send-symbolic")
		}
	}
}

// onSend routes the entry: a question in Ask mode, a note edit in Edit mode.
func (s *Sidebar) onSend() {
	if s.editToggle != nil && s.editToggle.Active() {
		s.runEdit()
	} else {
		s.runAsk()
	}
}

// rebuildActionsMenu fills the actions popover with one entry per configured
// action. Sort entries stay enabled — runAction reports when there's nothing to
// sort.
func (s *Sidebar) rebuildActionsMenu() {
	if s.actionsPop == nil {
		return
	}
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)
	if len(s.actions) == 0 {
		empty := gtk.NewLabel("No actions configured")
		empty.AddCSSClass("dim-label")
		box.Append(empty)
	}
	for _, act := range s.actions {
		act := act
		b := gtk.NewButtonWithLabel(act.Name)
		b.AddCSSClass("flat")
		b.SetHAlign(gtk.AlignFill)
		if l, ok := b.Child().(*gtk.Label); ok {
			l.SetXAlign(0)
		}
		b.ConnectClicked(func() {
			s.menuBtn.Popdown()
			s.runAction(act)
		})
		box.Append(b)
	}
	s.actionsPop.SetChild(box)
}

// buildSetupCard constructs the (hidden) panel that tells a fresh user how to get
// Ollama / the model running, filled in by onProbe.
func (s *Sidebar) buildSetupCard() *gtk.Box {
	card := gtk.NewBox(gtk.OrientationVertical, 6)
	card.AddCSSClass("ai-setup-card")
	card.SetVisible(false)

	s.setupTitle = gtk.NewLabel("")
	s.setupTitle.AddCSSClass("ai-setup-title")
	s.setupTitle.SetXAlign(0)
	s.setupTitle.SetWrap(true)
	card.Append(s.setupTitle)

	s.setupBody = gtk.NewLabel("")
	s.setupBody.AddCSSClass("dim-label")
	s.setupBody.SetXAlign(0)
	s.setupBody.SetWrap(true)
	card.Append(s.setupBody)

	s.setupCmd = gtk.NewLabel("")
	s.setupCmd.AddCSSClass("ai-setup-cmd")
	s.setupCmd.SetXAlign(0)
	s.setupCmd.SetWrap(true)
	s.setupCmd.SetSelectable(true)
	card.Append(s.setupCmd)

	btnRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	s.copyBtn = gtk.NewButton()
	s.copyBtn.SetChild(labelledIcon("atlas-copy-symbolic", "Copy commands"))
	s.copyBtn.ConnectClicked(func() {
		s.copyBtn.Clipboard().SetText(s.setupCmd.Text())
		s.copyBtn.SetChild(labelledIcon("atlas-copy-symbolic", "Copied"))
		coreglib.TimeoutAdd(1200, func() bool {
			s.copyBtn.SetChild(labelledIcon("atlas-copy-symbolic", "Copy commands"))
			return false
		})
	})
	recheck := gtk.NewButtonWithLabel("Recheck")
	recheck.ConnectClicked(func() {
		if !s.probing {
			s.setupBody.SetText("Checking for Ollama…")
			s.probe()
		}
	})
	btnRow.Append(s.copyBtn)
	btnRow.Append(recheck)
	card.Append(btnRow)

	s.setupCard = card
	return card
}

// Ollama readiness polling. The app is useful without Ollama, so an offline
// server must not cost anything: the interval backs off from 10s to a minute
// while nothing is there, and snaps back as soon as the server answers. A
// hidden panel isn't polled at all.
const (
	pollMinSeconds = 10
	pollMaxSeconds = 60
)

func (s *Sidebar) startStatusPoll() {
	s.pollSeconds = pollMinSeconds
	s.probe()
	s.schedulePoll()
}

// schedulePoll arms the next readiness check at the current interval.
func (s *Sidebar) schedulePoll() {
	interval := s.pollSeconds
	coreglib.TimeoutSecondsAdd(uint(interval), func() bool {
		if s.widget != nil && !s.widget.Mapped() {
			// The assistant panel is hidden; there is nothing to update.
			s.schedulePoll()
			return false
		}
		s.probe()
		if interval != s.pollSeconds {
			s.schedulePoll() // the interval changed: re-arm at the new one
			return false
		}
		return true
	})
}

// probe checks Ollama readiness (reachable + model installed) off the main
// thread, then applies the result back on it.
func (s *Sidebar) probe() {
	if s.busy || s.probing {
		return
	}
	s.probing = true
	model := s.client.Model
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		models, err := s.client.Tags(ctx)
		coreglib.IdleAdd(func() bool {
			s.onProbe(model, models, err)
			return false
		})
	}()
}

func (s *Sidebar) onProbe(model string, models []string, err error) {
	s.probing = false
	reachable := err == nil
	if (!reachable || !modelInstalled(models, model)) && s.setupCard == nil {
		s.setupSlot.Append(s.buildSetupCard())
	}
	if reachable {
		s.pollSeconds = pollMinSeconds
	} else if s.pollSeconds < pollMaxSeconds {
		s.pollSeconds *= 2 // nothing there: check progressively less often
		if s.pollSeconds > pollMaxSeconds {
			s.pollSeconds = pollMaxSeconds
		}
	}
	s.ready = reachable && modelInstalled(models, model)
	s.setStatus(reachable)

	switch {
	case !reachable:
		s.setupTitle.SetText("Ollama required")
		s.setupBody.SetText("Run Ollama locally to use the assistant. Install it and pull " +
			model + " — this card clears once it's ready.")
		s.setupCmd.SetText("curl -fsSL https://ollama.com/install.sh | sh\nollama pull " + model)
		s.setupCard.SetVisible(true)
	case !s.ready:
		s.setupTitle.SetText("Model required")
		s.setupBody.SetText(fmt.Sprintf("Ollama is running, but %s isn't installed. Pull it once — "+
			"a few GB — and this card clears.", model))
		s.setupCmd.SetText("ollama pull " + model)
		s.setupCard.SetVisible(true)
	default:
		if s.setupCard != nil {
			s.setupCard.SetVisible(false)
		}
	}

	if !s.busy {
		s.askEntry.SetSensitive(s.ready)
		s.sendBtn.SetSensitive(s.ready)
		s.menuBtn.SetSensitive(s.ready)
		s.editToggle.SetSensitive(s.ready)
	}
}

// modelInstalled reports whether want is among the installed tags, tolerating a
// missing/explicit ":latest" suffix.
func modelInstalled(models []string, want string) bool {
	want = strings.TrimSuffix(want, ":latest")
	for _, m := range models {
		if strings.TrimSuffix(m, ":latest") == want {
			return true
		}
	}
	return false
}

func (s *Sidebar) setStatus(online bool) {
	if online {
		s.statusDot.RemoveCSSClass("offline")
		s.statusDot.AddCSSClass("online")
	} else {
		s.statusDot.RemoveCSSClass("online")
		s.statusDot.AddCSSClass("offline")
	}
}

func (s *Sidebar) setBusy(busy bool) {
	s.busy = busy
	s.orb.SetActive(busy) // swell + brighten the orb while generating
	enabled := !busy && s.ready
	s.askEntry.SetSensitive(enabled)
	s.sendBtn.SetSensitive(enabled)
	s.menuBtn.SetSensitive(enabled)
	s.editToggle.SetSensitive(enabled)
	s.refreshStats()
}

// refreshStats updates the caption under the name: live tokens/sec while
// streaming, "thinking…" before the first token, the final throughput after, or
// just the model when idle.
func (s *Sidebar) refreshStats() {
	if s.statsLabel == nil {
		return
	}
	switch {
	case s.busy && s.streamTokens > 0:
		rate := 0.0
		if el := time.Since(s.streamStart).Seconds(); el > 0 {
			rate = float64(s.streamTokens) / el
		}
		s.statsLabel.SetText(fmt.Sprintf("%s · generating… %d tok, %.0f tok/s", s.model, s.streamTokens, rate))
	case s.busy:
		s.statsLabel.SetText(s.model + " · thinking…")
	case s.lastStats.Tokens > 0:
		s.statsLabel.SetText(s.model + " · " + s.lastStats.Summary())
	default:
		s.statsLabel.SetText(s.model)
	}
}

func (s *Sidebar) setAnswerText(text string) {
	s.answer.SetText(text)
	s.setSuggestionsVisible(false)
}

func (s *Sidebar) setAnswerMarkdown(md string) {
	s.setSuggestionsVisible(false)
	if strings.TrimSpace(md) == "" {
		s.answer.SetText("")
		return
	}
	s.answer.SetMarkup(markdownToPango(md))
}

// setIdleAnswer shows a faint hint before any question is asked.
func (s *Sidebar) setIdleAnswer() {
	s.answer.SetMarkup(`<span alpha='55%'>Ask anything about the note you have open. Nothing is sent anywhere — the model runs on this machine.</span>`)
}

// buildSuggestions offers one-tap prompts, so the assistant shows what it can
// do instead of waiting behind an empty text field.
func (s *Sidebar) buildSuggestions() *gtk.FlowBox {
	s.suggestions = gtk.NewFlowBox()
	s.suggestions.SetSelectionMode(gtk.SelectionNone)
	s.suggestions.SetColumnSpacing(6)
	s.suggestions.SetRowSpacing(6)
	s.suggestions.SetMaxChildrenPerLine(2)
	s.suggestions.SetHomogeneous(false)
	s.suggestions.AddCSSClass("ai-suggestions")
	// The chip says less than it asks. A chip wide enough to hold the whole
	// question wraps onto two lines in a panel this narrow, and four of those
	// stack into a wall; two words fit side by side. The question the model
	// actually gets is unchanged.
	for _, sug := range []struct{ label, prompt string }{
		{"Summarise", "Summarise this note"},
		{"Open tasks", "What are the open tasks?"},
		{"Better title", "Suggest a better title"},
		{"Explain simply", "Explain this to a beginner"},
	} {
		p := sug.prompt
		chip := gtk.NewButtonWithLabel(sug.label)
		chip.AddCSSClass("ai-chip")
		chip.SetTooltipText(p)
		chip.ConnectClicked(func() {
			if s.askEntry == nil {
				return
			}
			s.askEntry.Buffer().SetText(p, -1)
			s.runAsk()
		})
		s.suggestions.Append(chip)
	}
	return s.suggestions
}

// FocusInput puts the caret in the prompt field (Ctrl+L).
func (s *Sidebar) FocusInput() {
	if s.askEntry != nil {
		s.askEntry.GrabFocus()
	}
}

// setSuggestionsVisible hides the starter chips once there is a real answer on
// screen, and brings them back when the panel returns to idle.
func (s *Sidebar) setSuggestionsVisible(v bool) {
	if s.suggestions != nil {
		s.suggestions.SetVisible(v)
	}
}

// labelledIcon is a button's child: an icon and its word, which GTK has no
// single widget for.
func labelledIcon(icon, text string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.SetHAlign(gtk.AlignCenter)
	box.Append(gtk.NewImageFromIconName(icon))
	box.Append(gtk.NewLabel(text))
	return box
}
