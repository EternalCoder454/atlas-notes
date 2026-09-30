package editor

import (
	"testing"

	"atlas-notes/internal/frontmatter"
)

func TestReadOnlyText(t *testing.T) {
	for _, c := range []struct{ front, want string }{
		{"k: {a: 1, b: 2}", "{a: 1, b: 2}"},
		{"\"a: b\": {x: 1}", "{x: 1}"},
		{"nested:\n  a: 1\n  b: 2", "a: 1  b: 2"},
	} {
		d, ok := frontmatter.Parse("---\n" + c.front + "\n---")
		if !ok || len(d.Props) != 1 {
			t.Fatalf("%q: %+v", c.front, d)
		}
		if got := readOnlyText(d.Props[0]); got != c.want {
			t.Errorf("%q: got %q, want %q", c.front, got, c.want)
		}
	}
}
