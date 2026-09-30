package storage

import (
	"fmt"
	"path"
	"regexp"
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
func (s *Store) UnlinkedMentions(rel string, limit int) ([]string, error) {
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
	sort.Strings(candidates)

	linked, err := s.Backlinks(rel)
	if err != nil {
		return nil, err
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
// index and the links table follow. It reports false, changing nothing, when
// the note no longer has such a mention.
//
// The link is written [[Name]]. When the mention was worded differently, in
// another case, it stays as it was written: [[Name|as written]]. When another
// note has the same name and a bare name would mean that one, the path is
// written instead.
func (s *Store) LinkMention(rel, from string) (bool, error) {
	rel = normalizeRel(rel)
	name := path.Base(rel)
	text, err := s.ReadNote(from)
	if err != nil {
		return false, err
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
		link = "[[" + target + "|" + written + "]]"
	}
	if err := s.WriteNote(from, text[:start]+link+text[end:]); err != nil {
		return false, fmt.Errorf("linking %s: %w", name, err)
	}
	return true, nil
}

// FindMention finds the first place text says name as a whole word, without
// regard to case, and returns its byte range. Fenced code, `inline code` and
// anything already part of a link, a web address or a picture do not count,
// and neither does a tag: "#Plan" is a tag, not a mention of a note called
// Plan.
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
	offset := 0
	for n, line := range strings.Split(text, "\n") {
		lineStart := offset
		offset += len(line) + 1
		if fence[n] {
			continue
		}
		spans := markup.Line(line, false)
		code := inlineCode(line)
		for _, m := range re.FindAllStringIndex(line, -1) {
			if !wholeWord(line, m[0], m[1]) || inRanges(code, m[0], m[1]) {
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

// wholeWord says the bytes line[a:b] are not part of a longer word.
func wholeWord(line string, a, b int) bool {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	if a > 0 {
		if r, _ := utf8.DecodeLastRuneInString(line[:a]); isWord(r) {
			return false
		}
	}
	if b < len(line) {
		if r, _ := utf8.DecodeRuneInString(line[b:]); isWord(r) {
			return false
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
