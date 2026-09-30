package app

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// rebuild reads both texts back out of a diff: what an older text and a newer
// one must have been for the diff to be right.
func rebuild(lines []diffLine) (oldText, newText []string) {
	for _, l := range lines {
		if l.Op != diffAdd {
			oldText = append(oldText, l.Text)
		}
		if l.Op != diffDel {
			newText = append(newText, l.Text)
		}
	}
	return oldText, newText
}

func checkDiff(t *testing.T, oldText, newText string) []diffLine {
	t.Helper()
	got, _ := diffLines(oldText, newText)
	o, n := rebuild(got)
	if !equalLines(o, splitLines(oldText)) || !equalLines(n, splitLines(newText)) {
		t.Fatalf("the diff of %q and %q does not give both texts back: %v", oldText, newText, got)
	}
	return got
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDiffIdenticalTextsHaveNoChanges(t *testing.T) {
	got := checkDiff(t, "a\nb\nc\n", "a\nb\nc\n")
	if add, del := diffCounts(got); add != 0 || del != 0 {
		t.Errorf("identical texts differ by +%d -%d", add, del)
	}
	if _, precise := diffLines("", ""); !precise {
		t.Error("two empty texts are not a precise answer")
	}
}

func TestDiffAllAddedAndAllRemoved(t *testing.T) {
	got := checkDiff(t, "", "a\nb\n")
	if add, del := diffCounts(got); add != 2 || del != 0 {
		t.Errorf("all added: +%d -%d", add, del)
	}
	got = checkDiff(t, "a\nb\nc", "")
	if add, del := diffCounts(got); add != 0 || del != 3 {
		t.Errorf("all removed: +%d -%d", add, del)
	}
}

func TestDiffOneChangedLine(t *testing.T) {
	got := checkDiff(t, "one\ntwo\nthree\n", "one\n2\nthree\n")
	if add, del := diffCounts(got); add != 1 || del != 1 {
		t.Errorf("one changed line: +%d -%d", add, del)
	}
}

func TestDiffAMovedBlockIsRemovedAndAddedAgain(t *testing.T) {
	got := checkDiff(t, "a\nb\nc\nd\ne\n", "d\ne\na\nb\nc\n")
	add, del := diffCounts(got)
	// Moving is not something a line diff knows: it is the shortest way to
	// remove lines from one place and add them in another, two lines here.
	if add != 2 || del != 2 {
		t.Errorf("moved block: +%d -%d, want +2 -2", add, del)
	}
}

func TestDiffTreatsWindowsLineEndingsAsPlainOnes(t *testing.T) {
	got := checkDiff(t, "a\r\nb\r\n", "a\nb\n")
	if add, del := diffCounts(got); add != 0 || del != 0 {
		t.Errorf("line endings alone made a difference: +%d -%d", add, del)
	}
}

// TestDiffRandomEditsAlwaysGiveBothTextsBack is the check that matters most: no
// matter how two texts differ, the diff must read back as exactly those two.
func TestDiffRandomEditsAlwaysGiveBothTextsBack(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 400; i++ {
		var a []string
		for j := r.Intn(30); j > 0; j-- {
			a = append(a, fmt.Sprint("line ", r.Intn(8)))
		}
		b := append([]string(nil), a...)
		for j := r.Intn(8); j > 0; j-- {
			switch p := r.Intn(len(b) + 1); r.Intn(3) {
			case 0:
				b = append(b[:p], append([]string{fmt.Sprint("new ", r.Intn(8))}, b[p:]...)...)
			case 1:
				if p < len(b) {
					b = append(b[:p], b[p+1:]...)
				}
			default:
				if p < len(b) {
					b[p] = fmt.Sprint("line ", r.Intn(8))
				}
			}
		}
		checkDiff(t, strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

func TestDiffOfALargeNoteWithAFewEditsIsFast(t *testing.T) {
	var a, b []string
	for i := 0; i < 10000; i++ {
		a = append(a, fmt.Sprint("line ", i))
		b = append(b, fmt.Sprint("line ", i))
	}
	b[100] = "changed"
	b[5000] = "changed too"
	b = append(b[:7000], b[7003:]...)
	start := time.Now()
	got, precise := diffLines(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if !precise {
		t.Error("a few edits were not answered precisely")
	}
	if add, del := diffCounts(got); add != 2 || del != 5 {
		t.Errorf("+%d -%d, want +2 -5", add, del)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestDiffOfTwoUnrelatedLargeNotesGivesUpQuicklyAndSaysSo(t *testing.T) {
	var a, b []string
	for i := 0; i < 10000; i++ {
		a = append(a, fmt.Sprint("old ", i))
		b = append(b, fmt.Sprint("new ", i))
	}
	start := time.Now()
	got, precise := diffLines(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if precise {
		t.Error("two unrelated notes were reported as a precise diff")
	}
	o, n := rebuild(got)
	if len(o) != 10000 || len(n) != 10000 {
		t.Errorf("the coarse answer lost lines: %d and %d", len(o), len(n))
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("gave up after %v", time.Since(start))
	}
}

func TestFoldDiffCollapsesLongUnchangedRuns(t *testing.T) {
	var a, b []string
	for i := 0; i < 20; i++ {
		a = append(a, fmt.Sprint("same ", i))
		b = append(b, fmt.Sprint("same ", i))
	}
	b[10] = "different"
	lines, _ := diffLines(strings.Join(a, "\n"), strings.Join(b, "\n"))
	rows := foldDiff(lines)
	var folded []int
	for _, r := range rows {
		if r.Folded > 0 {
			folded = append(folded, r.Folded)
		}
	}
	// Ten lines before the change and nine after, two kept beside it each way.
	if len(folded) != 2 || folded[0] != 8 || folded[1] != 7 {
		t.Errorf("folded runs %v, want [8 7]", folded)
	}
}

func TestFoldDiffKeepsShortRunsWhole(t *testing.T) {
	lines, _ := diffLines("a\nb\nc\nd\ne\nf\n", "X\nb\nc\nd\ne\nY\n")
	for _, r := range foldDiff(lines) {
		if r.Folded > 0 {
			t.Errorf("a run of four lines was folded: %+v", r)
		}
	}
}

func TestFoldDiffOfNoChangesFoldsTheWholeNote(t *testing.T) {
	lines, _ := diffLines("a\nb\nc\nd\n", "a\nb\nc\nd\n")
	rows := foldDiff(lines)
	if len(rows) != 1 || rows[0].Folded != 4 {
		t.Errorf("rows %+v, want one folded row of 4", rows)
	}
}

func TestDiffOfHugeDifferingNotesSkipsTheSearch(t *testing.T) {
	var a, b []string
	for i := 0; i < 30000; i++ {
		a = append(a, fmt.Sprint("old ", i))
		b = append(b, fmt.Sprint("new ", i))
	}
	start := time.Now()
	_, precise := diffLines(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if precise || time.Since(start) > time.Second {
		t.Errorf("precise=%v after %v", precise, time.Since(start))
	}
}

func TestDiffOfTextsThatDifferOnlyInTheFinalNewlineHasNoLineChanges(t *testing.T) {
	lines, _ := diffLines("a\nb", "a\nb\n")
	if add, del := diffCounts(lines); add != 0 || del != 0 {
		t.Errorf("+%d -%d", add, del)
	}
}
