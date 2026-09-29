// Package markup finds the parts of a note that point somewhere: links to
// other notes ([[Name]]), tags (#tag) and images (![alt](path)).
//
// It is the one definition of all three. The index uses it to answer "which
// notes link here" and "which notes carry this tag", the editor uses it to
// draw and click them, and renaming a note uses it to rewrite the links that
// named it. If any two of those disagreed about what counts as a tag, a tag
// the editor showed would be missing from the tag list, so they all ask here.
//
// Nothing inside code counts: not in a fenced block, not in `inline code`.
// A note about Markdown has to be able to write [[this]] or #that literally.
package markup

import (
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is what a span on a line is.
type Kind int

const (
	KindWikiLink Kind = iota // [[Target|Alias]]
	KindTag                  // #tag
	KindImage                // ![alt](path) or ![[image.png]]
	KindURL                  // a bare http(s) URL, or [text](url)
)

// Span is one thing found on a line. Start and End are byte offsets into the
// line, End exclusive, and cover the whole construct including its brackets.
type Span struct {
	Kind  Kind
	Start int
	End   int
	// Target is where it points: a note name or path for a wiki link, the
	// tag without its "#" for a tag, a path for an image, a URL for a link.
	Target string
	// Alias is a wiki link's display text, an image's alt text, or a Markdown
	// link's text. Empty when there is none.
	Alias string
	// Heading is the part of a wiki link after "#", if any.
	Heading string
}

// Line finds every span on one line of text. inCode says the line is inside
// a fenced code block, where nothing counts.
func Line(line string, inCode bool) []Span {
	if inCode {
		return nil
	}
	code := codeRanges(line)
	var out []Span
	for i := 0; i < len(line); {
		if in, end := within(code, i); in {
			i = end
			continue
		}
		c := line[i]
		switch {
		case c == '!' && strings.HasPrefix(line[i:], "![["):
			if s, ok := wikiAt(line, i+1); ok {
				s.Start = i
				if isImagePath(s.Target) {
					s.Kind = KindImage
				}
				out = append(out, s)
				i = s.End
				continue
			}
		case c == '!' && strings.HasPrefix(line[i:], "!["):
			if s, ok := mdLinkAt(line, i+1); ok {
				s.Start, s.Kind = i, KindImage
				out = append(out, s)
				i = s.End
				continue
			}
		case c == '[' && strings.HasPrefix(line[i:], "[["):
			if s, ok := wikiAt(line, i); ok {
				out = append(out, s)
				i = s.End
				continue
			}
		case c == '[':
			if s, ok := mdLinkAt(line, i); ok {
				s.Kind = KindURL
				out = append(out, s)
				i = s.End
				continue
			}
		case c == '#':
			if s, ok := tagAt(line, i); ok {
				out = append(out, s)
				i = s.End
				continue
			}
		case c == 'h' && (strings.HasPrefix(line[i:], "http://") || strings.HasPrefix(line[i:], "https://")):
			if i == 0 || !isWordByte(line[i-1]) {
				end := urlEnd(line, i)
				out = append(out, Span{Kind: KindURL, Start: i, End: end, Target: line[i:end]})
				i = end
				continue
			}
		}
		i++
	}
	return out
}

// Walk calls fn for every line of text with the spans found on it, keeping
// track of fenced code blocks across lines.
func Walk(text string, fn func(n int, line string, spans []Span)) {
	fence := ""
	for n, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			fn(n, line, nil)
			continue
		}
		if f := openFence(trimmed); f != "" {
			fence = f
			fn(n, line, nil)
			continue
		}
		fn(n, line, Line(line, false))
	}
}

// InCodeFence reports, for each line of text, whether it is part of a fenced
// code block (the fences included). The editor re-parses one line at a time
// and needs to know this without re-reading the whole note.
func InCodeFence(text string) []bool {
	lines := strings.Split(text, "\n")
	out := make([]bool, len(lines))
	fence := ""
	for n, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			out[n] = true
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if f := openFence(trimmed); f != "" {
			fence, out[n] = f, true
		}
	}
	return out
}

func openFence(trimmed string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			return f
		}
	}
	return ""
}

// Summary is what the index keeps about a note.
type Summary struct {
	Links  []string // wiki link targets, as written, without alias or heading
	Tags   []string // tags, lowercased, without "#", each once
	Images []string // image paths, as written and URL-decoded
}

