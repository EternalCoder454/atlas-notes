package editor

import (
	"reflect"
	"testing"
)

func TestSlashQuery(t *testing.T) {
	cases := []struct {
		before string
		q      string
		ok     bool
	}{
		{"/", "", true},
		{"/hea", "hea", true},
		{"some words /ta", "ta", true},
		{"/bullet li", "bullet li", true},
		{"a/b", "", false},
		{"and/or", "", false},
		{"https://example.com", "", false},
		{"see /usr/bin", "", false},
		{"a / b", "", false},
		{"", "", false},
		{"no slash", "", false},
		{"/this is far too long to be a command", "", false},
	}
	for _, c := range cases {
		q, ok := slashQuery(c.before)
		if q != c.q || ok != c.ok {
			t.Errorf("slashQuery(%q) = %q, %v; want %q, %v", c.before, q, ok, c.q, c.ok)
		}
	}
}

func labels(items []slashItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.label)
	}
	return out
}

func TestSlashMatches(t *testing.T) {
	if n := len(slashMatches("", true, true)); n != len(slashItems) {
		t.Errorf("empty query offers %d, want all %d", n, len(slashItems))
	}
	if got := labels(slashMatches("head", true, true)); !reflect.DeepEqual(got, []string{"Heading 1", "Heading 2"}) {
		t.Errorf("head: %v", got)
	}
	// A name match comes before a match on a search word.
	if got := labels(slashMatches("task", true, true)); len(got) < 2 || got[0] != "Task" {
		t.Errorf("task: %v", got)
	}
	if got := labels(slashMatches("bullet l", true, true)); !reflect.DeepEqual(got, []string{"Bullet list"}) {
		t.Errorf("bullet l: %v", got)
	}
	if got := slashMatches("zzz", true, true); len(got) != 0 {
		t.Errorf("zzz: %v", labels(got))
	}
	for _, it := range slashMatches("", false, false) {
		if it.action != "" || it.image {
			t.Errorf("%s offered without an app to handle it", it.label)
		}
	}
}

func TestCompleteSlash(t *testing.T) {
	item := func(label string) slashItem {
		for _, it := range slashItems {
			if it.label == label {
				return it
			}
		}
		t.Fatalf("no item %q", label)
		return slashItem{}
	}
	cases := []struct {
		before, label string
		want          completion
	}{
		{"/hea", "Heading 1", completion{before: 4, text: "# "}},
		{"/", "Callout", completion{before: 1, text: "> [!note] "}},
		{"words /ta", "Task", completion{before: 3, text: "\n- [ ] "}},
		{"words /li", "Link", completion{before: 3, text: "[["}},
		{"/code", "Code block", completion{before: 5, text: "```\n\n```", skip: -4}},
		{"/sum", "Summarise", completion{before: 4}},
	}
	for _, c := range cases {
		got, ok := completeSlash(c.before, item(c.label))
		if !ok || got != c.want {
			t.Errorf("completeSlash(%q, %s) = %+v, %v; want %+v", c.before, c.label, got, ok, c.want)
		}
	}
	if _, ok := completeSlash("plain", item("Task")); ok {
		t.Error("completed without a slash")
	}
	// The table's caret starts the first body cell.
	c, _ := completeSlash("/table", item("Table"))
	if c.skip >= 0 || c.text[:6] != "| Name" {
		t.Errorf("table: %+v", c)
	}
}

func TestSlashInContext(t *testing.T) {
	if k, q := suggestContext("/head"); k != suggestSlash || q != "head" {
		t.Errorf("got %v %q", k, q)
	}
	// Inside inline code a slash is code.
	if k, _ := suggestContext("`x /he"); k != suggestNone {
		t.Errorf("inline code: %v", k)
	}
	// An open [[ link keeps its own suggestions.
	if k, _ := suggestContext("[[Work/pl"); k != suggestNotes {
		t.Errorf("link: %v", k)
	}
}
