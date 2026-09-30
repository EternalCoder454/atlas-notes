package storage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"atlas-notes/internal/markup"
)

// Unlinked mentions: notes that say another note's name in plain words without
// linking to it. They are the notes most likely to want a link, and the ones
// nobody would find by looking at links alone.

// mentionCandidates is how many notes the index is asked for. The index only
// knows words, so what it returns is a superset: each candidate is then read
// and checked for a real, plain mention, and a common name would otherwise
// mean reading half the vault.
const mentionCandidates = 120

// UnlinkedMentions lists, sorted, up to limit notes whose text has rel's name
// as a whole word, outside code and outside any link, and that do not link to
// rel. rel itself and locked notes are left out; a locked note is not in the
// index, so it is never even read. A name shorter than the index's shortest
// word has no mentions worth listing, so it has none.
//
// linked is rel's backlinks, which the caller has already asked for, so they
// are not asked for twice. The newest notes are read first, and reading stops
// when ctx is cancelled: on a big vault the person has usually moved on to
// another note before the answer is wanted.
func (s *Store) UnlinkedMentions(ctx context.Context, rel string, limit int, linked []string) ([]string, error) {
	rel = normalizeRel(rel)
	name := path.Base(rel)
	q := mentionPhrase(name)
	if rel == "" || q == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT n.path FROM notes_fts
		JOIN notes n ON n.id = notes_fts.rowid
		WHERE notes_fts MATCH ? AND n.locked = 0 AND n.path != ?
		ORDER BY n.modified_at DESC
		LIMIT ?`, q, rel, mentionCandidates)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	skip := make(map[string]bool, len(linked))
	for _, p := range linked {
		skip[p] = true
	}
	var out []string
	for _, p := range candidates {
		if skip[p] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, err := s.ReadNote(p)
		if err != nil {
			continue // gone, or locked since: not a mention we can show
		}
		if _, _, ok := FindMention(text, name); ok {
			out = append(out, p)
			if len(out) == limit {
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// mentionPhrase is the FTS5 query for a name as a phrase: its words, in order,
// next to each other. It is empty for a name too short to search for.
func mentionPhrase(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 || utf8.RuneCountInString(strings.Join(words, "")) < minSearchRunes {
		return ""
	}
	return `"` + strings.Join(words, " ") + `"`
}

// LinkMention turns the first plain mention of rel's name in note from into a
// link to rel, and saves it through the normal write path, so history, the
// index and the links table follow. The text before the change is kept as a
// version. It reports false, changing nothing, when there is nothing to do: the
// note is gone or locked, already links to rel, or no longer has such a
// mention.
//
// Reading, finding and writing happen under one hold of the write lock. A save,
// a sync or a rename in between would otherwise be written over by text read
// before it, or bring a renamed note back under its old name.
//
// The link is written [[Name]]. When the mention was worded differently, in
// another case, it stays as it was written: [[Name|as written]]. When another
// note has the same name and a bare name would mean that one, the path is
// written instead.
func (s *Store) LinkMention(rel, from string) (bool, error) {
	rel, from = normalizeRel(rel), normalizeRel(from)
	name := path.Base(rel)
	if rel == "" || from == "" || rel == from {
		return false, nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.IsNoteLocked(from) || s.lockedByFolder(from) {
		return false, nil
	}
	text, err := s.ReadNote(from)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, ErrLocked) {
			return false, nil
		}
		return false, err
	}
	// The same test as the "Linked from" list, so a second click on a stale
	// button cannot link a second mention.
	if linked, err := s.Backlinks(rel); err == nil && slices.Contains(linked, from) {
		return false, nil
	}
	start, end, ok := FindMention(text, name)
	if !ok {
		return false, nil
	}
	target := name
	if all, err := s.notePaths(); err == nil && !strings.EqualFold(markup.Resolve(name, all), rel) {
		target = rel
	}
	written := text[start:end]
	link := "[[" + target + "]]"
	if written != target {
		bar := "|"
		if inTableRow(text, start) {
			bar = `\|` // a bare pipe would split the cell
		}
		link = "[[" + target + bar + written + "]]"
	}
	updated := text[:start] + link + text[end:]
	if err := s.writeNoteLocked(from, updated, true); err != nil {
		// The file is saved before the index is brought up to date, and only
		// the second can fail after that. The text is there, so it is a link.
		if cur, rerr := s.ReadNote(from); rerr == nil && cur == updated {
			log.Printf("atlas-notes: linked %q, but its index entry could not be updated: %v", from, err)
			return true, nil
		}
		return false, fmt.Errorf("linking %s: %w", name, err)
	}
	return true, nil
}

// inTableRow says the line that holds offset is a row of a table.
func inTableRow(text string, offset int) bool {
	start := strings.LastIndexByte(text[:offset], '\n') + 1
	return strings.HasPrefix(strings.TrimSpace(text[start:]), "|")
}

// FindMention finds the first place text says name as a whole word, without
// regard to case, and returns its byte range. Fenced and indented code,
// `inline code`, front matter, HTML comments, reference definitions and
// anything already part of a link, a web address or a picture do not count,
// and neither does a tag ("#Plan" is not a mention of a note called Plan) or a
// name wrapped in square brackets on its own ("[Plan]", "[Plan][1]").
func FindMention(text, name string) (start, end int, ok bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, 0, false
	}
	re, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(name))
	if err != nil {
		return 0, 0, false
	}
	fence := markup.InCodeFence(text)
	lines := strings.Split(text, "\n")
	front := frontMatterLines(lines)
	inComment := false
	offset := 0
	for n, line := range lines {
		lineStart := offset
		offset += len(line) + 1
		comments, still := commentRanges(line, inComment)
		inComment = still
		if fence[n] || n < front || refDefinition.MatchString(line) ||
			strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		spans := markup.Line(line, false)
		code := inlineCode(line)
		for _, m := range re.FindAllStringIndex(line, -1) {
			if !wholeWord(line, m[0], m[1]) || inRanges(code, m[0], m[1]) || inRanges(comments, m[0], m[1]) {
				continue
			}
			if m[0] > 0 && m[1] < len(line) && line[m[0]-1] == '[' && line[m[1]] == ']' {
				continue
			}
			inSpan := false
			for _, sp := range spans {
				if m[0] < sp.End && m[1] > sp.Start {
					inSpan = true
					break
				}
			}
			if !inSpan {
				return lineStart + m[0], lineStart + m[1], true
			}
		}
	}
	return 0, 0, false
}

// refDefinition is a Markdown reference definition, "[x]: https://...".
var refDefinition = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s`)

