package export

import (
	"fmt"
	"html"
	"strings"
)

// Format is a kind of document a note can be exported as.
type Format struct {
	ID   string // what callers ask for: "md", "odt", "docx", "html", "txt"
	Name string // what a person is shown
	Ext  string // with its dot
	MIME string
}

// Formats lists every format, in the order they are offered.
var Formats = []Format{
	{"docx", "Word document", ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{"odt", "OpenDocument text", ".odt", "application/vnd.oasis.opendocument.text"},
	{"md", "Markdown", ".md", "text/markdown"},
	{"html", "Web page", ".html", "text/html"},
	{"txt", "Plain text", ".txt", "text/plain"},
}

// Lookup finds a format by its ID.
func Lookup(id string) (Format, bool) {
	for _, f := range Formats {
		if f.ID == id {
			return f, true
		}
	}
	return Format{}, false
}

// Render turns a note into a document of the given format. title is the
// note's name, which goes into the document's own properties where the format
// has somewhere to put it.
func Render(formatID, title, markdown string) ([]byte, error) {
	switch formatID {
	case "md":
		// The note as it is, comments included: they are invisible to any
		// Markdown reader, and keeping them means a note exported and brought
		// back into a vault keeps its priorities and due dates.
		out := markdown
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return []byte(out), nil
	case "txt":
		return []byte(renderText(parse(markdown))), nil
	case "html":
		return []byte(renderHTML(title, parse(markdown))), nil
	case "docx":
		return renderDOCX(title, parse(markdown))
	case "odt":
		return renderODT(title, parse(markdown))
	}
	return nil, fmt.Errorf("export: no format %q", formatID)
}

// FileName is a name to save an export under: the note's, with the format's
// extension, and without the characters that some systems will not accept in
// a file name.
func FileName(title string, f Format) string {
	name := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, title)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Note"
	}
	return name + f.Ext
}

// checkbox is how a checklist item's box is drawn in a document that has no
// checkbox of its own.
func checkbox(checked bool) string {
	if checked {
		return "☑ " // ☑
	}
	return "☐ " // ☐
}

// ---- plain text ----------------------------------------------------------

func renderText(blocks []block) string {
	var b strings.Builder
	for i, bl := range blocks {
		if i > 0 && !(isListItem(bl) && isListItem(blocks[i-1])) {
			b.WriteString("\n")
		}
		indent := strings.Repeat("  ", bl.level)
		switch bl.kind {
		case heading, paragraph:
			for _, l := range bl.lines {
				b.WriteString(plainText(l) + "\n")
			}
		case quote:
			for _, l := range bl.lines {
				b.WriteString("  " + plainText(l) + "\n")
			}
		case bullet:
			b.WriteString(indent + "• " + plainText(bl.lines[0]) + "\n")
		case numbered:
			b.WriteString(fmt.Sprintf("%s%d. %s\n", indent, bl.number, plainText(bl.lines[0])))
		case task:
			line := indent + checkbox(bl.checked) + plainText(bl.lines[0])
			if bl.detail != "" {
				line += " (" + bl.detail + ")"
			}
			b.WriteString(line + "\n")
		case code:
			for _, l := range strings.Split(bl.code, "\n") {
				b.WriteString("    " + l + "\n")
			}
		case divider:
			b.WriteString("\n")
		}
	}
	return b.String()
}

func isListItem(b block) bool {
	return b.kind == bullet || b.kind == numbered || b.kind == task
}

// ---- web page --------------------------------------------------------------

// htmlStyle keeps the page readable when opened on its own, and out of the way
// when it is pasted somewhere with styles of its own.
const htmlStyle = `body{font:16px/1.6 system-ui,sans-serif;max-width:46em;margin:2em auto;padding:0 1em;color:#1d1a22}
blockquote{margin:0;padding-left:1em;border-left:3px solid #ccc;color:#555}
pre,code{font-family:ui-monospace,monospace;background:#f3eff7;border-radius:4px}
pre{padding:.8em;overflow-x:auto}code{padding:0 .2em}
ul.tasks{list-style:none;padding-left:0}ul.tasks ul.tasks{padding-left:1.5em}
.detail{color:#666;font-size:.9em}.done{color:#777}
hr{border:0;border-top:1px solid #ccc}
@media(prefers-color-scheme:dark){body{background:#141218;color:#e6e0e9}pre,code{background:#2b2930}blockquote{color:#aaa}}`

