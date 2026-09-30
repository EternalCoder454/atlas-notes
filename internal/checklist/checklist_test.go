package checklist

import "testing"

func TestParseLine(t *testing.T) {
	cases := []struct {
		line string
		want Item
		ok   bool
	}{
		{"- [ ] Buy milk", Item{Text: "Buy milk"}, true},
		{"- [x] Done thing", Item{Text: "Done thing", Checked: true}, true},
		{"- [X] Caps done", Item{Text: "Caps done", Checked: true}, true},
		{
			"- [ ] Task <!-- priority:high due:2026-07-01 order:2 -->",
			Item{Text: "Task", Priority: PriorityHigh, DueDate: "2026-07-01", Order: 2},
			true,
		},
		{"  - [ ] Indented", Item{Text: "Indented"}, true},
		{"Not a task", Item{}, false},
		{"- not a checkbox", Item{}, false},
	}
	for _, c := range cases {
		got, ok := ParseLine(c.line)
		if ok != c.ok {
			t.Errorf("ParseLine(%q) ok=%v want %v", c.line, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("ParseLine(%q) = %+v want %+v", c.line, got, c.want)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	items := []Item{
		{Text: "Plain"},
		{Text: "Checked", Checked: true},
		{Text: "Full", Checked: true, Priority: PriorityMedium, DueDate: "2026-12-31", Order: 3},
	}
	for _, it := range items {
		line := it.Marshal()
		got, ok := ParseLine(line)
		if !ok {
			t.Fatalf("Marshal(%+v) = %q did not re-parse", it, line)
		}
		if got != it {
			t.Errorf("round trip: %+v -> %q -> %+v", it, line, got)
		}
	}
}

func TestParseDocument(t *testing.T) {
	content := "# Title\n\n- [ ] one\nsome text\n- [x] two\n"
	items := Parse(content)
	if len(items) != 2 {
		t.Fatalf("Parse len = %d want 2", len(items))
	}
	if items[0].Text != "one" || items[1].Text != "two" || !items[1].Checked {
		t.Errorf("Parse = %+v", items)
	}
}

// TestParseLineScanner covers the edge cases of the hand-written scanner that
// replaced the regular expression: unusual spacing, non-ASCII text, malformed
// boxes, and metadata that isn't a well-formed comment.
func TestParseLineScanner(t *testing.T) {
	cases := []struct {
		line string
		want Item
		ok   bool
	}{
		{"-  [ ]  Extra spaces", Item{Text: "Extra spaces"}, true},
		{"\t- [x] Tab indented", Item{Text: "Tab indented", Checked: true}, true},
		{"- [ ] Café — naïve ünïcode", Item{Text: "Café — naïve ünïcode"}, true},
		{"- [ ] Trailing spaces   ", Item{Text: "Trailing spaces"}, true},
		{"- [ ] Has <!-- priority:low --> mid-line meta", Item{Text: "Has  mid-line meta", Priority: PriorityLow}, true},
		{"- [ ] Unclosed <!-- priority:low", Item{Text: "Unclosed <!-- priority:low"}, true},
		{"- [ ] Bad meta <!-- nonsense -->", Item{Text: "Bad meta"}, true},
		{"- [ ] Bad order <!-- order:abc -->", Item{Text: "Bad order"}, true},
		{"-[ ] No space after dash", Item{}, false},
		{"- [] Empty box", Item{}, false},
		{"- [y] Wrong mark", Item{}, false},
		{"- [ ]No space after box", Item{}, false},
		{"- [ ]", Item{}, false},
		{"", Item{}, false},
		{"- ", Item{}, false},
		{"* [ ] Asterisk bullet", Item{}, false},
	}
	for _, c := range cases {
		got, ok := ParseLine(c.line)
		if ok != c.ok {
			t.Errorf("ParseLine(%q) ok=%v want %v", c.line, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("ParseLine(%q) = %+v want %+v", c.line, got, c.want)
		}
	}
}

func TestTextOffset(t *testing.T) {
	cases := map[string]int{
		"- [ ] Buy milk":        6,
		"  - [x] Indented":      8,
		"-  [ ]  Extra spacing": 8,
		"Not a task":            -1,
	}
	for line, want := range cases {
		if got := TextOffset(line); got != want {
			t.Errorf("TextOffset(%q) = %d want %d", line, got, want)
		}
	}
}

func TestTaskLine(t *testing.T) {
	cases := []struct {
		line            string
		checked, isTask bool
	}{
		{"- [ ] one", false, true},
		{"- [x] two", true, true},
		{"- [X] three", true, true},
		{"  - [ ] indented", false, true},
		{"some prose", false, false},
		{"# Title", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		checked, ok := TaskLine(c.line)
		if ok != c.isTask || checked != c.checked {
			t.Errorf("TaskLine(%q) = (%v, %v), want (%v, %v)", c.line, checked, ok, c.checked, c.isTask)
		}
	}
}

func TestEmojiForm(t *testing.T) {
	cases := []struct {
		line string
		want Item
	}{
		{"- [ ] Buy milk ⏫ 📅 2026-07-01", Item{Text: "Buy milk", Priority: PriorityHigh, DueDate: "2026-07-01"}},
		{"- [x] Buy milk 📅 2026-07-01 🔼", Item{Text: "Buy milk", Checked: true, Priority: PriorityMedium, DueDate: "2026-07-01"}},
		{"- [ ] Call 🔽 mum", Item{Text: "Call mum", Priority: PriorityLow}},
		{"- [ ] a 📅2026-07-01", Item{Text: "a", DueDate: "2026-07-01"}},
		{"- [ ] a ⏫️", Item{Text: "a", Priority: PriorityHigh}},
		// Emoji inside a code span is text about the emoji.
		{"- [ ] use `⏫` for high", Item{Text: "use `⏫` for high"}},
		{"- [ ] `` ` 📅 2026-07-01 `` x", Item{Text: "`` ` 📅 2026-07-01 `` x"}},
		// An unclosed tick is literal, so the marker after it counts.
		{"- [ ] it`s ⏫", Item{Text: "it`s", Priority: PriorityHigh}},
		// Not a date, or no date: stays as text.
		{"- [ ] a 📅 2026-13-45", Item{Text: "a 📅 2026-13-45"}},
		{"- [ ] a 📅 2026-02-30", Item{Text: "a 📅 2026-02-30"}},
		{"- [ ] a 📅", Item{Text: "a 📅"}},
		{"- [ ] a 📅 soon", Item{Text: "a 📅 soon"}},
		{"- [ ] a 📅 2026-07-011", Item{Text: "a 📅 2026-07-011"}},
		// Both forms: the emoji win, the comment still supplies the rest.
		{"- [ ] a ⏫ <!-- priority:low due:2026-01-01 order:3 -->", Item{Text: "a", Priority: PriorityHigh, DueDate: "2026-01-01", Order: 3}},
		{"- [ ] a 📅 2026-05-05 <!-- due:2026-01-01 -->", Item{Text: "a", DueDate: "2026-05-05"}},
		// Emoji inside the comment are not read as markers.
		{"- [ ] a <!-- 🔼 -->", Item{Text: "a"}},
	}
	for _, c := range cases {
		got, ok := ParseLine(c.line)
		if !ok || got != c.want {
			t.Errorf("ParseLine(%q) = %+v, %v; want %+v", c.line, got, ok, c.want)
		}
	}
}

func TestMarshalWritesEmoji(t *testing.T) {
	cases := []struct {
		it   Item
		want string
	}{
		{Item{Text: "a"}, "- [ ] a"},
		{Item{Text: "a", Checked: true, Priority: PriorityHigh, DueDate: "2026-07-01"}, "- [x] a ⏫ 📅 2026-07-01"},
		{Item{Text: "a", Priority: PriorityLow}, "- [ ] a 🔽"},
		{Item{Text: "a", Priority: PriorityMedium, Order: 2}, "- [ ] a 🔼 <!-- order:2 -->"},
		// What the emoji cannot say stays in the comment instead of vanishing.
		{Item{Text: "a", Priority: "urgent", DueDate: "soon"}, "- [ ] a <!-- priority:urgent due:soon -->"},
	}
	for _, c := range cases {
		if got := c.it.Marshal(); got != c.want {
			t.Errorf("Marshal(%+v) = %q, want %q", c.it, got, c.want)
		}
		if back, ok := ParseLine(c.it.Marshal()); !ok || back != c.it {
			t.Errorf("round trip of %+v gave %+v", c.it, back)
		}
	}
}

func TestMetaRanges(t *testing.T) {
	text := "Buy milk ⏫ and `⏫` 📅 2026-07-01"
	var hidden []string
	for _, r := range MetaRanges(text) {
		hidden = append(hidden, text[r[0]:r[1]])
	}
	if len(hidden) != 2 || hidden[0] != " ⏫" || hidden[1] != " 📅 2026-07-01" {
		t.Fatalf("MetaRanges = %q", hidden)
	}
	if MetaRanges("plain text") != nil {
		t.Fatal("plain text has no ranges")
	}
}