// Summarize collects a note's links, tags and images.
func Summarize(text string) Summary {
	var s Summary
	seenTag := map[string]bool{}
	seenLink := map[string]bool{}
	Walk(text, func(_ int, _ string, spans []Span) {
		for _, sp := range spans {
			switch sp.Kind {
			case KindWikiLink:
				key := strings.ToLower(sp.Target)
				if !seenLink[key] {
					seenLink[key] = true
					s.Links = append(s.Links, sp.Target)
				}
			case KindTag:
				key := strings.ToLower(sp.Target)
				if !seenTag[key] {
					seenTag[key] = true
					s.Tags = append(s.Tags, key)
				}
			case KindImage:
				s.Images = append(s.Images, sp.Target)
			}
		}
	})
	return s
}

// wikiAt parses "[[Target#Heading|Alias]]" starting at i, which points at the
// first "[". A link never spans lines, and an empty target is not a link.
func wikiAt(line string, i int) (Span, bool) {
	rest := line[i+2:]
	end := strings.Index(rest, "]]")
	if end < 0 {
		return Span{}, false
	}
	inner := rest[:end]
	if strings.ContainsAny(inner, "[]") {
		return Span{}, false
	}
	target, alias, _ := strings.Cut(inner, "|")
	target, heading, _ := strings.Cut(target, "#")
	target = strings.TrimSpace(target)
	if target == "" {
		return Span{}, false
	}
	return Span{
		Kind: KindWikiLink, Start: i, End: i + 2 + end + 2,
		Target: target, Alias: strings.TrimSpace(alias), Heading: strings.TrimSpace(heading),
	}, true
}

// mdLinkAt parses "[text](target)" starting at i, which points at "[". The
// target may be wrapped in <angle brackets> to hold spaces, and may carry a
// "title" after it, which is dropped.
func mdLinkAt(line string, i int) (Span, bool) {
	close := matchingBracket(line, i)
	if close < 0 || close+1 >= len(line) || line[close+1] != '(' {
		return Span{}, false
	}
	text := line[i+1 : close]
	j := close + 2
	var target string
	if j < len(line) && line[j] == '<' {
		gt := strings.IndexByte(line[j:], '>')
		if gt < 0 {
			return Span{}, false
		}
		target = line[j+1 : j+gt]
		j += gt + 1
		k := strings.IndexByte(line[j:], ')')
		if k < 0 {
			return Span{}, false
		}
		j += k + 1
	} else {
		depth := 0
		k := j
		for ; k < len(line); k++ {
			if line[k] == '(' {
				depth++
			} else if line[k] == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
		}
		if k >= len(line) {
			return Span{}, false
		}
		target = line[j:k]
		if sp := strings.IndexAny(target, " \t"); sp >= 0 {
			target = target[:sp] // `path "title"`
		}
		j = k + 1
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return Span{}, false
	}
	if dec, err := url.PathUnescape(target); err == nil {
		target = dec
	}
	return Span{Start: i, End: j, Target: target, Alias: text}, true
}

