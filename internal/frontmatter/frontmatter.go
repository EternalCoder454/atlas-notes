// Package frontmatter reads and writes the YAML front matter at the top of a
// note: the lines between a "---" on line 1 and the next "---".
//
// It is a hand-written reader for the small part of YAML notes use, not a YAML
// library. It understands `key: value` lines whose value is a plain or quoted
// scalar or a list (`[a, b]`, or `- a` lines under the key), and it leaves
// everything else alone: a nested map, a block scalar, a comment or a line it
// cannot place is kept as written, shown read-only, and never rewritten. A block
// whose top level holds something that is not a key at all is not front matter as
// far as this package goes (Parse says no), so a note that opens with a horizontal
// rule and a paragraph is not mistaken for one.
//
// Nothing here knows about the editor. Rendering a property gives the lines to
// put in its place, and touching only those lines is what keeps the keys, their
// order and the parts this package does not understand exactly as they were.
package frontmatter

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// maxLines is how far into a note the closing "---" is looked for. Front matter
// is a handful of lines; a note that has none within this many is not one.
const maxLines = 200

// Kind is what a property's value is taken to be, from how it is written.
type Kind uint8

const (
	Text     Kind = iota // a plain or quoted scalar; an empty value is text too
	Number               // 12, -3.5, 1e3
	Date                 // 2026-09-29
	Bool                 // true or false
	List                 // [a, b] or "- a" lines
	Tags                 // the list under "tags"
	Aliases              // the list under "aliases"
	ReadOnly             // anything else: shown, never rewritten
)

// Prop is one key of the front matter.
type Prop struct {
	Key    string // the key, unquoted
	KeyRaw string // the key as written
	Kind   Kind
	// Value is the scalar of a Text, Number, Date or Bool, unquoted. Items are the
	// entries of a List, Tags or Aliases.
	Value string
	Items []string
	// Flow says the list was written as [a, b]; Indent is what stands before the
	// "- " of a list written a line to an item.
	Flow   bool
	Indent string
	// Comment is a trailing " # note" after a one-line value, kept when the line is
	// written again.
	Comment string
	// Line and End are the first and last line of the property in the note (both
	// included), and Lines the text of them as written.
	Line, End int
	Lines     []string
}

// Doc is a note's front matter.
type Doc struct {
	Props []Prop
	// End is the line of the closing "---". Lines holds lines 0 through End.
	End   int
	Lines []string
}

var (
	numberRe = regexp.MustCompile(`^[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?$`)
	dateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	itemRe   = regexp.MustCompile(`^(\s*)-(\s+(.*))?$`)
)

// IsNumber reports whether s is written as a number.
func IsNumber(s string) bool { return numberRe.MatchString(s) }

// IsDate reports whether s is a calendar date written YYYY-MM-DD.
func IsDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Has reports whether text starts with a front matter block: a "---" first line.
// It is what the editor asks on every edit, so it costs one comparison.
func Has(text string) bool {
	if !strings.HasPrefix(text, "---") {
		return false
	}
	rest := text[3:]
	return rest == "" || rest[0] == '\n' || rest[0] == '\r' || rest[0] == ' ' || rest[0] == '\t'
}

// Parse reads the front matter at the top of text. ok is false when there is
// none, or when what is there is not front matter (see the package comment).
func Parse(text string) (*Doc, bool) {
	if !Has(text) {
		return nil, false
	}
	var lines []string
	rest := text
	for len(lines) < maxLines {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			lines = append(lines, rest)
			break
		}
		lines = append(lines, rest[:i])
		rest = rest[i+1:]
	}
	return ParseLines(lines)
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func fence(s string) bool { return strings.TrimRight(s, " \t\r") == "---" }

