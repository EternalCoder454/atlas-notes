package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/checklist"
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
	orb        *logoOrb
	nameLabel  *gtk.Label
	statusDot  *gtk.Box
	statsLabel *gtk.Label
	answer     *gtk.Label
	askEntry   *gtk.Entry
	sendBtn    *gtk.Button
	menuBtn    *gtk.MenuButton
	actionsPop *gtk.Popover
	editToggle *gtk.ToggleButton

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
	s.orb = newLogoOrb()
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
	s.statusDot.SetSizeRequest(9, 9)
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

	// Setup card (hidden until a probe finds Ollama unreachable / model missing).
	s.widget.Append(s.buildSetupCard())

	s.widget.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// Answer: a Markdown-rendered label that streams in, in a scroller.
	s.answer = gtk.NewLabel("")
	s.answer.SetWrap(true)
	s.answer.SetXAlign(0)
	s.answer.SetYAlign(0)
	s.answer.SetVAlign(gtk.AlignStart)
	s.answer.SetSelectable(true)
	s.answer.AddCSSClass("ai-answer")
	s.setIdleAnswer()
	answerBox := gtk.NewBox(gtk.OrientationVertical, 0)
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
	s.menuBtn.SetIconName("view-list-symbolic")
	s.menuBtn.SetTooltipText("Note actions")
	s.menuBtn.AddCSSClass("flat")
	s.actionsPop = gtk.NewPopover()
	s.menuBtn.SetPopover(s.actionsPop)
	s.rebuildActionsMenu()
	s.editToggle = gtk.NewToggleButton()
	s.editToggle.SetIconName("document-edit-symbolic")
	s.editToggle.SetTooltipText("Edit mode — apply the reply to the note instead of answering")
	s.editToggle.AddCSSClass("flat")
	s.editToggle.ConnectToggled(s.onModeToggled)
	s.askEntry = gtk.NewEntry()
	s.askEntry.SetHExpand(true)
	s.askEntry.SetPlaceholderText(s.placeholder())
	s.askEntry.ConnectActivate(s.onSend)
	s.sendBtn = gtk.NewButtonWithLabel("Send")
	s.sendBtn.AddCSSClass("suggested-action")
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

