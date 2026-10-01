// Package ai is a thin client for a local Ollama server. Every call is blocking
// and bounded by a 60s timeout; the UI layer is responsible for running these on
// a goroutine and marshalling results back to the GTK main thread.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"atlas-notes/internal/checklist"
)

const (
	DefaultBaseURL = "http://localhost:11434"
	DefaultModel   = "qwen3.5:9b"

	// requestTimeout bounds an entire streaming request (including reading the
	// streamed body), so it is generous; the UI also bounds each call with its
	// own context deadline.
	requestTimeout = 3 * time.Minute

	// DefaultSystemPrompt is used when a client has no configured system prompt.
	// It is the same as Settings' default (storage.DefaultSystemPrompt).
	DefaultSystemPrompt = "You are the assistant inside Atlas Notes, a note-taking app, helping the user with the note they have open. Read the whole note before you answer. When you say what the note says, stick to what it actually contains, and never make up facts, numbers, quotes or sources. When the user asks you to write something from the note, such as titles, a summary, an explanation or a rewrite, write it: new wording drawn from the note is what they want. Answer directly, with no preamble and no sign-off."
)

// Client talks to a local Ollama server. The model is configurable so the user
// can change it in config; BaseURL is exposed mainly for tests.
type Client struct {
	BaseURL      string
	Model        string
	SystemPrompt string
	http         *http.Client
}

// NewClient returns a Client for the given model and system prompt (each falls
// back to a default when empty).
func NewClient(model, systemPrompt string) *Client {
	if model == "" {
		model = DefaultModel
	}
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}
	return &Client{
		BaseURL:      DefaultBaseURL,
		Model:        model,
		SystemPrompt: systemPrompt,
		http:         &http.Client{Timeout: requestTimeout},
	}
}

type tagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// Tags returns the model names installed on the Ollama server. A nil error means
// the server is reachable (the slice may be empty if nothing is pulled), letting
// the UI tell "Ollama down" from "model not installed".
func (c *Client) Tags(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Ollama at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned %s", resp.Status)
	}
	var tr tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tr.Models))
	for _, m := range tr.Models {
		switch {
		case m.Name != "":
			names = append(names, m.Name)
		case m.Model != "":
			names = append(names, m.Model)
		}
	}
	return names, nil
}

type generateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	System  string         `json:"system"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

type generateResponse struct {
	Response      string `json:"response"`
	Done          bool   `json:"done"`
	Error         string `json:"error,omitempty"`
	EvalCount     int    `json:"eval_count"`     // tokens generated
	EvalDuration  int64  `json:"eval_duration"`  // generation time, ns
	TotalDuration int64  `json:"total_duration"` // total time, ns
}

// Stats summarizes a generation's throughput, for display in the UI.
type Stats struct {
	Tokens    int           // tokens generated
	Elapsed   time.Duration // total wall-clock time
	TokPerSec float64       // generation speed
}

// Summary renders the stats like "141.7 tok/s · 363 tokens · 3.9s", or "" when
// the server reported no token counts.
func (s Stats) Summary() string {
	if s.Tokens == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f tok/s · %d tokens · %.1fs", s.TokPerSec, s.Tokens, s.Elapsed.Seconds())
}

func statsFrom(gr generateResponse) Stats {
	st := Stats{Tokens: gr.EvalCount, Elapsed: time.Duration(gr.TotalDuration)}
	if gr.EvalDuration > 0 {
		st.TokPerSec = float64(gr.EvalCount) / (float64(gr.EvalDuration) / 1e9)
	}
	return st
}

// generate performs a streaming /api/generate call. onToken (if non-nil) is
// invoked on this goroutine with each chunk as it arrives. It returns the full
// response text and the throughput stats from Ollama's final chunk.
func (c *Client) generate(ctx context.Context, prompt string, temperature float64, onToken func(string)) (string, Stats, error) {
	sys := c.SystemPrompt
	if sys == "" {
		sys = DefaultSystemPrompt
	}
	reqBody := generateRequest{
		Model:  c.Model,
		Prompt: prompt,
		System: sys,
		Stream: true,
	}
	// temperature < 0 uses the model's default; >= 0 is sent explicitly
	// (0 = greedy/deterministic, which keeps note edits faithful).
	if temperature >= 0 {
		reqBody.Options = map[string]any{"temperature": temperature}
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", Stats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", Stats{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Stats{}, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", Stats{}, fmt.Errorf("ollama returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	var stats Stats
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var gr generateResponse
		if err := json.Unmarshal(line, &gr); err != nil {
			continue
		}
		if gr.Error != "" {
			return full.String(), Stats{}, fmt.Errorf("ollama: %s", gr.Error)
		}
		if gr.Response != "" {
			full.WriteString(gr.Response)
			if onToken != nil {
				onToken(gr.Response)
			}
		}
		if gr.Done {
			stats = statsFrom(gr)
			break
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), stats, err
	}
	return strings.TrimSpace(full.String()), stats, nil
}

// RunAction runs a user-defined action: it substitutes {content} in the prompt
// template with the note text and streams the model's response.
func (c *Client) RunAction(ctx context.Context, promptTemplate, content string, temperature float64, onToken func(string)) (string, Stats, error) {
	return c.generate(ctx, fillTemplate(promptTemplate, content), temperature, onToken)
}

// fillTemplate puts the note into a shortcut's prompt. A prompt written
// without {content}, such as "Translate this into French", still means this
// note, so the note goes after it rather than nowhere.
func fillTemplate(promptTemplate, content string) string {
	if !strings.Contains(promptTemplate, "{content}") {
		return strings.TrimRight(promptTemplate, "\n") + "\n\nNote:\n" + content
	}
	return strings.ReplaceAll(promptTemplate, "{content}", content)
}

// askRules say how to take a request about the note. A request to write
// something (three titles, a summary) is not a question the note answers,
// and the rules say so: told only to answer from the note, a small model
// replied that the note contained no better title.
const askRules = `Read the whole note, then do what the request asks.
- If it asks you to write something from the note, such as titles, a summary, an explanation, a list, questions, ideas or a rewrite, write it from the note's content. Never reply that the note does not contain it: you are the one writing it.
- If it asks what the note says about something, answer from the note. Only when the note really does not cover it, say so in one sentence.
- You may use general knowledge to explain terms or ideas the note mentions, but do not present it as something the note says.
Reply in Markdown, as briefly as the request allows.`

// Ask does what the user asks about the open note: answers a question from it,
// or writes something from it. title is the note's name, which is not part of
// its text.
func (c *Client) Ask(ctx context.Context, title, content, request string, onToken func(string)) (string, Stats, error) {
	return c.generate(ctx, askPrompt(title, content, request), -1, onToken)
}

// askPrompt is the note, then the rules, then the request: last, where a small
// model is least likely to lose it behind a long note.
func askPrompt(title, content, request string) string {
	var b strings.Builder
	b.WriteString("The user has this note open in Atlas Notes.\n\n")
	if title = strings.TrimSpace(title); title != "" {
		b.WriteString("Title: " + title + "\n")
	}
	b.WriteString("Note:\n\"\"\"\n")
	if strings.TrimSpace(content) == "" {
		b.WriteString("(the note is empty)")
	} else {
		b.WriteString(strings.TrimRight(content, "\n"))
	}
	b.WriteString("\n\"\"\"\n\n")
	b.WriteString(askRules)
	b.WriteString("\n\nRequest: " + strings.TrimSpace(request))
	return b.String()
}

// EditNote applies a free-form instruction to the note and returns the complete
// updated note, for the assistant's "edit the note" mode. A low temperature keeps
// it faithful — reproducing the note and changing only what the instruction asks.
func (c *Client) EditNote(ctx context.Context, content, instruction string, onToken func(string)) (string, Stats, error) {
	out, stats, err := c.generate(ctx, editPrompt(content, instruction), 0, onToken) // greedy: faithful, deterministic edits
	return unquoteNote(out), stats, err
}

// editPrompt is the note first and the instruction after it, followed by what
// to output, so the instruction is not lost behind a long note.
func editPrompt(content, instruction string) string {
	return fmt.Sprintf("Here is a note:\n\"\"\"\n%s\n\"\"\"\n\nInstruction: %s\n\n"+
		"Apply the instruction to the note, then output the ENTIRE updated note. Reproduce every other line exactly, "+
		"including all headings, paragraphs, blank lines, and existing '- [ ]' / '- [x]' items, and change only what "+
		"the instruction requires. Write any new task as a '- [ ] ' checkbox. Output only the note itself: no "+
		"commentary, and no quotation marks or code fence around it.",
		strings.TrimRight(content, "\n"), strings.TrimSpace(instruction))
}

// unquoteNote takes off the triple quotes the note was sent in, should the
// model copy them into its answer.
func unquoteNote(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, `"""`) && strings.HasSuffix(t, `"""`) && len(t) >= 6 {
		return strings.TrimSpace(t[3 : len(t)-3])
	}
	return s
}

// SortPriorities asks the model to reorder and re-prioritise the items, then
// merges the response back onto the originals (preserving due dates / checked
// state, matched by text). The returned slice is sorted by the new order.
func (c *Client) SortPriorities(ctx context.Context, items []checklist.Item, promptTemplate string) ([]checklist.Item, Stats, error) {
	if len(items) == 0 {
		return nil, Stats{}, fmt.Errorf("no checklist items to sort")
	}
	var sb strings.Builder
	for _, it := range items {
		sb.WriteString("- ")
		sb.WriteString(it.Text)
		sb.WriteByte('\n')
	}
	prompt := strings.ReplaceAll(promptTemplate, "{items}", sb.String())

	raw, stats, err := c.generate(ctx, prompt, -1, nil)
	if err != nil {
		return nil, Stats{}, err
	}
	out, err := mergeSortResponse(raw, items)
	return out, stats, err
}

type sortedItem struct {
	Text     string `json:"text"`
	Priority string `json:"priority"`
	Order    int    `json:"order"`
}

func mergeSortResponse(raw string, original []checklist.Item) ([]checklist.Item, error) {
	jsonText := extractJSONArray(raw)
	if jsonText == "" {
		return nil, fmt.Errorf("no JSON array found in model response")
	}
	var parsed []sortedItem
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return nil, fmt.Errorf("parse priorities JSON: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("model returned an empty checklist")
	}

	byText := make(map[string]checklist.Item, len(original))
	for _, it := range original {
		byText[normalizeText(it.Text)] = it
	}

	out := make([]checklist.Item, 0, len(parsed))
	for i, p := range parsed {
		it := byText[normalizeText(p.Text)] // zero value if unmatched
		it.Text = p.Text
		if pr := checklist.Priority(strings.ToLower(strings.TrimSpace(p.Priority))); pr.Valid() && pr != checklist.PriorityNone {
			it.Priority = pr
		}
		if p.Order > 0 {
			it.Order = p.Order
		} else {
			it.Order = i + 1
		}
		out = append(out, it)
	}
	// Sort by priority first — small models grade priorities reliably but often
	// leave "order" in document order, so this is what actually re-prioritizes the
	// list — then by the model's order within a priority.
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := priorityRank(out[i].Priority), priorityRank(out[j].Priority); ri != rj {
			return ri < rj
		}
		return out[i].Order < out[j].Order
	})
	// Renumber to a clean 1..n sequence after sorting.
	for i := range out {
		out[i].Order = i + 1
	}
	return out, nil
}

func priorityRank(p checklist.Priority) int {
	switch p {
	case checklist.PriorityHigh:
		return 0
	case checklist.PriorityMedium:
		return 1
	case checklist.PriorityLow:
		return 2
	default:
		return 3
	}
}

func normalizeText(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// extractJSONArray returns the substring from the first '[' to the last ']',
// tolerating models that wrap JSON in prose or code fences.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
