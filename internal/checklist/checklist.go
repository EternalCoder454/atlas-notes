// Package checklist defines the pure (GTK-free) data model for checklist items:
// parsing them out of markdown lines, serializing them back, and ordering them.
// Keeping this package free of any UI dependency lets the storage and ai layers
// import it without pulling in GTK.
package checklist

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Priority is the urgency of a checklist item.
type Priority string

const (
	PriorityNone   Priority = ""
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
)

// Valid reports whether p is one of the known priority values (empty included).
func (p Priority) Valid() bool {
	switch p {
	case PriorityNone, PriorityLow, PriorityMedium, PriorityHigh:
		return true
	}
	return false
}

// Item is a single checklist entry parsed from a markdown line such as:
//
//   - [ ] Buy groceries <!-- priority:high due:2026-07-01 order:1 -->
type Item struct {
	Text     string
	Checked  bool
	Priority Priority
	DueDate  string // ISO yyyy-mm-dd, empty if unset
	Order    int    // 1-based sort order; 0 means "unset"
}

// itemRe matches a markdown task line, capturing indentation, the checkbox
// state, and the remaining text (which may include trailing metadata).
var itemRe = regexp.MustCompile(`^(\s*)-\s+\[([ xX])\]\s+(.*)$`)

// metaRe matches a trailing HTML comment carrying item metadata.
var metaRe = regexp.MustCompile(`<!--\s*(.*?)\s*-->`)

// ParseLine parses a single line into an Item. ok is false when the line is not
// a checklist item.
func ParseLine(line string) (it Item, ok bool) {
	m := itemRe.FindStringSubmatch(line)
	if m == nil {
		return Item{}, false
	}
	it.Checked = m[2] == "x" || m[2] == "X"
	rest := m[3]
	if cm := metaRe.FindStringSubmatch(rest); cm != nil {
		parseMeta(cm[1], &it)
		rest = metaRe.ReplaceAllString(rest, "")
	}
	it.Text = strings.TrimSpace(rest)
	return it, true
}

func parseMeta(meta string, it *Item) {
	for _, field := range strings.Fields(meta) {
		k, v, found := strings.Cut(field, ":")
		if !found {
			continue
		}
		switch k {
		case "priority":
			it.Priority = Priority(v)
		case "due":
			it.DueDate = v
		case "order":
			if n, err := strconv.Atoi(v); err == nil {
				it.Order = n
			}
		}
	}
}

// Marshal renders the item back to a markdown line (without leading
// indentation), appending a metadata comment only when there is metadata.
func (it Item) Marshal() string {
	box := " "
	if it.Checked {
		box = "x"
	}
	line := fmt.Sprintf("- [%s] %s", box, it.Text)
	if meta := it.metaComment(); meta != "" {
		line += " " + meta
	}
	return line
}

func (it Item) metaComment() string {
	var parts []string
	if it.Priority != PriorityNone {
		parts = append(parts, "priority:"+string(it.Priority))
	}
	if it.DueDate != "" {
		parts = append(parts, "due:"+it.DueDate)
	}
	if it.Order > 0 {
		parts = append(parts, "order:"+strconv.Itoa(it.Order))
	}
	if len(parts) == 0 {
		return ""
	}
	return "<!-- " + strings.Join(parts, " ") + " -->"
}

// Parse extracts every checklist item from note content, in document order.
func Parse(content string) []Item {
	var items []Item
	for _, line := range strings.Split(content, "\n") {
		if it, ok := ParseLine(line); ok {
			items = append(items, it)
		}
	}
	return items
}

// HasItems reports whether content contains at least one checklist item.
func HasItems(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if _, ok := ParseLine(line); ok {
			return true
		}
	}
	return false
}

// Meta returns the trailing HTML-comment metadata for the item, or "".
func (it Item) Meta() string { return it.metaComment() }

// TextOffset returns the rune offset at which the item text begins (just after
// the "- [x] " prefix), or -1 when line is not a checklist item.
func TextOffset(line string) int {
	loc := itemRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return -1
	}
	// Submatch 3 (the text) has byte-start loc[6].
	return utf8.RuneCountInString(line[:loc[6]])
}

// PriorityRank maps a priority to a sortable rank (high first). Unknown/none
// sort last. Used as a stable ordering key.
func PriorityRank(p Priority) int {
	switch p {
	case PriorityHigh:
		return 0
	case PriorityMedium:
		return 1
	case PriorityLow:
		return 2
	default:
		return 3
	}
}