// frontMatterLines is how many lines at the top are a "---" block of
// properties, which are not prose. Zero when the note does not start with one.
func frontMatterLines(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\r") != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			return i + 1
		}
	}
	return 0
}

// commentRanges is the byte ranges of a line that are inside an HTML comment,
// given whether the line starts inside one, and whether the next does.
func commentRanges(line string, in bool) (ranges [][2]int, still bool) {
	i := 0
	for i < len(line) {
		if in {
			j := strings.Index(line[i:], "-->")
			if j < 0 {
				return append(ranges, [2]int{i, len(line)}), true
			}
			ranges = append(ranges, [2]int{i, i + j + 3})
			i += j + 3
			in = false
			continue
		}
		j := strings.Index(line[i:], "<!--")
		if j < 0 {
			break
		}
		i += j
		k := strings.Index(line[i+4:], "-->")
		if k < 0 {
			return append(ranges, [2]int{i, len(line)}), true
		}
		ranges = append(ranges, [2]int{i, i + 4 + k + 3})
		i += 4 + k + 3
		in = false
	}
	return ranges, false
}

// wholeWord says the bytes line[a:b] are not part of a longer word. A
// combining mark belongs to its letter, so "Cafe" is not in a decomposed
// "Café". A "/" or "." with a letter behind it is part of a path or a file
// name, as in "foo/Plan" and "Plan.md", but not a full stop that ends a
// sentence.
func wholeWord(line string, a, b int) bool {
	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.In(r, unicode.Mn, unicode.Mc)
	}
	letter := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	if a > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:a])
		if isWord(r) {
			return false
		}
		if r == '/' || r == '.' {
			if p, _ := utf8.DecodeLastRuneInString(line[:a-size]); letter(p) {
				return false
			}
		}
	}
	if b < len(line) {
		r, size := utf8.DecodeRuneInString(line[b:])
		if isWord(r) {
			return false
		}
		if r == '/' || r == '.' {
			if b+size < len(line) {
				if n, _ := utf8.DecodeRuneInString(line[b+size:]); letter(n) {
					return false
				}
			}
		}
	}
	return true
}

// inlineCode is the byte ranges between paired backticks, the marks included.
// An odd last backtick opens nothing, as in the editor.
func inlineCode(line string) [][2]int {
	var out [][2]int
	open := -1
	for i := 0; i < len(line); i++ {
		if line[i] != '`' {
			continue
		}
		if open < 0 {
			open = i
		} else {
			out = append(out, [2]int{open, i + 1})
			open = -1
		}
	}
	return out
}

func inRanges(ranges [][2]int, a, b int) bool {
	for _, r := range ranges {
		if a < r[1] && b > r[0] {
			return true
		}
	}
	return false
}
