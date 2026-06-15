package ui

import (
	"context"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/checklist"
	"atlas-notes/internal/storage"
)

// aiTimeout bounds a single AI call. Ollama is local; 60s is plenty plus slack.
const aiTimeout = 65 * time.Second

// Sidebar is the always-visible right-hand AI assistant panel. Every AI call
// runs on a goroutine and marshals its result back to the GTK main thread via
// glib.IdleAdd, so the UI never blocks. Action buttons are built from config
// (including Sort, which is a sort-mode action) and laid out in a FlowBox that
// reflows to two columns when the panel is wide and one when it's narrow.
type Sidebar struct {
	client *ai.Client

	widget      *gtk.Box
	statusDot   *gtk.Box
	modelLabel  *gtk.Label
	actionsFlow *gtk.FlowBox
	actionBtns  []*gtk.Button
	sortBtns    []*gtk.Button
	askEntry    *gtk.Entry
	responseBuf *gtk.TextBuffer
	spinner     *gtk.Spinner

	busy    bool
	actions []storage.AIAction

	// GetContent returns the current note's markdown.
	GetContent func() string
	// SetContent replaces the note's content (used by replace/sort actions).
	SetContent func(string)
}

// NewSidebar builds the AI panel and starts the Ollama status poll.
func NewSidebar(client *ai.Client) *Sidebar {
	s := &Sidebar{client: client}

	s.widget = gtk.NewBox(gtk.OrientationVertical, 8)
	s.widget.AddCSSClass("ai-sidebar")
	s.widget.SetVExpand(true)
	s.widget.SetMarginTop(12)
	s.widget.SetMarginBottom(12)
	s.widget.SetMarginStart(12)
	s.widget.SetMarginEnd(12)

	title := gtk.NewLabel("AI Assistant")
	title.SetXAlign(0)
	title.AddCSSClass("title-4")
	s.widget.Append(title)

	statusRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	s.statusDot = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.statusDot.SetSizeRequest(12, 12)
	s.statusDot.SetVAlign(gtk.AlignCenter)
	s.statusDot.AddCSSClass("status-dot")
	s.statusDot.AddCSSClass("offline")
	s.modelLabel = gtk.NewLabel(client.Model)
	s.modelLabel.SetXAlign(0)
	s.modelLabel.AddCSSClass("dim-label")
	s.spinner = gtk.NewSpinner()
	s.spinner.SetHExpand(true)
	s.spinner.SetHAlign(gtk.AlignEnd)
	statusRow.Append(s.statusDot)
	statusRow.Append(s.modelLabel)
	statusRow.Append(s.spinner)
	s.widget.Append(statusRow)

	s.widget.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	// Action shortcuts: a FlowBox reflows 1↔2 columns with the panel width.
	s.actionsFlow = gtk.NewFlowBox()
	s.actionsFlow.SetSelectionMode(gtk.SelectionNone)
	s.actionsFlow.SetMaxChildrenPerLine(2)
	s.actionsFlow.SetMinChildrenPerLine(1)
	s.actionsFlow.SetColumnSpacing(6)
	s.actionsFlow.SetRowSpacing(6)
	s.actionsFlow.SetHomogeneous(true)
	s.widget.Append(s.actionsFlow)

	s.widget.Append(gtk.NewSeparator(gtk.OrientationHorizontal))

	askLabel := gtk.NewLabel("Ask about this note")
	askLabel.SetXAlign(0)
	s.widget.Append(askLabel)

	s.askEntry = gtk.NewEntry()
	s.askEntry.SetHExpand(true)
	s.askEntry.SetPlaceholderText("Type a question, press Enter…")
	s.askEntry.ConnectActivate(s.runAsk)
	s.widget.Append(s.askEntry)

	responseView := gtk.NewTextView()
	responseView.SetEditable(false) // read-only, but text stays selectable
	responseView.SetCursorVisible(false)
	responseView.SetWrapMode(gtk.WrapWordChar)
	responseView.SetLeftMargin(6)
	responseView.SetRightMargin(6)
	responseView.SetTopMargin(6)
	responseView.SetBottomMargin(6)
	responseView.AddCSSClass("ai-response")
	s.responseBuf = responseView.Buffer()
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(responseView)
	scroll.SetVExpand(true)
	s.widget.Append(scroll)

	s.startStatusPoll()
	s.UpdateState()
	return s
}