// ParseLines is Parse for a note already split into lines.
func ParseLines(lines []string) (*Doc, bool) {
	if len(lines) < 2 || !fence(lines[0]) {
		return nil, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t\r"); t == "---" || t == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	d := &Doc{End: end, Lines: append([]string(nil), lines[:end+1]...)}
	for i := 1; i < end; {
		line := lines[i]
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			i++
			continue
		}
		keyRaw, key, rest, ok := splitKey(line)
		if !ok {
			return nil, false // a top-level line that is no key: not front matter
		}
		// What follows the key line and belongs to it: indented lines, and "- "
		// lines at the key's own level. Blank and comment lines between them are
		// taken in only when more of the property comes after.
		j := i + 1
		last := i
		for j < end {
			l := lines[j]
			if blank(l) || (strings.HasPrefix(strings.TrimSpace(l), "#") && (l[0] == ' ' || l[0] == '\t')) {
				j++
				continue
			}
			if l[0] == ' ' || l[0] == '\t' || l == "-" || strings.HasPrefix(l, "- ") {
				j++
				last = j - 1
				continue
			}
			break
		}
		p := readProp(keyRaw, key, rest, lines[i+1:last+1])
		p.Line, p.End = i, last
		p.Lines = append([]string(nil), lines[i:last+1]...)
		d.Props = append(d.Props, p)
		i = last + 1
	}
	return d, true
}

// splitKey cuts "key: rest" into the key as written, the key unquoted and what
// follows the colon. A key is a plain word run up to a ": " (or a colon that ends
// the line), or a quoted string.
func splitKey(line string) (keyRaw, key, rest string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return
	}
	if line[0] == '"' || line[0] == '\'' {
		v, n, closed := unquote(line)
		if !closed {
			return
		}
		after := strings.TrimLeft(line[n:], " \t")
		if !strings.HasPrefix(after, ":") || (len(after) > 1 && after[1] != ' ' && after[1] != '\t') {
			return
		}
		return line[:n], v, strings.TrimSpace(after[1:]), true
	}
	if strings.ContainsRune("-?:,[]{}#&*!|>%@`", rune(line[0])) {
		if (line[0] != '-' && line[0] != '?' && line[0] != ':') || (len(line) > 1 && (line[1] == ' ' || line[1] == '\t')) {
			return
		}
	}
	for i := 0; i < len(line); i++ {
		if line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t') {
			k := strings.TrimRight(line[:i], " \t")
			if k == "" {
				return
			}
			return k, k, strings.TrimSpace(line[i+1:]), true
		}
	}
	return
}

// unquote reads the quoted scalar at the start of s. It returns the text
// inside, how many bytes the scalar took (quotes included) and whether it was
// closed.
func unquote(s string) (string, int, bool) {
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'' && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), i + 1, true
		case q == '"' && c == '"':
			return b.String(), i + 1, true
		case q == '"' && c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case 'x', 'u', 'U':
				n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[i]]
				if i+n < len(s) {
					var v rune
					good := true
					for _, h := range s[i+1 : i+1+n] {
						d := strings.IndexRune("0123456789abcdef", h|0x20)
						if h > 0x7f || d < 0 {
							good = false
							break
						}
						v = v*16 + rune(d)
					}
					if good {
						b.WriteRune(v)
						i += n
						continue
					}
				}
				b.WriteByte('\\')
				b.WriteByte(s[i])
			default: // \" \\ \/ and anything else: the character itself
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", len(s), false
}

// stripComment cuts a trailing " # comment" off an unquoted value. It returns the
// value, trimmed, and the comment with the spaces that led it.
func stripComment(s string) (value, comment string) {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			j := i
			for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
				j--
			}
			return s[:j], s[j:]
		}
	}
	return s, ""
}

// listKind is the kind of list a key holds.
func listKind(key string) Kind {
	switch strings.ToLower(key) {
	case "tags", "tag":
		return Tags
	case "aliases", "alias":
		return Aliases
	}
	return List
}

