package app

import "strings"

// A line diff for the version history's "Changes" view. It is Myers' O(ND)
// algorithm over whole lines, with two guards so that a huge note cannot freeze
// the dialog: the common start and end of the two texts are stripped first, which
// is nearly all of a typical edit, and the search gives up once the two texts
// differ by more than diffMaxEdits lines. A note that different is shown as one
// block removed and one added, which is true, if less helpful, and says so.
//
// It knows nothing about widgets, so it is tested on its own.

type diffOp int

const (
	diffSame diffOp = iota
	diffDel         // in the older text only
	diffAdd         // in the newer text only
)

// diffLine is one line of the result.
type diffLine struct {
	Op   diffOp
	Text string
}

// diffMaxLines is the most lines, both texts together after their shared ends
// are stripped, that are compared at all. Past it the answer is the coarse one
// without a search: the table below is sized by the edits, but the scan of the
// lines is not free either, and a note that long that differs that much is not
// one anybody reads line by line.
const diffMaxLines = 40000

// diffMaxEdits is how many added plus removed lines the search will look for
// before it stops and treats the texts as wholly different. The search keeps a
// table that grows with the square of this, so it is also the memory cap:
// 1500 edits is about 2.25 million small integers.
const diffMaxEdits = 1500

// splitLines cuts text into lines the way a person counts them: a final newline
// ends the last line and does not start another, and an empty text has none.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffLines compares old with new line by line. precise is false when the texts
// differed by more than diffMaxEdits lines and the answer is the coarse one, all
// of what is different removed and then all of it added.
func diffLines(oldText, newText string) (out []diffLine, precise bool) {
	a, b := splitLines(oldText), splitLines(newText)

	// Strip what the two share at either end: it costs nothing and shrinks the
	// search to the part that changed.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	midA, midB := a[pre:len(a)-suf], b[pre:len(b)-suf]

	out = make([]diffLine, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		out = append(out, diffLine{diffSame, l})
	}
	var mid []diffLine
	if len(midA)+len(midB) > diffMaxLines {
		mid, precise = append(marked(midA, diffDel), marked(midB, diffAdd)...), false
	} else {
		mid, precise = myers(midA, midB)
	}
	out = append(out, mid...)
	for _, l := range a[len(a)-suf:] {
		out = append(out, diffLine{diffSame, l})
	}
	return out, precise
}

// myers is the shortest edit script between a and b, or the coarse answer when
// it is longer than diffMaxEdits.
func myers(a, b []string) ([]diffLine, bool) {
	n, m := len(a), len(b)
	switch {
	case n == 0 && m == 0:
		return nil, true
	case n == 0:
		return marked(b, diffAdd), true
	case m == 0:
		return marked(a, diffDel), true
	}

	maxD := min(n+m, diffMaxEdits)
	// v[k+off] is how far along a the furthest path on diagonal k has got. trace
	// keeps v as it stood before each round, which is what walking back needs.
	off := maxD + 1
	v := make([]int32, 2*maxD+3)
	var trace [][]int32
	found := -1
search:
	for d := 0; d <= maxD; d++ {
		snap := make([]int32, 2*d+3)
		copy(snap, v[off-d-1:off+d+2])
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = int(v[off+k+1]) // down: a line of b is added
			} else {
				x = int(v[off+k-1]) + 1 // right: a line of a is removed
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = int32(x)
			if x >= n && y >= m {
				found = d
				break search
			}
		}
	}
	if found < 0 {
		return append(marked(a, diffDel), marked(b, diffAdd)...), false
	}

	// Walk back from the end to the start, reading each round's choice from the
	// table, and collect the lines in reverse.
	var rev []diffLine
	x, y := n, m
	for d := found; d > 0; d-- {
		snap := trace[d] // v before round d, over diagonals -d-1..d+1
		at := func(k int) int { return int(snap[k+d+1]) }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY { // the diagonal: lines both share
			x--
			y--
			rev = append(rev, diffLine{diffSame, a[x]})
		}
		if x == prevX {
			y--
			rev = append(rev, diffLine{diffAdd, b[y]})
		} else {
			x--
			rev = append(rev, diffLine{diffDel, a[x]})
		}
	}
	for x > 0 && y > 0 { // the run of shared lines at the very start
		x--
		y--
		rev = append(rev, diffLine{diffSame, a[x]})
	}
	out := make([]diffLine, len(rev))
	for i, l := range rev {
		out[len(rev)-1-i] = l
	}
	return out, true
}

func marked(lines []string, op diffOp) []diffLine {
	out := make([]diffLine, len(lines))
	for i, l := range lines {
		out[i] = diffLine{op, l}
	}
	return out
}

// diffRow is a line of the Changes view: a line of the diff, or a stretch of
// unchanged lines folded into one row that says how many.
type diffRow struct {
	diffLine
	Folded int // when above zero, this row stands for that many unchanged lines
}

// diffContext is how many unchanged lines are kept on each side of a change.
const diffContext = 2

// foldDiff folds runs of unchanged lines into one row each, keeping diffContext
// lines beside every change. A run is folded only when that hides at least two
// lines: a row saying "1 unchanged line" would take the room of the line itself.
func foldDiff(lines []diffLine) []diffRow {
	rows := make([]diffRow, 0, len(lines))
	for i := 0; i < len(lines); {
		if lines[i].Op != diffSame {
			rows = append(rows, diffRow{diffLine: lines[i]})
			i++
			continue
		}
		j := i
		for j < len(lines) && lines[j].Op == diffSame {
			j++
		}
		// lines[i:j] is a run of unchanged lines. The start of the note and its
		// end have no change on the outer side, so no context is kept there.
		keepHead, keepTail := diffContext, diffContext
		if i == 0 {
			keepHead = 0
		}
		if j == len(lines) {
			keepTail = 0
		}
		hidden := (j - i) - keepHead - keepTail
		if hidden < 2 {
			for _, l := range lines[i:j] {
				rows = append(rows, diffRow{diffLine: l})
			}
		} else {
			for _, l := range lines[i : i+keepHead] {
				rows = append(rows, diffRow{diffLine: l})
			}
			rows = append(rows, diffRow{Folded: hidden})
			for _, l := range lines[j-keepTail : j] {
				rows = append(rows, diffRow{diffLine: l})
			}
		}
		i = j
	}
	return rows
}

// diffCounts is how many lines the diff adds and removes.
func diffCounts(lines []diffLine) (added, removed int) {
	for _, l := range lines {
		switch l.Op {
		case diffAdd:
			added++
		case diffDel:
			removed++
		}
	}
	return added, removed
}
