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

func TestParseAndHasItems(t *testing.T) {
	content := "# Title\n\n- [ ] one\nsome text\n- [x] two\n"
	if !HasItems(content) {
		t.Fatal("HasItems = false, want true")
	}
	items := Parse(content)
	if len(items) != 2 {
		t.Fatalf("Parse len = %d want 2", len(items))
	}
	if items[0].Text != "one" || items[1].Text != "two" || !items[1].Checked {
		t.Errorf("Parse = %+v", items)
	}
	if HasItems("# just a title\nno tasks here") {
		t.Error("HasItems = true for content without tasks")
	}
}
