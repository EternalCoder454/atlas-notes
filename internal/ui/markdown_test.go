package ui

import "testing"

func TestMarkdownToPango(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**bold**", "<b>bold</b>"},
		{"*em*", "<i>em</i>"},
		{"`code`", "<tt>code</tt>"},
		{"- item", "• item"},
		{"# Title", "<b>Title</b>"},
		{"a < b & c", "a &lt; b &amp; c"},
		{"```\ncode block\n```", "<tt>code block</tt>"},
	}
	for _, c := range cases {
		if got := markdownToPango(c.in); got != c.want {
			t.Errorf("markdownToPango(%q) = %q want %q", c.in, got, c.want)
		}
	}
}