// readProp works out what a key's value is. cont is the lines after the key's.
func readProp(keyRaw, key, rest string, cont []string) Prop {
	p := Prop{Key: key, KeyRaw: keyRaw, Kind: ReadOnly}
	lk := listKind(key)

	// A comment straight after the colon is a comment on an empty value.
	if strings.HasPrefix(rest, "#") {
		rest, p.Comment = "", " "+rest
	}
	switch {
	case rest == "":
		if len(cont) == 0 {
			if lk != List {
				p.Kind, p.Indent = lk, "  "
			} else {
				p.Kind = Text
			}
			return p
		}
		if p.Comment != "" {
			return p
		}
		items, indent, ok := blockItems(cont)
		if !ok {
			return p
		}
		p.Kind, p.Items, p.Indent = lk, items, indent
	case len(cont) > 0:
		// A value that goes on over more lines: not one this reads.
	case rest[0] == '[':
		v, c := stripComment(rest)
		if !strings.HasSuffix(v, "]") {
			return p
		}
		items, ok := flowItems(v[1 : len(v)-1])
		if !ok {
			return p
		}
		p.Kind, p.Items, p.Flow, p.Comment = lk, items, true, c
	case strings.ContainsRune("{|>&*!%@`", rune(rest[0])):
		// A map, a block scalar, an anchor, a tag: not one this reads.
	case rest[0] == '"' || rest[0] == '\'':
		v, n, closed := unquote(rest)
		if !closed {
			return p
		}
		after := strings.TrimSpace(rest[n:])
		if after != "" {
			if !strings.HasPrefix(after, "#") {
				return p
			}
			p.Comment = " " + after
		}
		if lk != List {
			p.Kind, p.Items, p.Indent = lk, splitList(v), "  "
			return p
		}
		p.Kind, p.Value = Text, v
	default:
		v, c := stripComment(rest)
		p.Comment = c
		switch {
		case lk != List:
			p.Kind, p.Items, p.Indent = lk, splitList(v), "  "
		case v == "true" || v == "false":
			p.Kind, p.Value = Bool, v
		case IsNumber(v):
			p.Kind, p.Value = Number, v
		case IsDate(v):
			p.Kind, p.Value = Date, v
		default:
			p.Kind, p.Value = Text, v
		}
	}
	return p
}

// splitList cuts a scalar held by a list key ("a, b" or "a b") into its entries.
func splitList(s string) []string {
	f := strings.Split(s, ",")
	if !strings.Contains(s, ",") {
		f = strings.Fields(s)
	}
	var out []string
	for _, x := range f {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// blockItems reads the "- a" lines under a key. ok is false for anything else in
// them: a nested item, a comment, a blank line, a scalar that does not stand alone.
func blockItems(cont []string) (items []string, indent string, ok bool) {
	for n, l := range cont {
		m := itemRe.FindStringSubmatch(l)
		if m == nil {
			return nil, "", false
		}
		if n == 0 {
			indent = m[1]
		} else if m[1] != indent {
			return nil, "", false
		}
		v, ok := scalarItem(m[3])
		if !ok {
			return nil, "", false
		}
		items = append(items, v)
	}
	return items, indent, len(items) > 0
}

// scalarItem reads one list entry: a scalar with nothing after it.
func scalarItem(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	switch s[0] {
	case '"', '\'':
		v, n, closed := unquote(s)
		return v, closed && n == len(s)
	case '[', '{', '|', '>', '&', '*', '!', '%', '@', '`', '#':
		return "", false
	}
	if v, c := stripComment(s); c != "" || strings.Contains(v, ": ") || strings.HasSuffix(v, ":") {
		return "", false
	}
	return s, true
}

// flowItems reads the inside of [a, "b, c", d].
func flowItems(s string) ([]string, bool) {
	var out []string
	for i := 0; ; {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return out, true // also the empty list, and a trailing comma
		}
		var v string
		switch s[i] {
		case '"', '\'':
			x, n, closed := unquote(s[i:])
			if !closed {
				return nil, false
			}
			v, i = x, i+n
		case '[', '{', ']', '}', '#', '&', '*', '!', '|', '>':
			return nil, false
		default:
			j := i
			for j < len(s) && s[j] != ',' {
				if s[j] == '[' || s[j] == '{' || s[j] == ']' || s[j] == '}' {
					return nil, false
				}
				j++
			}
			v, i = strings.TrimSpace(s[i:j]), j
			if strings.Contains(v, ": ") {
				return nil, false
			}
		}
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] != ',' {
			return nil, false
		}
		i++
		if v != "" {
			out = append(out, v)
		}
	}
}

