package frontmatter

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) *Doc {
	t.Helper()
	d, ok := Parse(text)
	if !ok {
		t.Fatalf("not front matter: %q", text)
	}
	return d
}

func TestKinds(t *testing.T) {
	d := mustParse(t, strings.Join([]string{
		"---",
		"title: Hello there",
		"count: 42",
		"ratio: -3.5",
		"due: 2026-09-29",
		"done: true",
		"empty:",
		"tags: [a, b]",
		"aliases:",
		"  - Short",
		"  - \"Long, name\"",
		"links:",
		"- one",
		"- two",
		"---",
		"body",
	}, "\n"))
	want := []struct {
		key  string
		kind Kind
	}{
		{"title", Text}, {"count", Number}, {"ratio", Number}, {"due", Date}, {"done", Bool},
		{"empty", Text}, {"tags", Tags}, {"aliases", Aliases}, {"links", List},
	}
	if len(d.Props) != len(want) {
		t.Fatalf("got %d props: %+v", len(d.Props), d.Props)
	}
	for i, w := range want {
		if d.Props[i].Key != w.key || d.Props[i].Kind != w.kind {
			t.Errorf("prop %d = %s/%d, want %s/%d", i, d.Props[i].Key, d.Props[i].Kind, w.key, w.kind)
		}
	}
	if !reflect.DeepEqual(d.Props[7].Items, []string{"Short", "Long, name"}) {
		t.Errorf("aliases = %q", d.Props[7].Items)
	}
	if !reflect.DeepEqual(d.Props[8].Items, []string{"one", "two"}) || d.Props[8].Indent != "" {
		t.Errorf("links = %+v", d.Props[8])
	}
	if d.End != 14 {
		t.Errorf("End = %d", d.End)
	}
	if d.Props[8].Line != 11 || d.Props[8].End != 13 {
		t.Errorf("links lines = %d-%d", d.Props[8].Line, d.Props[8].End)
	}
}

func TestRoundTripKeepsTheLines(t *testing.T) {
	cases := []string{
		"title: Hello there",
		"count: 42",
		"due: 2026-09-29",
		"done: false",
		"empty:",
		"tags: [a, b, c]",
		"tags:\n  - a\n  - b",
		"links:\n- one\n- two",
		"aliases: []",
		`quoted: "a: b"`,
		"url: https://example.com/a?b=c",
		"note: text # a comment",
		"emoji: ünï 🎉 日本語",
		"cmt: # only a comment",
	}
	for _, c := range cases {
		d := mustParse(t, "---\n"+c+"\n---\n")
		if len(d.Props) != 1 {
			t.Fatalf("%q: %d props", c, len(d.Props))
		}
		p := d.Props[0]
		if got := strings.Join(p.Render(), "\n"); got != c {
			t.Errorf("round trip of %q gave %q", c, got)
		}
	}
}

func TestColonsAndQuotingInValues(t *testing.T) {
	p := NewProp("k", Text)
	for value, want := range map[string]string{
		"plain":       "k: plain",
		"a: b":        `k: "a: b"`,
		"ends:":       `k: "ends:"`,
		"with # hash": `k: "with # hash"`,
		"#lead":       `k: "#lead"`,
		"- dash":      `k: "- dash"`,
		"-dash":       "k: -dash",
		"12":          `k: "12"`,
		"true":        `k: "true"`,
		"2026-01-02":  `k: "2026-01-02"`,
		` pad `:       `k: " pad "`,
		"say \"hi\"":  `k: say "hi"`,
		"line\nbreak": `k: "line\nbreak"`,
		"back\\slash": `k: back\slash`,
		"":            "k:",
		"[x]":         `k: "[x]"`,
		"café ☕":      "k: café ☕",
	} {
		p.Value = value
		got := p.Render()[0]
		if got != want {
			t.Errorf("value %q wrote %q, want %q", value, got, want)
		}
		// And what was written reads back as the same value.
		d := mustParse(t, "---\n"+got+"\n---")
		if len(d.Props) != 1 || d.Props[0].Kind != Text || d.Props[0].Value != value {
			t.Errorf("%q read back as %+v", got, d.Props)
		}
	}
}