// SetActions stores the configured note actions and rebuilds the input-bar menu.
func (s *Sidebar) SetActions(actions []storage.AIAction) {
	s.actions = actions
	s.rebuildActionsMenu()
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
			s.sendBtn.SetLabel("Edit")
		} else {
			s.sendBtn.SetLabel("Send")
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
	s.copyBtn = gtk.NewButtonWithLabel("Copy commands")
	s.copyBtn.ConnectClicked(func() {
		s.copyBtn.Clipboard().SetText(s.setupCmd.Text())
		s.copyBtn.SetLabel("Copied")
		coreglib.TimeoutAdd(1200, func() bool { s.copyBtn.SetLabel("Copy commands"); return false })
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

func (s *Sidebar) startStatusPoll() {
	s.probe()
	coreglib.TimeoutSecondsAdd(10, func() bool {
		s.probe()
		return true // keep polling
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
	s.ready = reachable && modelInstalled(models, model)
	s.setStatus(reachable)

	switch {
	case !reachable:
		s.setupTitle.SetText("The assistant needs Ollama")
		s.setupBody.SetText("Atlas Notes couldn't reach Ollama, the local AI runtime that runs the model on your machine. Install it and pull the model — this card clears itself once it's ready.")
		s.setupCmd.SetText("curl -fsSL https://ollama.com/install.sh | sh\nollama pull " + model)
		s.setupCard.SetVisible(true)
	case !s.ready:
		s.setupTitle.SetText("Model not installed")
		s.setupBody.SetText(fmt.Sprintf("Ollama is running, but %q isn't installed yet. Pull it once (a few GB) — this card clears when it's ready.", model))
		s.setupCmd.SetText("ollama pull " + model)
		s.setupCard.SetVisible(true)
	default:
		s.setupCard.SetVisible(false)
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
	s.orb.setActive(busy) // swell + brighten the orb while generating
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

func (s *Sidebar) setAnswerText(text string) { s.answer.SetText(text) }

func (s *Sidebar) setAnswerMarkdown(md string) {
	if strings.TrimSpace(md) == "" {
		s.answer.SetText("")
		return
	}
	s.answer.SetMarkup(markdownToPango(md))
}

// setIdleAnswer shows a faint hint before any question is asked.
func (s *Sidebar) setIdleAnswer() {
	s.answer.SetMarkup(`<span alpha='55%'>Ask a question about this note, run an action from the menu, or turn on edit mode (the pencil) to change the note with an instruction.</span>`)
}

// runStream runs a streaming AI call. When echo is true, tokens append to the
// answer area as they arrive; onResult receives the full text (to Markdown-
// render, or apply to the note).
func (s *Sidebar) runStream(echo bool, call func(context.Context, func(string)) (string, ai.Stats, error), onResult func(full string)) {
	if s.busy {
		return
	}
	s.setBusy(true)
	s.respBuilder.Reset()
	s.streamTokens = 0
	s.streamStart = time.Now()
	if echo {
		s.answer.SetText("")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
		defer cancel()
		full, stats, err := call(ctx, func(tok string) {
			coreglib.IdleAdd(func() bool {
				s.streamTokens++
				if echo {
					s.respBuilder.WriteString(tok)
					s.answer.SetText(s.respBuilder.String())
				}
				s.refreshStats()
				return false
			})
		})
		coreglib.IdleAdd(func() bool {
			s.setBusy(false)
			if err != nil {
				s.setAnswerText("Error: " + err.Error())
			} else {
				s.lastStats = stats
				s.refreshStats()
				onResult(full)
			}
			return false
		})
	}()
}

// runAction dispatches a configured action by its mode.
func (s *Sidebar) runAction(action storage.AIAction) {
	if s.GetContent == nil {
		return
	}
	content := s.GetContent()

	switch action.Mode {
	case storage.ActionModeSort:
		items := checklist.Parse(content)
		if len(items) == 0 {
			s.setAnswerText("No checklist items to sort.")
			return
		}
		s.runSort(content, items, action.Prompt)
	case storage.ActionModeReplace:
		// Don't echo into the answer — the output replaces the note; show a
		// confirmation when it's applied. The live caption still counts tokens.
		s.runStream(false, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
			return s.client.RunAction(ctx, action.Prompt, content, onToken)
		}, func(full string) {
			if s.SetContent != nil {
				s.SetContent(full)
			}
			s.setAnswerText(action.Name + " applied.")
		})
	default: // show
		s.runStream(true, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
			return s.client.RunAction(ctx, action.Prompt, content, onToken)
		}, func(full string) {
			s.setAnswerMarkdown(full)
		})
	}
}

func (s *Sidebar) runAsk() {
	if s.GetContent == nil || s.busy || !s.ready {
		return
	}
	question := strings.TrimSpace(s.askEntry.Buffer().Text())
	if question == "" {
		return
	}
	content := s.GetContent()
	s.askEntry.Buffer().SetText("", -1) // clear after capturing
	s.runStream(true, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
		return s.client.Ask(ctx, content, question, onToken)
	}, func(full string) {
		s.setAnswerMarkdown(full)
	})
}

// runEdit applies a free-form instruction to the current note: the assistant
// returns the full updated note, which replaces the content. The reply isn't
// echoed (it's the note); a confirmation is shown instead.
func (s *Sidebar) runEdit() {
	if s.GetContent == nil || s.SetContent == nil || s.busy || !s.ready {
		return
	}
	instruction := strings.TrimSpace(s.askEntry.Buffer().Text())
	if instruction == "" {
		return
	}
	content := s.GetContent()
	s.askEntry.Buffer().SetText("", -1) // clear after capturing
	s.runStream(false, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
		return s.client.EditNote(ctx, content, instruction, onToken)
	}, func(full string) {
		switch {
		case strings.TrimSpace(full) == "":
			s.setAnswerText("The assistant returned nothing; the note is unchanged.")
		case strings.TrimSpace(full) == strings.TrimSpace(content):
			s.setAnswerText("No change made. To reformat the whole note, use Clean & Format from the menu.")
		default:
			s.SetContent(full)
			s.setAnswerText("Note updated.")
		}
	})
}

func (s *Sidebar) runSort(content string, items []checklist.Item, prompt string) {
	if s.busy {
		return
	}
	s.setBusy(true)
	s.streamTokens = 0
	s.streamStart = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
		defer cancel()
		sorted, stats, err := s.client.SortPriorities(ctx, items, prompt)
		coreglib.IdleAdd(func() bool {
			s.setBusy(false)
			if err != nil {
				s.setAnswerText("Error: " + err.Error())
				return false
			}
			s.lastStats = stats
			s.refreshStats()
			if s.SetContent != nil {
				s.SetContent(applySortedItems(content, sorted))
			}
			s.setAnswerText("Re-prioritised checklist.")
			return false
		})
	}()
}

// applySortedItems rewrites the checklist lines of content in the new order,
// leaving every non-checklist line untouched.
func applySortedItems(content string, sorted []checklist.Item) string {
	lines := strings.Split(content, "\n")
	var slots []int
	for i, line := range lines {
		if _, ok := checklist.ParseLine(line); ok {
			slots = append(slots, i)
		}
	}
	for i, slot := range slots {
		if i < len(sorted) {
			lines[slot] = sorted[i].Marshal()
		}
	}
	return strings.Join(lines, "\n")
}