// ---- Writing -----------------------------------------------------------------

// Editable reports whether the property can be rewritten.
func (p *Prop) Editable() bool { return p.Kind != ReadOnly }

// looksTyped reports whether a plain scalar would be read as something other
// than text: a number, a date, a boolean or null. A text value that does is
// quoted, so that it stays text.
func looksTyped(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "~", "yes", "no", "on", "off", "y", "n":
		return true
	}
	return IsNumber(s) || IsDate(s)
}

// plain reports whether s can be written without quotes. flow is for a value
// inside [ ], where a comma and the brackets would end it.
func plain(s string, flow bool) bool {
	if s == "" || s != strings.TrimSpace(s) || !utf8.ValidString(s) {
		return false
	}
	switch s[0] {
	case '-', '?', ':':
		if len(s) == 1 || s[1] == ' ' {
			return false
		}
	case ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return false
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") ||
		strings.Contains(s, ":\t") || strings.Contains(s, "\t#") {
		return false
	}
	if flow && strings.ContainsAny(s, ",[]{}") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	return true
}

// quote writes s as a double-quoted scalar.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// scalar writes a value: as it is when that reads back the same, quoted when not.
// keepText quotes a value that would otherwise read back as a number, a date or a
// boolean.
func scalar(s string, flow, keepText bool) string {
	if plain(s, flow) && !(keepText && looksTyped(s)) {
		return s
	}
	return quote(s)
}

// KeyText writes a key that a person typed. A key that would not read back as
// itself is quoted.
func KeyText(k string) string {
	k = strings.TrimSpace(k)
	if plain(k, false) {
		return k
	}
	return quote(k)
}

// NewProp is a property that has just been added, with the value its kind starts
// from. A key of "tags" or "aliases" is the list kind of that name whatever kind
// was asked for, as it would be read back.
func NewProp(key string, kind Kind) Prop {
	p := Prop{Key: strings.TrimSpace(key), KeyRaw: KeyText(key), Kind: kind, Indent: "  "}
	if lk := listKind(p.Key); lk != List {
		p.Kind = lk
	}
	switch p.Kind {
	case Number:
		p.Value = "0"
	case Bool:
		p.Value = "false"
	}
	return p
}

// Render is the lines the property is written as. A read-only property is its
// own lines, untouched.
func (p *Prop) Render() []string {
	switch p.Kind {
	case ReadOnly:
		return append([]string(nil), p.Lines...)
	case List, Tags, Aliases:
		if len(p.Items) == 0 {
			return []string{p.KeyRaw + ": []" + p.Comment}
		}
		if p.Flow {
			parts := make([]string, len(p.Items))
			for i, it := range p.Items {
				parts[i] = scalar(it, true, false)
			}
			return []string{p.KeyRaw + ": [" + strings.Join(parts, ", ") + "]" + p.Comment}
		}
		out := []string{p.KeyRaw + ":" + p.Comment}
		for _, it := range p.Items {
			out = append(out, p.Indent+"- "+scalar(it, false, false))
		}
		return out
	}
	v := p.Value
	switch {
	case v == "":
	case p.Kind == Number && IsNumber(v), p.Kind == Date && IsDate(v), p.Kind == Bool && (v == "true" || v == "false"):
	default:
		v = scalar(v, false, true)
	}
	if v == "" {
		return []string{p.KeyRaw + ":" + p.Comment}
	}
	return []string{p.KeyRaw + ": " + v + p.Comment}
}

// Tags is the tags the front matter gives the note, without any "#".
func (d *Doc) Tags() []string {
	var out []string
	for _, p := range d.Props {
		if p.Kind != Tags {
			continue
		}
		for _, t := range p.Items {
			if t = strings.TrimLeft(strings.TrimSpace(t), "#"); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}
