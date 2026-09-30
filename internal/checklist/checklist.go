// Package checklist defines the pure (GTK-free) data model for checklist items:
// parsing them out of markdown lines, serializing them back, and ordering them.
// Keeping this package free of any UI dependency lets the storage and ai layers
// import it without pulling in GTK.
package checklist

import (
	"strconv"
	"strings"
	"time"
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
//   - [ ] Buy groceries ⏫ 📅 2026-07-01
//
// That is the Obsidian Tasks plugin's form, which is what Marshal writes: ⏫ high,
// 🔼 medium, 🔽 low, and 📅 for the due date. The older form keeps the same
// facts in an HTML comment, and is still read everywhere:
//
//   - [ ] Buy groceries <!-- priority:high due:2026-07-01 order:1 -->
//
// When a line carries both, the emoji win: they are what Obsidian shows and
// what someone editing the line by hand sees, so they are the newer word. The
// comment is the only place "order" can live, since Obsidian has no such thing.
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
	// Cutting a marker can join the bytes either side of it into a new one (a
	// broken emoji with a real one inside it), so cut until nothing is left to
	// cut. Only a line that had a marker pays for the second look.
	for {
		cut := cutEmoji(rest, &it)
		if len(cut) == len(rest) {
			break
		}
		rest = cut
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

// findMeta locates a trailing "<!-- … -->" metadata comment in s and returns
// the index of its opening marker and of its closing one.
//
// The closing marker is searched for *after* the opening one. The two overlap
// in "<!-->", where a search from the start of the line finds "-->" inside the
// opening marker itself and yields an end before the start — which used to be
// sliced, and panicked.
func findMeta(s string) (start, end int, ok bool) {
	start = strings.Index(s, metaOpen)
	if start < 0 {
		return 0, 0, false
	}
	from := start + len(metaOpen)
	rel := strings.Index(s[from:], metaClose)
	if rel < 0 {
		return 0, 0, false
	}
	return start, from + rel, true
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
// indentation). Priority and due date are written as Obsidian Tasks emoji; a
// comment is added only for what has no emoji form: the order, and any priority
// or date the emoji cannot say (a value nobody recognises is kept, not lost).
func (it Item) Marshal() string {
	box := " "
	if it.Checked {
		box = "x"
	}
	var b strings.Builder
	b.WriteString("- [")
	b.WriteString(box)
	b.WriteString("] ")
	b.WriteString(it.Text)
	emojiPri := priorityEmoji(it.Priority)
	if emojiPri != "" {
		b.WriteByte(' ')
		b.WriteString(emojiPri)
	}
	emojiDue := validDate(it.DueDate)
	if emojiDue {
		b.WriteString(" " + dueEmoji + " " + it.DueDate)
	}
	if meta := it.metaComment(emojiPri != "", emojiDue); meta != "" {
		b.WriteByte(' ')
		b.WriteString(meta)
	}
	return b.String()
}

// metaComment renders what the emoji could not carry.
func (it Item) metaComment(priDone, dueDone bool) string {
	var parts []string
	if it.Priority != PriorityNone && !priDone {
		parts = append(parts, "priority:"+string(it.Priority))
	}
	if it.DueDate != "" && !dueDone {
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

const (
	dueEmoji  = "📅"
	highEmoji = "⏫"
	medEmoji  = "🔼"
	lowEmoji  = "🔽"
	varSel    = "\uFE0F" // emoji presentation selector some keyboards add
)

func priorityEmoji(p Priority) string {
	switch p {
	case PriorityHigh:
		return highEmoji
	case PriorityMedium:
		return medEmoji
	case PriorityLow:
		return lowEmoji
	}
	return ""
}

// validDate reports whether s is a real yyyy-mm-dd date ("2026-02-30" is not).
func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// emojiToken is one Obsidian-style marker found in a line: the byte range it
// covers, widened to take the spaces before it, and what it says.
type emojiToken struct {
	start, end int
	pri        Priority
	due        string
}

// scanEmoji calls fn for each priority or due-date marker in s, in order.
// Markers inside a code span or inside a "<!-- … -->" comment are not markers:
// `⏫` is someone writing about the emoji, and the comment is read by parseMeta.
// A 📅 with no real date after it is left alone as ordinary text.
//
// It touches only three lead bytes, so a line with none of them costs one pass
// and no allocation.
func scanEmoji(s string, fn func(emojiToken)) {
	prevEnd := 0
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			if j := closingTicks(s, i+n, n); j >= 0 {
				i = j + n
			} else {
				i += n // no closing run: the ticks are literal
			}
		case c == '<' && strings.HasPrefix(s[i:], metaOpen):
			if rel := strings.Index(s[i+len(metaOpen):], metaClose); rel >= 0 {
				i += len(metaOpen) + rel + len(metaClose)
			} else {
				i++
			}
		case c == 0xE2 || c == 0xF0:
			var tok emojiToken
			end := -1
			switch {
			case strings.HasPrefix(s[i:], highEmoji):
				end, tok.pri = i+len(highEmoji), PriorityHigh
			case strings.HasPrefix(s[i:], medEmoji):
				end, tok.pri = i+len(medEmoji), PriorityMedium
			case strings.HasPrefix(s[i:], lowEmoji):
				end, tok.pri = i+len(lowEmoji), PriorityLow
			case strings.HasPrefix(s[i:], dueEmoji):
				e := i + len(dueEmoji)
				if strings.HasPrefix(s[e:], varSel) {
					e += len(varSel)
				}
				for e < len(s) && (s[e] == ' ' || s[e] == '\t') {
					e++
				}
				if e+10 <= len(s) && validDate(s[e:e+10]) &&
					(e+10 == len(s) || s[e+10] < '0' || s[e+10] > '9') {
					end, tok.due = e+10, s[e:e+10]
				}
			}
			if end < 0 {
				i++
				continue
			}
			if strings.HasPrefix(s[end:], varSel) {
				end += len(varSel)
			}
			start := i
			for start > prevEnd && (s[start-1] == ' ' || s[start-1] == '\t') {
				start--
			}
			tok.start, tok.end = start, end
			fn(tok)
			prevEnd, i = end, end
		default:
			i++
		}
	}
}

// closingTicks finds a run of exactly n backticks at or after from, or -1.
func closingTicks(s string, from, n int) int {
	for i := from; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return i
		}
		i = j - 1
	}
	return -1
}

// cutEmoji reads the markers in s into it and returns s without them. When
// there are none it returns s itself, so a plain task allocates nothing here.
func cutEmoji(s string, it *Item) string {
	var b strings.Builder
	last := 0
	scanEmoji(s, func(t emojiToken) {
		if t.pri != PriorityNone {
			it.Priority = t.pri
		}
		if t.due != "" {
			it.DueDate = t.due
		}
		b.WriteString(s[last:t.start])
		last = t.end
	})
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// MetaRanges returns the byte ranges of a task line's emoji markers (with the
// spaces before each), so the editor can hide them beside its own priority bar
// and date chip. text is the task's text, not the "- [ ]" prefix.
func MetaRanges(text string) [][2]int {
	var out [][2]int
	scanEmoji(text, func(t emojiToken) { out = append(out, [2]int{t.start, t.end}) })
	return out
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

// TaskLine reports whether a single line is a checklist item, and whether it is
// ticked. It only matches the prefix — no metadata is parsed — which is what a
// counter walking a whole document wants.
func TaskLine(line string) (checked, ok bool) {
	_, checked, ok = cutTaskPrefix(line)
	return checked, ok
}

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