func renderHTML(title string, blocks []block) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>" + htmlStyle + "</style>\n</head>\n<body>\n")
	writeHTMLBlocks(&b, blocks)
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func writeHTMLBlocks(b *strings.Builder, blocks []block) {
	for i := 0; i < len(blocks); {
		bl := blocks[i]
		switch bl.kind {
		case heading:
			fmt.Fprintf(b, "<h%d>%s</h%d>\n", bl.level, htmlRuns(bl.lines[0]), bl.level)
		case paragraph:
			b.WriteString("<p>" + htmlLines(bl.lines) + "</p>\n")
		case quote:
			b.WriteString("<blockquote><p>" + htmlLines(bl.lines) + "</p></blockquote>\n")
		case code:
			class := ""
			if bl.lang != "" {
				class = ` class="language-` + html.EscapeString(bl.lang) + `"`
			}
			b.WriteString("<pre><code" + class + ">" + html.EscapeString(bl.code) + "</code></pre>\n")
		case divider:
			b.WriteString("<hr>\n")
		default: // a run of list items, with their nesting
			j := i
			for j < len(blocks) && isListItem(blocks[j]) {
				j++
			}
			writeHTMLList(b, blocks[i:j], 0)
			i = j
			continue
		}
		i++
	}
}

// writeHTMLList writes consecutive list items, opening a nested list wherever
// an item is deeper than the one before it, and a new list wherever the kind
// of item changes.
func writeHTMLList(b *strings.Builder, items []block, depth int) {
	for i := 0; i < len(items); {
		k := items[i].kind
		j := i
		for j < len(items) && (items[j].level > depth || items[j].kind == k) {
			j++
		}
		switch k {
		case numbered:
			start := ""
			if items[i].number != 1 {
				start = fmt.Sprintf(` start="%d"`, items[i].number)
			}
			b.WriteString("<ol" + start + ">\n")
		case task:
			b.WriteString("<ul class=\"tasks\">\n")
		default:
			b.WriteString("<ul>\n")
		}
		for x := i; x < j; {
			it := items[x]
			y := x + 1
			for y < j && items[y].level > depth {
				y++
			}
			b.WriteString("<li>")
			switch {
			case it.kind == task && it.checked:
				b.WriteString(checkbox(true) + `<span class="done">` + htmlRuns(it.lines[0]) + "</span>")
			case it.kind == task:
				b.WriteString(checkbox(false) + htmlRuns(it.lines[0]))
			default:
				b.WriteString(htmlRuns(it.lines[0]))
			}
			if it.detail != "" {
				b.WriteString(` <span class="detail">(` + html.EscapeString(it.detail) + `)</span>`)
			}
			if y > x+1 {
				b.WriteString("\n")
				writeHTMLList(b, items[x+1:y], depth+1)
			}
			b.WriteString("</li>\n")
			x = y
		}
		switch k {
		case numbered:
			b.WriteString("</ol>\n")
		default:
			b.WriteString("</ul>\n")
		}
		i = j
	}
}

func htmlLines(lines [][]inline) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = htmlRuns(l)
	}
	return strings.Join(parts, "<br>\n")
}

func htmlRuns(runs []inline) string {
	var b strings.Builder
	for _, r := range runs {
		t := html.EscapeString(r.text)
		if r.mono {
			t = "<code>" + t + "</code>"
		}
		if r.strike {
			t = "<s>" + t + "</s>"
		}
		if r.italic {
			t = "<em>" + t + "</em>"
		}
		if r.bold {
			t = "<strong>" + t + "</strong>"
		}
		if r.link != "" {
			t = `<a href="` + html.EscapeString(safeLink(r.link)) + `">` + t + "</a>"
		}
		b.WriteString(t)
	}
	return b.String()
}

// safeLink keeps a link to the schemes a note can reasonably mean. A
// javascript: link in an exported page would run when clicked, and nothing a
// person wrote in a note should become that.
func safeLink(u string) string {
	l := strings.ToLower(strings.TrimSpace(u))
	for _, ok := range []string{"http://", "https://", "mailto:", "#", "/"} {
		if strings.HasPrefix(l, ok) {
			return strings.TrimSpace(u)
		}
	}
	if !strings.Contains(l, ":") {
		return strings.TrimSpace(u) // a relative link
	}
	return "#"
}
