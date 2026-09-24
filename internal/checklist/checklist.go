// Package checklist defines the pure (GTK-free) data model for checklist items:
// parsing them out of markdown lines, serializing them back, and ordering them.
// Keeping this package free of any UI dependency lets the storage and ai layers
// import it without pulling in GTK.
package checklist

import (
	"fmt"
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

// ParseLine parses a single line into an Item. ok is false when the line is not
// a checklist item.
//
// This is the hottest function in the app: the editor runs it over every line of
// the open note on each render pass, so it is a hand-written scanner rather than
// a regular expression (about fifty times faster, and it allocates nothing for
// the common case of a line that isn't a task).
func ParseLine(line string) (it Item, ok bool) {
	rest, checked, found := cutTaskPrefix(line)
	if !found {
		return Item{}, false
	}
	it.Checked = checked
	if start, end, has := findMeta(rest); has {
		parseMeta(strings.TrimSpace(rest[start+len(metaOpen):end]), &it)
		rest = rest[:start] + rest[end+len(metaClose):]
	}
	it.Text = strings.TrimSpace(rest)
	return it, true
}

const (
	metaOpen  = "<!--"
	metaClose = "-->"
)

// cutTaskPrefix matches a leading `- [ ] ` / `- [x] ` (any indentation, any run
// of spaces around the marker) and returns the text after it.
func cutTaskPrefix(line string) (rest string, checked, ok bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != '-' {
		return "", false, false
	}
	i++
	start := i
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == start { // "-[ ]" is not a task line: the marker needs whitespace
		return "", false, false
	}
	if i+2 >= len(line) || line[i] != '[' || line[i+2] != ']' {
		return "", false, false
	}
	switch line[i+1] {
	case ' ':
		checked = false
	case 'x', 'X':
		checked = true
	default:
		return "", false, false
	}
	i += 3
	start = i
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == start { // the text must be separated from the box
		return "", false, false
	}
	return line[i:], checked, true
}

// findMeta locates a trailing "<!-- … -->" metadata comment in s.
func findMeta(s string) (start, end int, ok bool) {
	start = strings.Index(s, metaOpen)
	if start < 0 {
		return 0, 0, false
	}
	rel := strings.Index(s[start:], metaClose)
	if rel < 0 {
		return 0, 0, false
	}
	return start, start + rel, true
}

// parseMeta reads the "priority:… due:… order:…" fields of a metadata comment.
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
// It walks the content line by line without materializing a slice of lines.
func Parse(content string) []Item {
	var items []Item
	eachLine(content, func(line string) bool {
		if it, ok := ParseLine(line); ok {
			items = append(items, it)
		}
		return true
	})
	return items
}

// HasItems reports whether content contains at least one checklist item.
func HasItems(content string) bool {
	found := false
	eachLine(content, func(line string) bool {
		if _, ok := ParseLine(line); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// eachLine calls fn for every line in s, stopping early when fn returns false.
// It avoids the allocation strings.Split makes for a whole document.
func eachLine(s string, fn func(string) bool) {
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			fn(s)
			return
		}
		if !fn(s[:i]) {
			return
		}
		s = s[i+1:]
	}
}

// Meta returns the trailing HTML-comment metadata for the item, or "".
func (it Item) Meta() string { return it.metaComment() }

// TextOffset returns the rune offset at which the item text begins (just after
// the "- [x] " prefix), or -1 when line is not a checklist item.
func TextOffset(line string) int {
	rest, _, ok := cutTaskPrefix(line)
	if !ok {
		return -1
	}
	// The prefix is ASCII, so its rune count equals its byte length.
	return utf8.RuneCountInString(line[:len(line)-len(rest)])
}

// Progress counts finished and total checklist items in content. It is used for
// the editor's "n of m tasks" indicator, so it only matches the task prefix and
// never parses metadata.
func Progress(content string) (done, total int) {
	eachLine(content, func(line string) bool {
		_, checked, ok := cutTaskPrefix(line)
		if !ok {
			return true
		}
		total++
		if checked {
			done++
		}
		return true
	})
	return done, total
}
