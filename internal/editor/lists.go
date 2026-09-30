package editor

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"atlas-notes/internal/checklist"
)

// This file makes Enter continue a list: a new item opens with the same kind of
// marker, and Enter on an item with nothing in it ends the list.

// listItem is what Enter on a list line needs to know about it.
type listItem struct {
	next   string // what the next item opens with, indentation included
	textAt int    // the character column where the item's own text starts
	empty  bool   // the item has no text, only its marker (and any metadata)
}

// listContinuation reads line as the buffer holds it, with a drawn bullet already
// turned back into its marker (see sourceLine), and says how a list goes on from
// it. A line that is not a list item reports false. A rendered task starts with
// its checkbox's anchor, which stands for "- [ ] ".
func listContinuation(line string) (listItem, bool) {
	if strings.HasPrefix(line, anchorChar) {
		return newListItem("- [ ] ", 1, line[len(anchorChar):]), true
	}
	if off := checklist.TextOffset(line); off >= 0 {
		// The prefix is ASCII, so its length in characters is its length in bytes.
		return newListItem(indentOf(line)+"- [ ] ", off, line[off:]), true
	}
	if strings.HasPrefix(line, "> ") {
		// A quote inside a quote goes on at the same depth.
		n := 0
		for strings.HasPrefix(line[n:], "> ") {
			n += 2
		}
		return newListItem(line[:n], n, line[n:]), true
	}
	if m := markerPrefix(line); m > 0 {
		return newListItem(line[:m], m, line[m:]), true
	}
	if m := numberPrefix(line); m > 0 {
		i := len(indentOf(line))
		digits := line[i : m-2]
		n, err := strconv.Atoi(digits)
		next := strconv.Itoa(n + 1)
		if err != nil || len(next) > 9 {
			return listItem{}, false // too long a number to be a list's, as in numberPrefix
		}
		return newListItem(line[:i]+next+line[m-2:m], m, line[m:]), true
	}
	return listItem{}, false
}

func newListItem(next string, textAt int, text string) listItem {
	return listItem{next: next, textAt: textAt, empty: strings.TrimSpace(withoutMeta(text)) == ""}
}

// indentOf is the spaces and tabs a line starts with.
func indentOf(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

// markerPrefix returns the byte offset just past a "- ", "* " or "+ " marker and
// its indentation, or 0. It is bulletPrefix without leaving out the lines that
// start with a "[": a list of links ("- [[Note]]") is still a list to continue.
func markerPrefix(line string) int {
	i := len(indentOf(line))
	if i+1 >= len(line) || (line[i] != '-' && line[i] != '*' && line[i] != '+') || line[i+1] != ' ' {
		return 0
	}
	return i + 2
}

// withoutMeta drops a trailing "<!-- ... -->" comment, the one snapCaret keeps
// the caret out of.
func withoutMeta(s string) string {
	if b := strings.Index(s, "<!--"); b >= 0 {
		if rel := strings.Index(s[b:], "-->"); rel >= 0 && strings.TrimSpace(s[b+rel+len("-->"):]) == "" {
			return s[:b]
		}
	}
	return s
}

// continueList handles Enter on a list line and reports whether it did, in which
// case the newline is in and the view must not add another.
//
//   - At the end of an item, or in the middle of its text, a new item opens with
//     the same marker; in the middle, the rest of the text moves to it.
//   - On an item with no text, the marker goes and the line is left empty.
//   - A task's metadata comment stays on the task the caret was in, so that a
//     split does not hand it to the new item, where it would belong to nothing.
//
// It does nothing with the caret in the marker itself, in a fenced code block,
// or while the suggestion popover has Enter. The edits are one user action, so
// one undo takes them back.
func (e *Editor) continueList() bool {
	if e.sg.kind != suggestNone {
		return false
	}
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	if ins == nil {
		return false
	}
	ln, col := ins.Line(), ins.LineOffset()
	if inFence(e.fence, ln) {
		return false
	}
	line, ok := e.sourceLine(ln)
	if !ok {
		return false
	}
	item, ok := listContinuation(line)
	if !ok || col < item.textAt {
		return false
	}
	total := utf8.RuneCountInString(line)

	e.buffer.BeginUserAction()
	defer e.buffer.EndUserAction()
	if item.empty {
		start, ok1 := e.buffer.IterAtLineOffset(ln, 0)
		end, ok2 := e.buffer.IterAtLineOffset(ln, total)
		if ok1 && ok2 {
			e.buffer.Delete(start, end)
		}
		return true
	}
	meta := ""
	if stop := snapCaret(line, total); stop < total {
		start, ok1 := e.buffer.IterAtLineOffset(ln, stop)
		end, ok2 := e.buffer.IterAtLineOffset(ln, total)
		if ok1 && ok2 {
			meta = e.buffer.Slice(start, end, true)
			e.buffer.Delete(start, end)
		}
		col = min(col, stop)
	}
	at, ok := e.buffer.IterAtLineOffset(ln, col)
	if !ok {
		return true
	}
	e.buffer.Insert(at, meta+"\n"+item.next)
	return true
}
