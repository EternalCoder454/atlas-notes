package editor

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestFindMatches(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		query     string
		matchCase bool
		want      [][2]int
	}{
		{"plain", "hello world hello", "hello", false, [][2]int{{0, 5}, {12, 17}}},
		{"no match", "hello world", "xyz", false, nil},
		{"empty query", "hello", "", false, nil},
		{"empty query, match case", "hello", "", true, nil},
		{"empty text", "", "a", false, nil},
		{"query longer than text", "ab", "abc", false, nil},

		// Case.
		{"case folds by default", "Hello HELLO hello", "hello", false, [][2]int{{0, 5}, {6, 11}, {12, 17}}},
		{"query case is ignored too", "Hello HELLO hello", "HeLLo", false, [][2]int{{0, 5}, {6, 11}, {12, 17}}},
		{"match case", "Hello HELLO hello", "hello", true, [][2]int{{12, 17}}},
		{"match case, capitalised", "Hello HELLO hello", "Hello", true, [][2]int{{0, 5}}},

		// Non-ASCII text. Offsets count characters, so they are not the byte
		// offsets a strings.Index would give.
		{"accents fold", "Ünïcode ünïcode", "ÜNÏ", false, [][2]int{{0, 3}, {8, 11}}},
		{"accents keep case when asked", "Ünïcode ünïcode", "ünï", true, [][2]int{{8, 11}}},
		{"accents do not match plain letters", "Ünïcode", "unicode", false, nil},
		{"offsets are characters", "ééé x", "x", false, [][2]int{{4, 5}}},
		{"greek sigma forms fold together", "ΣΑΣ σας", "σασ", false, [][2]int{{0, 3}, {4, 7}}},
		{"emoji", "a😀b😀😀", "😀", false, [][2]int{{1, 2}, {3, 4}, {4, 5}}},
		{"offsets after emoji are characters", "😀😀 x", "x", false, [][2]int{{3, 4}}},
		{"mixed", "Ünïcode 😀 ünï", "ünï", false, [][2]int{{0, 3}, {10, 13}}},
		{"kelvin sign is a k", "K km", "k", false, [][2]int{{0, 1}, {2, 3}}},

		// Overlap: once a match is reported, the next one starts after it.
		{"adjacent", "aaaa", "aa", false, [][2]int{{0, 2}, {2, 4}}},
		{"odd run", "aaa", "aa", false, [][2]int{{0, 2}}},
		{"overlap on a shared letter", "abababa", "aba", false, [][2]int{{0, 3}, {4, 7}}},

		// Literal, not a pattern.
		{"dot is a dot", "a.b axb", "a.b", false, [][2]int{{0, 3}}},
		{"parens and plus", "f(x)+ f(x)", "(x)+", false, [][2]int{{1, 5}}},
		{"newline counts as one character", "one\ntwo\nthree", "two", false, [][2]int{{4, 7}}},

		// U+FFFC is a checkbox widget in the buffer, never text.
		{"not across an anchor", "a￼b", "ab", false, nil},
		{"not on an anchor", "a￼b", "￼", false, nil},
		{"not across, longer query", "ab￼ab", "b￼a", false, nil},
		{"either side of an anchor", "x￼x", "x", false, [][2]int{{0, 1}, {2, 3}}},
		{"text after an anchor", "￼foo", "foo", false, [][2]int{{1, 4}}},
		{"anchor between two matches", "ab￼ab", "ab", false, [][2]int{{0, 2}, {3, 5}}},
		{"anchor stops a run", "aa￼aa", "aaa", false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findMatches(c.text, c.query, c.matchCase)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("findMatches(%q, %q, %v) = %v, want %v", c.text, c.query, c.matchCase, got, c.want)
			}
		})
	}
}

// A task's "<!-- ... -->" comment is hidden by the editor, so what is written in
// it is not there to find. Offsets still count the hidden characters.
func TestFindMatchesSkipTaskMetadata(t *testing.T) {
	task := "￼Pay rent <!-- priority:high due:2026-10-01 order:1 -->"
	cases := []struct {
		name  string
		text  string
		query string
		want  [][2]int
	}{
		{"due in the comment", task, "due", nil},
		{"high in the comment", task, "high", nil},
		{"a date in the comment", task, "2026-10-01", nil},
		{"part of a date", task, "2026", nil},
		{"the comment's own markers", task, "<!--", nil},
		{"visible text before the comment", task, "rent", [][2]int{{5, 9}}},
		{"visible text after the comment", "￼Do it <!-- due:2026-10-01 --> due tomorrow", "due", [][2]int{{31, 34}}},
		{"each line has its own comment", "high due\n￼Task <!-- priority:high -->\nhigh", "high", [][2]int{{0, 4}, {38, 42}}},
		{"not across the edge of a comment", "due<!-- x -->due", "due<", nil},
		{"either side of a comment", "due<!-- x -->due", "due", [][2]int{{0, 3}, {13, 16}}},
		{"characters, not bytes, before it", "é😀 <!-- due -->due", "due", [][2]int{{15, 18}}},
		{"a comment that is never closed is text", "todo <!-- due", "due", [][2]int{{10, 13}}},
		{"only the first comment is metadata", "a <!-- x --> <!-- due -->", "due", [][2]int{{18, 21}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findMatches(c.text, c.query, false)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("findMatches(%q, %q) = %v, want %v", c.text, c.query, got, c.want)
			}
		})
	}
}

// Every rune of a folding orbit must fold to the same rune, or "K" (Kelvin)
// would match "k" from one side and not from the other.
func TestFoldRuneOrbits(t *testing.T) {
	for r := rune(0); r <= 0x1FFFF; r++ {
		want := foldRune(r)
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if got := foldRune(f); got != want {
				t.Fatalf("foldRune(%U) = %U, but foldRune(%U) = %U, and they are the same letter", f, got, r, want)
			}
		}
	}
}

// slowMatches is the obvious version of findMatches: strings.EqualFold on one
// rune at a time, restarting after each match. The fast one must agree with it
// on any text, including text made of the awkward characters.
func slowMatches(text, query string, matchCase bool) [][2]int {
	t, q := []rune(text), []rune(query)
	if len(q) == 0 {
		return nil
	}
	same := func(a, b rune) bool {
		if a == '￼' {
			return false
		}
		if matchCase {
			return a == b
		}
		return strings.EqualFold(string(a), string(b))
	}
	var out [][2]int
	for i := 0; i+len(q) <= len(t); {
		ok := true
		for j := range q {
			if !same(t[i+j], q[j]) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, [2]int{i, i + len(q)})
			i += len(q)
		} else {
			i++
		}
	}
	return out
}

func TestFindMatchesAgreesWithReference(t *testing.T) {
	alphabet := []rune("aAbBkKsSüÜéÉσςΣ😀 \n￼Kſ")
	rng := rand.New(rand.NewSource(1))
	word := func(max int) string {
		n := rng.Intn(max + 1)
		r := make([]rune, n)
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(r)
	}
	for i := 0; i < 3000; i++ {
		text, query := word(24), word(3)
		for _, matchCase := range []bool{false, true} {
			got := findMatches(text, query, matchCase)
			want := slowMatches(text, query, matchCase)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("findMatches(%q, %q, %v) = %v, reference says %v", text, query, matchCase, got, want)
			}
		}
	}
}

func BenchmarkFindMatches(b *testing.B) {
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. Ünïcode 😀\n", 1000)
	b.SetBytes(int64(len(text)))
	for i := 0; i < b.N; i++ {
		findMatches(text, "the", false)
	}
}
