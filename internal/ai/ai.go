// Package ai is a thin client for a local Ollama server. Every call is blocking
// and bounded by a 60s timeout; the UI layer is responsible for running these on
// a goroutine and marshalling results back to the GTK main thread.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"atlas-notes/internal/checklist"
)

const (
	DefaultBaseURL = "http://localhost:11434"
	DefaultModel   = "qwen2.5:3b"

	requestTimeout = 60 * time.Second
	probeTimeout   = 2 * time.Second

	// DefaultSystemPrompt is used when a client has no configured system prompt.
	DefaultSystemPrompt = "You are a concise assistant. You only have access to the note provided. Do not reference external information. Be brief and precise."
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

// Available reports whether the Ollama server responds to a HEAD request. It
// reuses the shared HTTP client (and its connection pool) with a short
// per-request deadline, so the periodic status poll never blocks or churns
// connections.
func (c *Client) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.BaseURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

// generate performs a single non-streaming /api/generate call.
func (c *Client) generate(ctx context.Context, prompt string) (string, error) {
	sys := c.SystemPrompt
	if sys == "" {
		sys = DefaultSystemPrompt
	}
	body, err := json.Marshal(generateRequest{
		Model:  c.Model,
		Prompt: prompt,
		System: sys,
		Stream: false,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned %s", resp.Status)
	}
	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if gr.Error != "" {
		return "", fmt.Errorf("ollama: %s", gr.Error)
	}
	return strings.TrimSpace(gr.Response), nil
}

// RunAction runs a user-defined action: it substitutes {content} in the prompt
// template with the note text and returns the model's response.
func (c *Client) RunAction(ctx context.Context, promptTemplate, content string) (string, error) {
	return c.generate(ctx, strings.ReplaceAll(promptTemplate, "{content}", content))
}

// Ask answers a question using only the supplied note.
func (c *Client) Ask(ctx context.Context, content, question string) (string, error) {
	return c.generate(ctx, fmt.Sprintf("Using only the note below, answer this question: %s\n\nNote:\n%s", question, content))
}

// SortPriorities asks the model to reorder and re-prioritise the items, then
// merges the response back onto the originals (preserving due dates / checked
// state, matched by text). The returned slice is sorted by the new order.
func (c *Client) SortPriorities(ctx context.Context, items []checklist.Item, promptTemplate string) ([]checklist.Item, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no checklist items to sort")
	}
	var sb strings.Builder
	for _, it := range items {
		sb.WriteString("- ")
		sb.WriteString(it.Text)
		sb.WriteByte('\n')
	}
	prompt := strings.ReplaceAll(promptTemplate, "{items}", sb.String())

	raw, err := c.generate(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return mergeSortResponse(raw, items)
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	// Renumber to a clean 1..n sequence after sorting.
	for i := range out {
		out[i].Order = i + 1
	}
	return out, nil
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
