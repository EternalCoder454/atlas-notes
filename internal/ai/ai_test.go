package ai

import (
	"testing"

	"atlas-notes/internal/checklist"
)

func TestExtractJSONArray(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[{"a":1}]`, `[{"a":1}]`},
		{"sure! ```json\n[1,2,3]\n``` done", "[1,2,3]"},
		{"no array here", ""},
		{`prefix ["x"] suffix`, `["x"]`},
	}
	for _, c := range cases {
		if got := extractJSONArray(c.in); got != c.want {
			t.Errorf("extractJSONArray(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestMergeSortResponse(t *testing.T) {
	original := []checklist.Item{
		{Text: "Pay rent", DueDate: "2026-07-01"},
		{Text: "Water plants", Checked: true},
		{Text: "Read book"},
	}
	raw := `Here you go:
	[
	  {"text":"Water plants","priority":"low","order":3},
	  {"text":"Pay rent","priority":"high","order":1},
	  {"text":"Read book","priority":"medium","order":2}
	]`
	out, err := mergeSortResponse(raw, original)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d want 3", len(out))
	}
	if out[0].Text != "Pay rent" || out[0].Priority != checklist.PriorityHigh {
		t.Errorf("out[0] = %+v want high-priority Pay rent", out[0])
	}
	// Metadata from the original is preserved, matched by text.
	if out[0].DueDate != "2026-07-01" {
		t.Errorf("due date not preserved: %+v", out[0])
	}
	if out[2].Text != "Water plants" || !out[2].Checked {
		t.Errorf("checked state not preserved: %+v", out[2])
	}
	// Orders are renumbered to a clean 1..n sequence.
	for i, it := range out {
		if it.Order != i+1 {
			t.Errorf("order[%d] = %d want %d", i, it.Order, i+1)
		}
	}
}

// TestMergeSortByPriority verifies the result is sorted by priority even when the
// model leaves "order" in document order (the common small-model behavior).
func TestMergeSortByPriority(t *testing.T) {
	original := []checklist.Item{
		{Text: "Buy milk"},
		{Text: "Fix prod bug"},
		{Text: "Read book"},
	}
	raw := `[
	  {"text":"Buy milk","priority":"low","order":1},
	  {"text":"Fix prod bug","priority":"high","order":2},
	  {"text":"Read book","priority":"medium","order":3}
	]`
	out, err := mergeSortResponse(raw, original)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Fix prod bug", "Read book", "Buy milk"} // high, medium, low
	for i, w := range want {
		if out[i].Text != w {
			t.Errorf("out[%d] = %q want %q", i, out[i].Text, w)
		}
	}
}
