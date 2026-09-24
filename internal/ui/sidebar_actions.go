package ui

import (
	"context"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/checklist"
	"atlas-notes/internal/storage"
)

// What the assistant panel actually does: run a call against the local model
// off the main thread, stream its tokens back onto the main thread, and apply
// the result — shown in the panel, written into the note, or used to reorder
// the note's checklist.

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
		// A little temperature helps the model apply Markdown structure (bold,
		// lists) when reformatting; the prompt keeps it faithful.
		s.runStream(false, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
			return s.client.RunAction(ctx, action.Prompt, content, 0.4, onToken)
		}, func(full string) {
			if s.SetContent != nil {
				s.SetContent(full)
			}
			s.setAnswerText(action.Name + " applied.")
		})
	default: // show
		s.runStream(true, func(ctx context.Context, onToken func(string)) (string, ai.Stats, error) {
			return s.client.RunAction(ctx, action.Prompt, content, -1, onToken)
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
