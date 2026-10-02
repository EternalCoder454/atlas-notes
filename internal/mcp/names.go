package mcp

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// errMissing is errNoNote with a few notes the model may have meant. It is the
// answer for a protected note as well as a missing one, so the suggestions come
// only from notes the model may see, and are the same whichever it was.
func (s *server) errMissing(rel string) error {
	near := s.similarNotes(rel, 3)
	if len(near) == 0 {
		return errNoNote(rel)
	}
	quoted := make([]string, len(near))
	for i, n := range near {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return fmt.Errorf("there is no note called %q. Did you mean %s? list_notes or search_notes shows what there is",
		rel, strings.Join(quoted, ", "))
}

// similarNotes is up to most visible notes whose names are like rel's: the same
// name in another folder first, then a name that holds or is held by it, then
// one a couple of typos away.
func (s *server) similarNotes(rel string, most int) []string {
	s.refresh()
	notes, err := s.visibleNotes()
	if err != nil {
		return nil
	}
	want := strings.ToLower(path.Base(rel))
	type cand struct {
		path string
		rank int
	}
	var found []cand
	rows := &[2][]int{make([]int, 72), make([]int, 72)} // reused for every name
	for _, m := range notes {
		if m.Path == rel {
			continue
		}
		base := strings.ToLower(path.Base(m.Path))
		switch {
		case base == want:
			found = append(found, cand{m.Path, 0})
		case min(len(base), len(want)) >= 3 && (strings.Contains(base, want) || strings.Contains(want, base)):
			found = append(found, cand{m.Path, 1})
		case len(want) <= 64 && abs(len(base)-len(want)) <= 2 && levenshtein(base, want, rows) <= 2:
			found = append(found, cand{m.Path, 2})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].rank != found[j].rank {
			return found[i].rank < found[j].rank
		}
		return found[i].path < found[j].path
	})
	var out []string
	for _, c := range found[:min(len(found), most)] {
		out = append(out, c.path)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// levenshtein is how many single-letter changes turn a into b. Both are short
// (at most 66 bytes); rows are the two working rows, reused between calls.
func levenshtein(a, b string, rows *[2][]int) int {
	x, y := []rune(a), []rune(b)
	prev, cur := rows[0][:len(y)+1], rows[1][:len(y)+1]
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(y)]
}
