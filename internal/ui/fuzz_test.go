package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzMarkdownToPango guards the assistant's reply renderer. Its output goes
// straight into GtkLabel.SetMarkup, so anything the model emits must come back
// as escaped text with only the tags this function adds — an unbalanced or
// injected tag makes the label render as literal markup or drop the reply.
func FuzzMarkdownToPango(f *testing.F) {
	for _, seed := range []string{
		"plain", "**bold**", "*it*", "`code`", "# Heading", "- bullet",
		"<b>not mine</b>", "a & b", "<script>x</script>", "```\nfence\n```",
		"**unclosed", "`unclosed", "<<<>>>", "&amp;", "&", "<", ">", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, md string) {
		if !utf8.ValidString(md) {
			return
		}
		out := markdownToPango(md)
		// Every markup tag in the output must be one this function generates.
		for i := 0; i < len(out); i++ {
			if out[i] != '<' {
				continue
			}
			end := strings.IndexByte(out[i:], '>')
			if end < 0 {
				t.Fatalf("unterminated tag in output for %q: %q", md, out)
			}
			tag := out[i : i+end+1]
			switch tag {
			case "<b>", "</b>", "<i>", "</i>", "<tt>", "</tt>":
			default:
				t.Fatalf("unexpected markup %q in output for input %q", tag, md)
			}
			i += end
		}
	})
}