// Widget returns the panel's root widget.
func (s *Sidebar) Widget() gtk.Widgetter { return s.widget }

// SetActions rebuilds the action buttons from config.
func (s *Sidebar) SetActions(actions []storage.AIAction) {
	s.actions = actions
	s.actionsFlow.RemoveAll()
	s.actionBtns = nil
	s.sortBtns = nil
	for _, act := range actions {
		b := fullButton(act.Name, func() { s.runAction(act) })
		s.actionBtns = append(s.actionBtns, b)
		if act.Mode == storage.ActionModeSort {
			s.sortBtns = append(s.sortBtns, b)
		}
		s.actionsFlow.Append(b)
	}
	s.UpdateState()
}

// SetModel updates the model label after a settings change.
func (s *Sidebar) SetModel(model string) {
	if s.modelLabel != nil {
		s.modelLabel.SetText(model)
	}
}

// UpdateState enables sort-mode actions only when the note has checklist items
// (and no AI call is in flight).
func (s *Sidebar) UpdateState() {
	hasItems := false
	if s.GetContent != nil {
		hasItems = checklist.HasItems(s.GetContent())
	}
	for _, b := range s.sortBtns {
		b.SetSensitive(hasItems && !s.busy)
	}
}

func (s *Sidebar) startStatusPoll() {
	check := func() {
		go func() {
			online := s.client.Available()
			coreglib.IdleAdd(func() bool {
				s.setStatus(online)
				return false
			})
		}()
	}
	check()
	coreglib.TimeoutSecondsAdd(10, func() bool {
		check()
		return true // keep polling
	})
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
	s.spinner.SetSpinning(busy)
	for _, b := range s.actionBtns {
		b.SetSensitive(!busy)
	}
	s.askEntry.SetSensitive(!busy)
	s.UpdateState() // sort buttons also depend on content
}

func (s *Sidebar) setResponse(text string) { s.responseBuf.SetText(text) }

// runText runs a string-returning AI call off the main thread.
func (s *Sidebar) runText(call func(context.Context) (string, error), onResult func(string)) {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
		defer cancel()
		res, err := call(ctx)
		coreglib.IdleAdd(func() bool {
			s.setBusy(false)
			if err != nil {
				s.setResponse("Error: " + err.Error())
			} else {
				onResult(res)
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
			return
		}
		s.runSort(content, items, action.Prompt)
	case storage.ActionModeReplace:
		s.runText(func(ctx context.Context) (string, error) {
			return s.client.RunAction(ctx, action.Prompt, content)
		}, func(res string) {
			if s.SetContent != nil {
				s.SetContent(res)
			}
			s.setResponse(action.Name + " applied.")
		})
	default: // show
		s.runText(func(ctx context.Context) (string, error) {
			return s.client.RunAction(ctx, action.Prompt, content)
		}, s.setResponse)
	}
}

func (s *Sidebar) runAsk() {
	if s.GetContent == nil {
		return
	}
	question := strings.TrimSpace(s.askEntry.Buffer().Text())
	if question == "" {
		return
	}
	content := s.GetContent()
	s.runText(func(ctx context.Context) (string, error) {
		return s.client.Ask(ctx, content, question)
	}, s.setResponse)
}

func (s *Sidebar) runSort(content string, items []checklist.Item, prompt string) {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
		defer cancel()
		sorted, err := s.client.SortPriorities(ctx, items, prompt)
		coreglib.IdleAdd(func() bool {
			s.setBusy(false)
			if err != nil {
				s.setResponse("Error: " + err.Error())
				return false
			}
			if s.SetContent != nil {
				s.SetContent(applySortedItems(content, sorted))
			}
			s.setResponse("Re-prioritised checklist.")
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

func fullButton(label string, onClick func()) *gtk.Button {
	b := gtk.NewButtonWithLabel(label)
	b.SetHExpand(true)
	b.ConnectClicked(onClick)
	return b
}