func TestQuotedForms(t *testing.T) {
	d := mustParse(t, "---\na: 'it''s'\nb: \"tab\\there \\u00e9\"\n\"odd key\": x\n'q: k': y\n---")
	if d.Props[0].Value != "it's" || d.Props[1].Value != "tab\there é" {
		t.Errorf("values = %q, %q", d.Props[0].Value, d.Props[1].Value)
	}
	if d.Props[2].Key != "odd key" || d.Props[2].KeyRaw != `"odd key"` {
		t.Errorf("key = %+v", d.Props[2])
	}
	if d.Props[3].Key != "q: k" || d.Props[3].Value != "y" {
		t.Errorf("key = %+v", d.Props[3])
	}
	// A quoted number is text.
	d = mustParse(t, "---\nn: \"12\"\n---")
	if d.Props[0].Kind != Text || d.Props[0].Value != "12" {
		t.Errorf("quoted number = %+v", d.Props[0])
	}
}

func TestColonInAPlainValue(t *testing.T) {
	d := mustParse(t, "---\ntitle: Re: the plan\nwhen: 10:30\n---")
	if d.Props[0].Value != "Re: the plan" || d.Props[1].Value != "10:30" {
		t.Errorf("props = %+v", d.Props)
	}
}

func TestUnknownPartsAreReadOnlyAndKept(t *testing.T) {
	text := "---\n# a comment\nnested:\n  a: 1\n  b: 2\nblock: |\n  line one\n  line two\nmap: {a: 1}\nbroken: [a, [b]]\nlong: one\n  two\ntags: [x]\n---\n"
	d := mustParse(t, text)
	var kinds []Kind
	for _, p := range d.Props {
		kinds = append(kinds, p.Kind)
	}
	want := []Kind{ReadOnly, ReadOnly, ReadOnly, ReadOnly, ReadOnly, Tags}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	nested := d.Props[0]
	if !reflect.DeepEqual(nested.Render(), []string{"nested:", "  a: 1", "  b: 2"}) || nested.Editable() {
		t.Errorf("nested = %+v", nested)
	}
	if nested.Line != 2 || nested.End != 4 {
		t.Errorf("nested lines = %d-%d", nested.Line, nested.End)
	}
}

func TestListsWithNoiseAreReadOnly(t *testing.T) {
	for _, c := range []string{
		"l:\n  - a\n  # note\n  - b",
		"l:\n  - a # why\n  - b",
		"l:\n  - k: v",
		"l: [a, b] extra",
	} {
		d := mustParse(t, "---\n"+c+"\n---")
		if len(d.Props) != 1 || d.Props[0].Kind != ReadOnly {
			t.Errorf("%q: %+v", c, d.Props)
			continue
		}
		if got := strings.Join(d.Props[0].Render(), "\n"); got != c {
			t.Errorf("%q rewritten as %q", c, got)
		}
	}
}

func TestMalformedIsNotFrontMatter(t *testing.T) {
	for _, c := range []string{
		"",
		"---",
		"----\na: b\n---",
		"---\na: b",              // never closed
		"---\nSome intro\n---\n", // a paragraph between two rules
		"---\na: b\nstray\n---",
		"---\n- item\n---", // a list with no key
		"---\n\"open: x\n---",
		"text\n---\na: b\n---",
	} {
		if d, ok := Parse(c); ok {
			t.Errorf("%q read as front matter: %+v", c, d)
		}
	}
	if _, ok := Parse("---\n---\n"); !ok {
		t.Error("empty front matter should be one")
	}
	if _, ok := Parse("---\r\na: b\r\n---\r\n"); !ok {
		t.Error("CRLF front matter should be one")
	}
}

func TestClosingDots(t *testing.T) {
	d := mustParse(t, "---\na: b\n...\nbody")
	if d.End != 2 || len(d.Props) != 1 {
		t.Errorf("doc = %+v", d)
	}
}

