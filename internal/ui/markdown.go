package ui

import (
	"regexp"
	"strings"
)

var (
	mdCode   = regexp.MustCompile("`([^`]+)`")
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic = regexp.MustCompile(`\*([^*]+)\*`)
)

// pangoEscape escapes the characters that are special in Pango markup.
func pangoEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// markdownToPango converts the small Markdown subset the assistant emits — bold,
// italics, inline code, bullet lists and headings — into Pango markup for a
// GtkLabel. All literal text is escaped first, so the only markup in the result
// is the tags this function adds.
func markdownToPango(md string) string {
	lines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	for i, line := range lines {
		lines[i] = mdLine(line)
	}
	return strings.Join(lines, "\n")
}

func mdLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	bullet := ""
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		bullet = "• "
		trimmed = trimmed[2:]
	}

	heading := false
	for strings.HasPrefix(trimmed, "#") {
		heading = true
		trimmed = trimmed[1:]
	}
	if heading {
		trimmed = strings.TrimLeft(trimmed, " ")
	}

	content := mdInline(pangoEscape(trimmed))
	if heading {
		content = "<b>" + content + "</b>"
	}
	return indent + bullet + content
}

func mdInline(s string) string {
	s = mdCode.ReplaceAllString(s, "<tt>$1</tt>")
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	s = mdItalic.ReplaceAllString(s, "<i>$1</i>")
	return s
}
