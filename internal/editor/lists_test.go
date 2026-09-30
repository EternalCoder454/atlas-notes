package editor

import "testing"

func TestListContinuation(t *testing.T) {
	cases := []struct {
		line   string
		ok     bool
		next   string
		textAt int
		empty  bool
	}{
		{"- a", true, "- ", 2, false},
		{"  * b", true, "  * ", 4, false},
		{"+ c", true, "+ ", 2, false},
		{"12. x", true, "13. ", 4, false},
		{"9) y", true, "10) ", 3, false},
		{"  3. z", true, "  4. ", 5, false},
		{"- [x] done <!-- due:2026-01-01 -->", true, "- [ ] ", 6, false},
		{"  - [ ] indented task", true, "  - [ ] ", 8, false},
		{anchorChar + "rendered task <!-- priority:high -->", true, "- [ ] ", 1, false},
		{"> q", true, "> ", 2, false},
		{"> > nested", true, "> > ", 4, false},
		{"- [[Note]] link", true, "- ", 2, false},
		{"- ", true, "- ", 2, true},
		{"  - ", true, "  - ", 4, true},
		{"3. ", true, "4. ", 3, true},
		{"> ", true, "> ", 2, true},
		{"- [ ] ", true, "- [ ] ", 6, true},
		{anchorChar, true, "- [ ] ", 1, true},
		{anchorChar + " <!-- priority:high -->", true, "- [ ] ", 1, true},
		{"plain", false, "", 0, false},
		{"", false, "", 0, false},
		{"-x", false, "", 0, false},
		{"**bold** text", false, "", 0, false},
		{"1.5 is a number", false, "", 0, false},
		{"999999999. too far", false, "", 0, false},
		{"# heading", false, "", 0, false},
	}
	for _, c := range cases {
		got, ok := listContinuation(c.line)
		if ok != c.ok {
			t.Errorf("listContinuation(%q) ok = %v, want %v", c.line, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.next != c.next || got.textAt != c.textAt || got.empty != c.empty {
			t.Errorf("listContinuation(%q) = {%q, %d, %v}, want {%q, %d, %v}",
				c.line, got.next, got.textAt, got.empty, c.next, c.textAt, c.empty)
		}
	}
}

// enter runs Enter with the caret at offset in a buffer holding text, and returns
// the text afterwards, whether the list handled it, and where the caret is.
func enter(text string, caret int) (string, bool, int) {
	e := bulletEditor(text)
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(caret))
	handled := e.continueList()
	return e.textNow(), handled, e.buffer.IterAtMark(e.buffer.GetInsert()).Offset()
}

func TestContinueList(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		caret int
		want  string
		done  bool
		at    int // where the caret ends, when handled
	}{
		{"end of a bullet", "- item", 6, "- item\n- ", true, 9},
		{"indented star", "  * b", 5, "  * b\n  * ", true, 10},
		{"numbered", "3. x", 4, "3. x\n4. ", true, 8},
		{"numbered paren", "9) y", 4, "9) y\n10) ", true, 9},
		{"quote", "> q", 3, "> q\n> ", true, 6},
		{"task keeps its metadata", "- [x] done <!-- due:2026-01-01 -->", 10,
			"- [x] done <!-- due:2026-01-01 -->\n- [ ] ", true, 41},
		{"split a bullet", "- abcd", 4, "- ab\n- cd", true, 7},
		{"split a task, metadata stays with the first half", "- [ ] abcd <!-- priority:high -->", 8,
			"- [ ] ab <!-- priority:high -->\n- [ ] cd", true, 38},
		{"empty bullet ends the list", "a\n- ", 4, "a\n", true, 2},
		{"empty number ends the list", "3. ", 3, "", true, 0},
		{"empty indented bullet loses its indent too", "  - ", 4, "", true, 0},
		{"empty task ends the list", "- [ ] ", 6, "", true, 0},
		{"caret in the marker", "- item", 1, "- item", false, 1},
		{"caret before the marker", "- item", 0, "- item", false, 0},
		{"plain line", "plain", 5, "plain", false, 5},
		{"a line that is only a quote bar", ">", 1, ">", false, 1},
	}
	for _, c := range cases {
		got, done, at := enter(c.text, c.caret)
		if got != c.want || done != c.done {
			t.Errorf("%s: got %q handled=%v, want %q handled=%v", c.name, got, done, c.want, c.done)
			continue
		}
		if done && at != c.at {
			t.Errorf("%s: caret at %d, want %d", c.name, at, c.at)
		}
	}
}

func TestContinueListIgnoresCode(t *testing.T) {
	e := bulletEditor("```\n- item\n```")
	e.fence = []bool{true, true, true}
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(10))
	if e.continueList() {
		t.Error("Enter was taken inside a code block")
	}
	if got := e.textNow(); got != "```\n- item\n```" {
		t.Errorf("text changed to %q", got)
	}
}

func TestContinueListYieldsToTheSuggestions(t *testing.T) {
	e := bulletEditor("- see [[No")
	e.sg.kind = suggestNotes
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(10))
	if e.continueList() {
		t.Error("Enter was taken while the suggestion popover was open")
	}
}

func TestContinueListReadsADrawnBullet(t *testing.T) {
	e := bulletEditor("• item")
	e.setBulletTag(0, '*')
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(6))
	if !e.continueList() {
		t.Fatal("Enter on a drawn bullet was not taken")
	}
	// The new item is written with the marker the bullet stands for; the drawn
	// one is put back by the next render pass.
	if got, want := e.Content(), "* item\n* "; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
}

func TestContinueListIsOneUndo(t *testing.T) {
	e := bulletEditor("- item")
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(4))
	if !e.continueList() {
		t.Fatal("Enter was not taken")
	}
	e.buffer.Undo()
	if got := e.textNow(); got != "- item" {
		t.Errorf("one undo left %q, want the line as it was", got)
	}
}

func TestContinueListOnEmptyItemIsOneUndo(t *testing.T) {
	e := bulletEditor("a\n- ")
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(4))
	if !e.continueList() {
		t.Fatal("Enter was not taken")
	}
	e.buffer.Undo()
	if got := e.textNow(); got != "a\n- " {
		t.Errorf("one undo left %q, want the marker back", got)
	}
}