func TestEditingChangesOnlyItsOwnLines(t *testing.T) {
	d := mustParse(t, "---\ntitle: Old\n# keep me\nnested:\n  a: 1\ntags:\n  - x\n---\n")
	p := d.Props[0]
	p.Value = "New: title"
	if got := p.Render(); !reflect.DeepEqual(got, []string{`title: "New: title"`}) {
		t.Errorf("title = %q", got)
	}
	tags := d.Props[2]
	tags.Items = append(tags.Items, "y", "with space")
	want := []string{"tags:", "  - x", "  - y", "  - with space"}
	if got := tags.Render(); !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %q", got)
	}
	tags.Items = nil
	if got := tags.Render(); !reflect.DeepEqual(got, []string{"tags: []"}) {
		t.Errorf("no tags = %q", got)
	}
}

func TestListStyleIsKept(t *testing.T) {
	d := mustParse(t, "---\nf: [a, b]\nb:\n    - a\n---")
	f, b := d.Props[0], d.Props[1]
	f.Items = append(f.Items, "c, d")
	if got := f.Render(); !reflect.DeepEqual(got, []string{`f: [a, b, "c, d"]`}) {
		t.Errorf("flow = %q", got)
	}
	b.Items = append(b.Items, "z")
	if got := b.Render(); !reflect.DeepEqual(got, []string{"b:", "    - a", "    - z"}) {
		t.Errorf("block = %q", got)
	}
}

func TestScalarTagsAreLists(t *testing.T) {
	d := mustParse(t, "---\ntags: one, two\naliases: Solo\n---")
	if d.Props[0].Kind != Tags || !reflect.DeepEqual(d.Props[0].Items, []string{"one", "two"}) {
		t.Errorf("tags = %+v", d.Props[0])
	}
	if d.Props[1].Kind != Aliases || !reflect.DeepEqual(d.Props[1].Items, []string{"Solo"}) {
		t.Errorf("aliases = %+v", d.Props[1])
	}
	empty := mustParse(t, "---\ntags:\n---")
	if empty.Props[0].Kind != Tags || len(empty.Props[0].Items) != 0 {
		t.Errorf("empty tags = %+v", empty.Props[0])
	}
}

func TestTagsOfTheDoc(t *testing.T) {
	d := mustParse(t, "---\ntags: [\"#one\", two]\nother: [x]\n---")
	if got := d.Tags(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("tags = %q", got)
	}
}

func TestNewProp(t *testing.T) {
	cases := []struct {
		key  string
		kind Kind
		want string
	}{
		{"words", Text, "words:"},
		{"n", Number, "n: 0"},
		{"ok", Bool, "ok: false"},
		{"list", List, "list: []"},
		{"Tags", List, "Tags: []"},
		{"a: b", Text, `"a: b":`},
	}
	for _, c := range cases {
		p := NewProp(c.key, c.kind)
		if got := p.Render()[0]; got != c.want {
			t.Errorf("%s -> %q, want %q", c.key, got, c.want)
		}
	}
	if p := NewProp("tags", Text); p.Kind != Tags {
		t.Errorf("tags key kind = %d", p.Kind)
	}
}

func TestDates(t *testing.T) {
	for s, want := range map[string]bool{
		"2026-09-29": true, "2026-02-30": false, "2026-9-1": false, "26-09-29": false,
	} {
		if IsDate(s) != want {
			t.Errorf("IsDate(%q) = %v", s, !want)
		}
	}
}

func TestParseNeverPanics(t *testing.T) {
	for _, s := range []string{
		"---\n:\n---", "---\n: x\n---", "---\n\"\n---", "---\n'\n---", "---\nk: [\n---", "---\nk: \"\\u\n---",
		"---\nk: \"\\ud\"\n---", "---\n-\n---", "---\n\tk: v\n---", "---\nk:\n\t- a\n---", "---\n\x00\n---",
	} {
		if d, ok := Parse(s); ok {
			for _, p := range d.Props {
				p.Render()
			}
		}
	}
}

func BenchmarkParse(b *testing.B) {
	text := "---\ntitle: A note\ntags: [a, b]\ndue: 2026-01-01\n---\n" + strings.Repeat("body line\n", 400)
	for i := 0; i < b.N; i++ {
		Parse(text)
	}
}