// matchingBracket finds the "]" that closes the "[" at i, allowing nested
// brackets in the text, on the same line.
func matchingBracket(line string, i int) int {
	depth := 0
	for k := i; k < len(line); k++ {
		switch line[k] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// tagAt parses "#tag" at i. A tag starts at the beginning of the line or
// after a space or an opening bracket, so "page#section" and "C#" are not
// tags, and a heading ("# Title") is not either, because the "#" is followed
// by a space. It runs over letters, digits, "_", "-" and "/" (for nested tags
// such as #project/atlas), must contain a letter, so "#1" and "#2026" stay
// numbers, and does not end in "/".
func tagAt(line string, i int) (Span, bool) {
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(line[:i])
		if !unicode.IsSpace(prev) && prev != '(' && prev != '[' {
			return Span{}, false
		}
	}
	j := i + 1
	letter := false
	for j < len(line) {
		r, size := utf8.DecodeRuneInString(line[j:])
		if unicode.IsLetter(r) {
			letter = true
		} else if !unicode.IsDigit(r) && r != '_' && r != '-' && r != '/' {
			break
		}
		j += size
	}
	for j > i+1 && line[j-1] == '/' {
		j--
	}
	if !letter || j == i+1 {
		return Span{}, false
	}
	return Span{Kind: KindTag, Start: i, End: j, Target: line[i+1 : j]}, true
}

// urlEnd finds where a bare URL stops: at whitespace, or before trailing
// punctuation that belongs to the sentence ("see https://x.org.").
func urlEnd(line string, i int) int {
	j := i
	for j < len(line) && line[j] != ' ' && line[j] != '\t' && line[j] != '<' && line[j] != '>' {
		j++
	}
	for j > i && strings.ContainsRune(".,;:!?'\")]", rune(line[j-1])) {
		// A closing bracket is kept when the URL opened one, as Wikipedia's do.
		if line[j-1] == ')' && strings.Count(line[i:j], "(") >= strings.Count(line[i:j], ")") {
			break
		}
		j--
	}
	return j
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// codeRanges finds the `inline code` spans on a line, as [start, end) pairs.
// A backtick with no partner is ordinary text.
func codeRanges(line string) [][2]int {
	var out [][2]int
	for i := 0; i < len(line); i++ {
		if line[i] != '`' {
			continue
		}
		j := strings.IndexByte(line[i+1:], '`')
		if j < 0 {
			break
		}
		out = append(out, [2]int{i, i + 1 + j + 1})
		i += j + 1
	}
	return out
}

func within(ranges [][2]int, i int) (bool, int) {
	for _, r := range ranges {
		if i >= r[0] && i < r[1] {
			return true, r[1]
		}
	}
	return false, 0
}

// imageExts are the file types shown inline as pictures.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".avif": true, ".heic": true, ".bmp": true, ".tif": true, ".tiff": true, ".svg": true,
}

// IsImagePath reports whether p names an image file.
func IsImagePath(p string) bool { return isImagePath(p) }

func isImagePath(p string) bool {
	return imageExts[strings.ToLower(path.Ext(p))]
}

// Resolve finds which note a wiki link target names, among notes (vault
// relative paths without extension). A target with a folder in it must match
// a path exactly; a bare name matches any note with that name, and when
// several do, the one nearest the root wins, then the first alphabetically,
// so the answer never depends on the order the notes were listed in. Case is
// ignored throughout, as on the file systems people usually sync between.
// It returns "" when nothing matches.
func Resolve(target string, notes []string) string {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(target), "/"))
	if t == "" {
		return ""
	}
	var candidates []string
	for _, n := range notes {
		ln := strings.ToLower(n)
		if ln == t {
			return n
		}
		if !strings.Contains(t, "/") && strings.ToLower(path.Base(n)) == t {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		di, dj := strings.Count(candidates[i], "/"), strings.Count(candidates[j], "/")
		if di != dj {
			return di < dj
		}
		return strings.ToLower(candidates[i]) < strings.ToLower(candidates[j])
	})
	return candidates[0]
}

// RewriteLinks changes the wiki links in text that point at oldRel so they
// point at newRel, keeping each link's heading and alias. before and after
// are the vault's note lists either side of the rename.
//
// A link written as a path is rewritten when the path was oldRel. A link
// written as a bare name is rewritten only if that name meant oldRel before
// the rename: with two notes called "Todo", renaming the one in a folder must
// not capture links that meant the other. It stays a bare name if the new
// name will mean the renamed note; otherwise it becomes the path.
// It returns the new text and how many links changed.
func RewriteLinks(text, oldRel, newRel string, before, after []string) (string, int) {
	oldPath := strings.ToLower(oldRel)
	oldBase := strings.ToLower(path.Base(oldRel))
	newBase := path.Base(newRel)
	bareMeantOld := strings.EqualFold(Resolve(oldBase, before), oldRel)
	bare := strings.EqualFold(Resolve(newBase, after), newRel)

	lines := strings.Split(text, "\n")
	changed := 0
	fence := ""
	for n, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if f := openFence(trimmed); f != "" {
			fence = f
			continue
		}
		spans := Line(line, false)
		if len(spans) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, sp := range spans {
			if sp.Kind != KindWikiLink {
				continue
			}
			t := strings.ToLower(strings.Trim(sp.Target, "/"))
			isPath := strings.Contains(t, "/")
			if !(isPath && t == oldPath) && !(!isPath && t == oldBase && bareMeantOld) {
				continue
			}
			target := newRel
			if !isPath && bare {
				target = newBase
			}
			inner := target
			if sp.Heading != "" {
				inner += "#" + sp.Heading
			}
			if sp.Alias != "" {
				inner += "|" + sp.Alias
			}
			open := "[["
			if line[sp.Start] == '!' {
				open = "![["
			}
			b.WriteString(line[last:sp.Start])
			b.WriteString(open + inner + "]]")
			last = sp.End
			changed++
		}
		if last > 0 {
			b.WriteString(line[last:])
			lines[n] = b.String()
		}
	}
	return strings.Join(lines, "\n"), changed
}
