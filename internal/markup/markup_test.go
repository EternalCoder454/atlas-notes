package markup

import (
	"reflect"
	"strings"
	"testing"
)

func kinds(spans []Span) []string {
	var out []string
	for _, s := range spans {
		k := map[Kind]string{KindWikiLink: "link", KindTag: "tag", KindImage: "image", KindURL: "url"}[s.Kind]
		out = append(out, k+":"+s.Target)
	}
	return out
}

func TestLineFindsEachKind(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"see [[Project Plan]] and [[Work/Todo|the list]]", []string{"link:Project Plan", "link:Work/Todo"}},
		{"[[Plan#Budget]]", []string{"link:Plan"}},
		{"#idea and #project/atlas, (#paren)", []string{"tag:idea", "tag:project/atlas", "tag:paren"}},
		{"![a cat](../attachments/cat%20photo.png)", []string{"image:../attachments/cat photo.png"}},
		{"![[diagram.png]] and ![[Other Note]]", []string{"image:diagram.png", "link:Other Note"}},
		{"read https://example.org/a_(b). now", []string{"url:https://example.org/a_(b)"}},
		{"read https://example.org/x.", []string{"url:https://example.org/x"}},
		{"[site](https://example.org) ok", []string{"url:https://example.org"}},
		{"![alt](<my pic.png>)", []string{"image:my pic.png"}},
		{`![alt](pic.png "a title")`, []string{"image:pic.png"}},
	}
	for _, c := range cases {
		if got := kinds(Line(c.line, false)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Line(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestWhatIsNotATag(t *testing.T) {
	for _, line := range []string{
		"# Heading",
		"## Heading",
		"issue #12 and year #2026",
		"C# and page#section",
		"a url https://x.org/#frag",
		"`#code` only",
		"#/",
	} {
		for _, s := range Line(line, false) {
			if s.Kind == KindTag {
				t.Errorf("%q: found tag %q", line, s.Target)
			}
		}
	}
}

func TestTrailingSlashIsNotPartOfATag(t *testing.T) {
	got := kinds(Line("#a/b/ next", false))
	if !reflect.DeepEqual(got, []string{"tag:a/b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestNothingCountsInsideCode(t *testing.T) {
	text := "real #tag\n```\n#nottag [[NotLink]]\n```\n`[[inline]]` [[Real]]\n~~~\n#no\n~~~"
	s := Summarize(text)
	if !reflect.DeepEqual(s.Tags, []string{"tag"}) {
		t.Errorf("tags = %v", s.Tags)
	}
	if !reflect.DeepEqual(s.Links, []string{"Real"}) {
		t.Errorf("links = %v", s.Links)
	}
	fence := InCodeFence(text)
	want := []bool{false, true, true, true, false, true, true, true}
	if !reflect.DeepEqual(fence, want) {
		t.Errorf("InCodeFence = %v, want %v", fence, want)
	}
}

func TestSummarizeFoldsTagCaseAndDedupes(t *testing.T) {
	s := Summarize("#Idea #idea #IDEA [[A]] [[a]] [[B]]")
	if !reflect.DeepEqual(s.Tags, []string{"idea"}) {
		t.Errorf("tags = %v", s.Tags)
	}
	if !reflect.DeepEqual(s.Links, []string{"A", "B"}) {
		t.Errorf("links = %v", s.Links)
	}
}

func TestTagsInOtherScripts(t *testing.T) {
	got := kinds(Line("#café #日本語 #über_2", false))
	want := []string{"tag:café", "tag:日本語", "tag:über_2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSpanOffsetsCoverTheConstruct(t *testing.T) {
	line := "x [[A|b]] y #t z"
	for _, s := range Line(line, false) {
		switch s.Kind {
		case KindWikiLink:
			if line[s.Start:s.End] != "[[A|b]]" {
				t.Errorf("link span %q", line[s.Start:s.End])
			}
		case KindTag:
			if line[s.Start:s.End] != "#t" {
				t.Errorf("tag span %q", line[s.Start:s.End])
			}
		}
	}
}

func TestUnclosedThingsAreText(t *testing.T) {
	for _, line := range []string{"[[never closed", "![alt](no close", "[[]]", "[[ | alias]]", "[text] (space)"} {
		if got := Line(line, false); len(got) != 0 {
			t.Errorf("Line(%q) = %v, want nothing", line, kinds(got))
		}
	}
}

func TestResolve(t *testing.T) {
	notes := []string{"Todo", "Work/Todo", "Work/Deep/Plan", "Archive/Plan", "Ideas"}
	cases := map[string]string{
		"todo":           "Todo",
		"Work/Todo":      "Work/Todo",
		"plan":           "Archive/Plan", // both at depth >= 1: shallower wins, then alphabetical
		"Work/Deep/Plan": "Work/Deep/Plan",
		"missing":        "",
		"Work/Missing":   "",
		"  Ideas ":       "Ideas",
	}
	for target, want := range cases {
		if got := Resolve(target, notes); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestRewriteLinksKeepsHeadingAndAlias(t *testing.T) {
	before := []string{"Plan", "Other"}
	after := []string{"Budget 2027", "Other"}
	text := "a [[Plan]] b [[plan#Q3|the plan]] c ![[Plan]] d [[Other]]\n```\n[[Plan]]\n```"
	got, n := RewriteLinks(text, "Plan", "Budget 2027", before, after)
	want := "a [[Budget 2027]] b [[Budget 2027#Q3|the plan]] c ![[Budget 2027]] d [[Other]]\n```\n[[Plan]]\n```"
	if got != want || n != 3 {
		t.Fatalf("got %q (%d), want %q", got, n, want)
	}
}

func TestRewriteLinksDoesNotCaptureAnotherNotesName(t *testing.T) {
	// "Todo" at the root is what [[Todo]] means. Renaming Work/Todo must not
	// touch it, but a link written with Work/Todo's path must follow.
	before := []string{"Todo", "Work/Todo"}
	after := []string{"Todo", "Work/Tasks"}
	got, n := RewriteLinks("[[Todo]] [[Work/Todo]]", "Work/Todo", "Work/Tasks", before, after)
	if got != "[[Todo]] [[Work/Tasks]]" || n != 1 {
		t.Fatalf("got %q (%d)", got, n)
	}
}

func TestRewriteLinksUsesThePathWhenTheNewNameIsTaken(t *testing.T) {
	// After the rename two notes are called "Notes"; the bare name would mean
	// the root one, so the link has to spell out the path.
	before := []string{"Notes", "Work/Draft"}
	after := []string{"Notes", "Work/Notes"}
	got, _ := RewriteLinks("[[Draft]]", "Work/Draft", "Work/Notes", before, after)
	if got != "[[Work/Notes]]" {
		t.Fatalf("got %q", got)
	}
}

func TestLineNeverPanics(t *testing.T) {
	weird := []string{"", "!", "![", "![[", "[[", "#", "`", "``", "[](", "![](", "h", "http://", "https://)", strings.Repeat("[", 50), "\xff\xfe#t"}
	for _, w := range weird {
		Line(w, false)
		Summarize(w)
	}
}

func FuzzLine(f *testing.F) {
	for _, s := range []string{"[[a|b]] #t ![x](y) https://z", "![[p.png]]", "`#x` #y"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		for _, s := range Line(line, false) {
			if s.Start < 0 || s.End > len(line) || s.Start >= s.End {
				t.Fatalf("bad span %+v in %q", s, line)
			}
		}
		RewriteLinks(line, "a", "b", []string{"a"}, []string{"b"})
	})
}

func TestSummarizeCountsFrontMatterTags(t *testing.T) {
	text := "---\ntitle: T\ntags:\n  - Alpha\n  - \"#beta\"\n  - two words\n  - 2026\n---\nBody #alpha #gamma\n"
	got := Summarize(text).Tags
	want := []string{"alpha", "gamma", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Tags = %v, want %v", got, want)
	}
	if got := Summarize("---\ntags: [x, y]\n---").Tags; strings.Join(got, ",") != "x,y" {
		t.Errorf("flow tags = %v", got)
	}
	// Front matter that is not front matter gives nothing.
	if got := Summarize("---\ntags: [x]\nstray line\n---").Tags; len(got) != 0 {
		t.Errorf("malformed front matter gave tags %v", got)
	}
}
