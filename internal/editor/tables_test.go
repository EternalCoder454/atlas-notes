package editor

import (
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseTable(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		rows  [][]string // nil when the lines are not a table
		align []tableAlign
		span  int
	}{
		{
			name:  "plain table",
			lines: []string{"| a | b |", "|---|---|", "| 1 | 2 |", "| 3 | 4 |"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}, {"3", "4"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  4,
		},
		{
			name:  "header only, no body",
			lines: []string{"| a | b |", "|---|---|"},
			rows:  [][]string{{"a", "b"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  2,
		},
		{
			name:  "alignments",
			lines: []string{"| a | b | c | d |", "|---|:--|:-:|--:|", "| 1 | 2 | 3 | 4 |"},
			rows:  [][]string{{"a", "b", "c", "d"}, {"1", "2", "3", "4"}},
			align: []tableAlign{alignDefault, alignLeft, alignCenter, alignRight},
			span:  3,
		},
		{
			name:  "single dash cells and a lone colon-dash",
			lines: []string{"| a | b |", "| - | :-: |"},
			rows:  [][]string{{"a", "b"}},
			align: []tableAlign{alignDefault, alignCenter},
			span:  2,
		},
		{
			name:  "no outer pipes",
			lines: []string{"a | b", "--- | ---", "1 | 2"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "only a leading pipe, only a trailing one",
			lines: []string{"| a | b", "---|---|", "1 | 2 |"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "one column needs its outer pipes",
			lines: []string{"| a |", "|---|", "| 1 |"},
			rows:  [][]string{{"a"}, {"1"}},
			align: []tableAlign{alignDefault},
			span:  3,
		},
		{
			name:  "escaped pipe",
			lines: []string{`| a \| b | c |`, "|---|---|", `| x | \| |`},
			rows:  [][]string{{"a | b", "c"}, {"x", "|"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "an escaped backslash does not escape the pipe",
			lines: []string{`| a \\| b |`, "|---|---|"},
			rows:  [][]string{{`a \\`, "b"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  2,
		},
		{
			name:  "code with a pipe",
			lines: []string{"| `a|b` | c |", "|---|---|", "| `x\\|y` | `` a|b `` |"},
			rows:  [][]string{{"`a|b`", "c"}, {"`x|y`", "`` a|b ``"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "a backtick with no partner is just a character",
			lines: []string{"| a` | b |", "|---|---|"},
			rows:  [][]string{{"a`", "b"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  2,
		},
		{
			name:  "ragged rows are padded and cut",
			lines: []string{"| a | b | c |", "|---|---|---|", "| 1 |", "| 1 | 2 | 3 | 4 | 5 |", "|", "||"},
			rows: [][]string{
				{"a", "b", "c"}, {"1", "", ""}, {"1", "2", "3"}, {"", "", ""}, {"", "", ""},
			},
			align: []tableAlign{alignDefault, alignDefault, alignDefault},
			span:  6,
		},
		{
			name:  "whitespace around cells and the row",
			lines: []string{"  |  a   |  b  |  ", "  | :-:  |   --: |", "\t| 1\t| 2 |"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}},
			align: []tableAlign{alignCenter, alignRight},
			span:  3,
		},
		{
			name:  "empty cells",
			lines: []string{"| a |  | c |", "|---|---|---|", "|  | 2 |  |"},
			rows:  [][]string{{"a", "", "c"}, {"", "2", ""}},
			align: []tableAlign{alignDefault, alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "the body ends at a line with no pipe",
			lines: []string{"| a | b |", "|---|---|", "| 1 | 2 |", "text", "| 3 | 4 |"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "the body ends at a blank line",
			lines: []string{"| a | b |", "|---|---|", "| 1 | 2 |", "", "| 3 | 4 |"},
			rows:  [][]string{{"a", "b"}, {"1", "2"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},
		{
			name:  "text after the table is left",
			lines: []string{"| a | b |", "|---|---|", "text"},
			rows:  [][]string{{"a", "b"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  2,
		},
		{
			name:  "multi-byte text",
			lines: []string{"| naïve | 日本 |", "|---|---|", "| ☃ | é |"},
			rows:  [][]string{{"naïve", "日本"}, {"☃", "é"}},
			align: []tableAlign{alignDefault, alignDefault},
			span:  3,
		},

		// Not tables.
		{name: "no delimiter row", lines: []string{"a | b", "c | d"}},
		{name: "no delimiter row, then a table row", lines: []string{"| a | b |", "| 1 | 2 |", "|---|---|"}},
		{name: "a header alone", lines: []string{"| a | b |"}},
		{name: "a line of dashes is a rule, not a delimiter row", lines: []string{"a | b", "---"}},
		{name: "delimiter row alone", lines: []string{"|---|---|"}},
		{name: "delimiter row and a text line", lines: []string{"|---|---|", "text"}},
		{name: "header and delimiter differ in width", lines: []string{"| a | b |", "|---|---|---|"}},
		{name: "header wider than the delimiter row", lines: []string{"| a | b | c |", "|---|---|"}},
		{name: "delimiter cell with other characters", lines: []string{"| a | b |", "|---|-x-|"}},
		{name: "delimiter cell that is only a colon", lines: []string{"| a | b |", "|---|:|"}},
		{name: "delimiter cell that is empty", lines: []string{"| a | b |", "|---||"}},
		{name: "delimiter cell with a space inside", lines: []string{"| a | b |", "|---|- -|"}},
		{name: "header with no pipe", lines: []string{"a", "|---|"}},
		{name: "no lines", lines: nil},
		{name: "a task line is not a row", lines: []string{"￼| a | b |", "|---|---|"}},
	}
	for _, c := range cases {
		got, ok := parseTable(c.lines)
		if ok != (c.rows != nil) {
			t.Errorf("%s: parseTable ok = %v, want %v", c.name, ok, c.rows != nil)
			continue
		}
		if !ok {
			continue
		}
		if !reflect.DeepEqual(got.rows, c.rows) {
			t.Errorf("%s: rows = %q, want %q", c.name, got.rows, c.rows)
		}
		if !reflect.DeepEqual(got.align, c.align) {
			t.Errorf("%s: align = %v, want %v", c.name, got.align, c.align)
		}
		if got.lines != c.span {
			t.Errorf("%s: spans %d lines, want %d", c.name, got.lines, c.span)
		}
	}
}

func TestSplitRow(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"| a | b |", []string{"a", "b"}},
		{"a | b", []string{"a", "b"}},
		{"a|b|c", []string{"a", "b", "c"}},
		{"|a|b", []string{"a", "b"}},
		{"a|b|", []string{"a", "b"}},
		{"|", nil},
		{"", nil},
		{"   ", nil},
		{"||", []string{""}},
		{"| |", []string{""}},
		{"a", []string{"a"}},
		{`a \| b`, []string{"a | b"}},
		{`| a \|`, []string{"a |"}},
		{`\\| b`, []string{`\\`, "b"}},
		{`a \* b | c`, []string{`a \* b`, "c"}},
		{"`a|b`|c", []string{"`a|b`", "c"}},
		{"a`|b", []string{"a`", "b"}},
		{"``a`|b``|c", []string{"``a`|b``", "c"}},
	}
	for _, c := range cases {
		if got := splitRow(c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitRow(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestFindTables(t *testing.T) {
	doc := []string{
		"intro",                // 0
		"| a | b |",            // 1
		"|---|---|",            // 2
		"| 1 | 2 |",            // 3
		"",                     // 4
		"text | with a pipe",   // 5
		"x | y",                // 6
		"--|--",                // 7
		"1 | 2",                // 8
		"end",                  // 9
		"| lone |",             // 10
		"| head |",             // 11
		"|--------|",           // 12
		"| and a body row |",   // 13
		"| and a second one |", // 14
	}
	got := findTables(doc, 0, nil)
	var at, span []int
	for _, f := range got {
		at = append(at, f.line)
		span = append(span, f.lines)
	}
	if want := []int{1, 6, 11}; !reflect.DeepEqual(at, want) {
		t.Errorf("tables start at lines %v, want %v", at, want)
	}
	if want := []int{3, 3, 4}; !reflect.DeepEqual(span, want) {
		t.Errorf("tables span %v lines, want %v", span, want)
	}

	// The lines of a pass are numbered from where the pass starts.
	got = findTables(doc[5:9], 105, nil)
	if len(got) != 1 || got[0].line != 106 || got[0].lines != 3 {
		t.Errorf("a slice starting at line 105 gives %+v, want one table at 106 of 3 lines", got)
	}

	// Nothing is found where there is no table.
	if got := findTables([]string{"a", "b | c", "d"}, 0, nil); len(got) != 0 {
		t.Errorf("found %+v in a document without a table", got)
	}
	if got := findTables(nil, 0, nil); len(got) != 0 {
		t.Errorf("found %+v in no lines", got)
	}
}

func TestFindTablesFence(t *testing.T) {
	// A table inside a fenced code block is code.
	doc := []string{
		"```",       // 0
		"| a | b |", // 1
		"|---|---|", // 2
		"| 1 | 2 |", // 3
		"```",       // 4
		"| c |",     // 5
		"|---|",     // 6
		"| 3 |",     // 7
	}
	fence := []bool{true, true, true, true, true, false, false, false}
	got := findTables(doc, 0, fence)
	if len(got) != 1 || got[0].line != 5 || got[0].lines != 3 {
		t.Errorf("found %+v, want one table at line 5 of 3 lines", got)
	}
	// Without the fence known, the same lines hold two.
	if got := findTables(doc, 0, nil); len(got) != 2 {
		t.Errorf("without a fence found %d tables, want 2", len(got))
	}

	// A table does not run on into a fence, and does not begin across one.
	doc = []string{"| a |", "|---|", "| x |", "```", "| y |", "```", "| b |"}
	fence = []bool{false, false, false, true, true, true, false}
	got = findTables(doc, 0, fence)
	if len(got) != 1 || got[0].line != 0 || got[0].lines != 3 {
		t.Errorf("found %+v, want one table at line 0 of 3 lines", got)
	}
	doc = []string{"| a |", "```", "|---|", "```", "| x |"}
	fence = []bool{false, true, true, true, false}
	if got := findTables(doc, 0, fence); len(got) != 0 {
		t.Errorf("a header and a delimiter row on either side of a fence gave %+v", got)
	}

	// Lines after the pass's own start are looked up by their place in the note.
	got = findTables(doc[4:], 4, fence)
	if len(got) != 0 {
		t.Errorf("found %+v in a lone line", got)
	}
}

func TestFindTablesTooBig(t *testing.T) {
	// More cells than are drawn: it stays text, and what follows still counts.
	doc := []string{"| a | b |", "|---|---|"}
	for i := 0; i < maxTableCells/2; i++ {
		doc = append(doc, "| "+strconv.Itoa(i)+" | x |")
	}
	doc = append(doc, "", "| c |", "|---|")
	got := findTables(doc, 0, nil)
	if len(got) != 1 || got[0].line != len(doc)-2 {
		t.Errorf("found %+v, want only the small table at line %d", got, len(doc)-2)
	}
	// One under the limit is drawn.
	if got := findTables(doc[:maxTableCells/2], 0, nil); len(got) != 1 {
		t.Errorf("a table just under the limit gave %d tables, want 1", len(got))
	}
}

func TestFindTablesRandom(t *testing.T) {
	// Whatever the lines are, tables stay in bounds, apart, and out of the fence.
	rng := rand.New(rand.NewSource(1))
	pieces := []string{"|", "---", ":--:", " ", "a", "`", `\`, "x|y", "```"}
	for n := 0; n < 3000; n++ {
		lines := make([]string, 1+rng.Intn(8))
		fence := make([]bool, len(lines))
		for i := range lines {
			var b strings.Builder
			for k := rng.Intn(6); k >= 0; k-- {
				b.WriteString(pieces[rng.Intn(len(pieces))])
			}
			lines[i] = b.String()
			fence[i] = rng.Intn(6) == 0
		}
		end := -1
		for _, f := range findTables(lines, 0, fence) {
			if f.line <= end || f.lines < 2 || f.line+f.lines > len(lines) {
				t.Fatalf("table at %d of %d lines in %q", f.line, f.lines, lines)
			}
			end = f.line + f.lines - 1
			for i := f.line; i <= end; i++ {
				if fence[i] {
					t.Fatalf("table takes the fenced line %d of %q", i, lines)
				}
			}
			for _, row := range f.rows {
				if len(row) != len(f.align) {
					t.Fatalf("row %q is not %d cells wide", row, len(f.align))
				}
			}
		}
	}
}

func TestWidenToTables(t *testing.T) {
	doc := []string{
		"text",      // 0
		"| a |",     // 1
		"|---|",     // 2
		"| 1 |",     // 3
		"| 2 |",     // 4
		"text",      // 5
		"x | y",     // 6
		"y",         // 7
		"| z |",     // 8
		"￼| task |", // 9: a task is not a row
	}
	last := len(doc) - 1
	isRow := func(n int) bool { return isTableRow(doc[n]) }
	cases := []struct {
		from, to     int
		wantF, wantT int
	}{
		{3, 3, 1, 4}, // a row in the body
		{2, 2, 1, 4}, // the delimiter row
		{1, 1, 1, 4}, // the header
		{4, 4, 1, 4}, // the last row
		{0, 0, 0, 0}, // prose above the table
		{5, 5, 5, 5}, // prose below it
		{0, 1, 0, 4}, // a range that ends on the header
		{4, 5, 1, 5}, // a range that starts on the last row
		{0, 5, 0, 5}, // a range with the whole table inside
		{4, 6, 1, 6}, // both ends on rows
		{7, 7, 7, 7}, // a line with no pipe between rows
		{8, 8, 8, 8}, // a lone row
		{8, 9, 8, 9}, // the task line after it does not join
		{6, 6, 6, 6}, // a row alone
		{2, 3, 1, 4}, // inside
		{1, 3, 1, 4}, // the top of the table is the top of the range
		{0, 4, 0, 4}, // the bottom of the table is the bottom of the range
		{9, 9, 9, 9}, // the last line of the note
		{6, 8, 6, 8}, // rows around a line with no pipe
		{0, last, 0, last},
	}
	for _, c := range cases {
		f, to := widenToTables(c.from, c.to, last, isRow)
		if f != c.wantF || to != c.wantT {
			t.Errorf("widenToTables(%d, %d) = %d, %d, want %d, %d", c.from, c.to, f, to, c.wantF, c.wantT)
		}
	}

	// The note's first and last lines are as far as it goes.
	all := []string{"| a |", "|---|", "| 1 |"}
	f, to := widenToTables(1, 1, 2, func(n int) bool { return isTableRow(all[n]) })
	if f != 0 || to != 2 {
		t.Errorf("a table filling the note widened to %d, %d, want 0, 2", f, to)
	}
}

func TestEscapeMarkup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a < b", "a &lt; b"},
		{"a & b", "a &amp; b"},
		{"a > b", "a &gt; b"},
		{"&amp;", "&amp;amp;"},
		{`say "hi"`, "say &quot;hi&quot;"},
		{"it's", "it&apos;s"},
		{"<b>x</b>", "&lt;b&gt;x&lt;/b&gt;"},
		{`<span foreground="red">x</span>`, "&lt;span foreground=&quot;red&quot;&gt;x&lt;/span&gt;"},
		{"", ""},
		{"日本", "日本"},
	}
	for _, c := range cases {
		if got := escapeMarkup(c.in); got != c.want {
			t.Errorf("escapeMarkup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCellMarkup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"", ""},

		// Everything that is not ours is escaped, whatever it looks like.
		{"a < b & c > d", "a &lt; b &amp; c &gt; d"},
		{"<b>not bold</b>", "&lt;b&gt;not bold&lt;/b&gt;"},
		{`<span foreground="red">x</span>`, "&lt;span foreground=&quot;red&quot;&gt;x&lt;/span&gt;"},
		{"&lt; is already text", "&amp;lt; is already text"},
		{"5 < 6 && 7 > 3", "5 &lt; 6 &amp;&amp; 7 &gt; 3"},

		// The inline constructs become tags.
		{"**bold**", "<b>bold</b>"},
		{"__bold__", "<b>bold</b>"},
		{"*it*", "<i>it</i>"},
		{"_it_", "<i>it</i>"},
		{"~~gone~~", "<s>gone</s>"},
		{"a **b** c *d* e", "a <b>b</b> c <i>d</i> e"},
		{"**bold and *nested* text**", "<b>bold and <i>nested</i> text</b>"},
		{"`code`", "<tt>code</tt>"},
		{"`a < b & c`", "<tt>a &lt; b &amp; c</tt>"},
		{"`<b>`", "<tt>&lt;b&gt;</tt>"},
		{"`**not bold**`", "<tt>**not bold**</tt>"},
		{"`` a ` b ``", "<tt> a ` b </tt>"},
		{"**`code` in bold**", "<b><tt>code</tt> in bold</b>"},

		// Text inside the markers, and the words around them, are escaped as well.
		{"**a < b**", "<b>a &lt; b</b>"},
		{"x & *y & z*", "x &amp; <i>y &amp; z</i>"},

		// A marker that does not close, or does not flank text, is text.
		{"**unclosed", "**unclosed"},
		{"*unclosed", "*unclosed"},
		{"a * b * c", "a * b * c"},
		{"a ** b ** c", "a ** b ** c"},
		{"2 * 3 = 6", "2 * 3 = 6"},
		{"`unclosed", "`unclosed"},
		{"~single~", "~single~"},
		{"snake_case_name", "snake_case_name"},
		{"__init__.py", "<b>init</b>.py"},
		{"a_b_c and d_e", "a_b_c and d_e"},
		{"**", "**"},
		{"****", "****"},

		// Escapes.
		{`\*not italic\*`, "*not italic*"},
		{`\<b\>`, "&lt;b&gt;"},
		{`a \\ b`, `a \ b`},
		{`trailing \`, `trailing \`},
		{`\x is kept`, `\x is kept`},

		// Links show their text and nothing else.
		{"[text](http://x.y/?a=1&b=2)", "text"},
		{"see [the docs](docs/a_b.md) here", "see the docs here"},
		{"[**bold** link](u)", "<b>bold</b> link"},
		{"[a [b] c](u)", "a [b] c"},
		{"[t](u(1))", "t"},
		{"[<b>](u)", "&lt;b&gt;"},
		{"![alt <img>](p.png)", "alt &lt;img&gt;"},
		{"[[Note]]", "Note"},
		{"[[Note|the alias]]", "the alias"},
		{"[[Folder/Note#Heading]]", "Folder/Note#Heading"},
		{"[[a & b]]", "a &amp; b"},
		{"[not a link]", "[not a link]"},
		{"[not a link] (x)", "[not a link] (x)"},
		{"[[]]", "[[]]"},
		{"[t](unclosed", "[t](unclosed"},
		{"!not an image", "!not an image"},
		{"wow! [x](y)", "wow! x"},

		// Text that is not text.
		{"a\x01b", "a b"},
		{"a\x00b", "a b"},
		{"a\tb", "a b"},
		{"a\xffb", "a�b"},
		{"\x7f", " "},
	}
	for _, c := range cases {
		if got := cellMarkup(c.in); got != c.want {
			t.Errorf("cellMarkup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// wellFormed checks that s is markup a label will take, made only of the tags
// cellMarkup writes, opened and closed in order, and of escaped text.
func wellFormed(s string) bool {
	var stack []string
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return false
			}
			tag := s[i+1 : i+end]
			i += end + 1
			switch tag {
			case "b", "i", "s", "tt":
				stack = append(stack, tag)
			case "/b", "/i", "/s", "/tt":
				if n := len(stack); n == 0 || "/"+stack[n-1] != tag {
					return false
				}
				stack = stack[:len(stack)-1]
			default:
				return false
			}
		case '>', '"', '\'':
			return false
		case '&':
			end := strings.IndexByte(s[i:], ';')
			if end < 0 {
				return false
			}
			switch s[i : i+end+1] {
			case "&amp;", "&lt;", "&gt;", "&quot;", "&apos;":
			default:
				return false
			}
			i += end + 1
		default:
			if c < ' ' || c == 0x7f {
				return false
			}
			i++
		}
	}
	return len(stack) == 0
}

func TestCellMarkupIsAlwaysWellFormed(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pieces := []string{
		"*", "**", "_", "__", "~~", "`", "``", "[", "]", "(", ")", "![", "[[", "]]", `\`, "<", ">",
		"&", `"`, "'", " ", "a", "b", "|", "\x01", "\xff", "<b>", "</b>", "&amp;", "é",
	}
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for k := rng.Intn(14); k >= 0; k-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		in := b.String()
		if got := cellMarkup(in); !wellFormed(got) {
			t.Fatalf("cellMarkup(%q) = %q, which is not well formed", in, got)
		}
	}
	// Deep nesting stops, and stays well formed.
	deep := strings.Repeat("**a *b ~~c ", 10) + strings.Repeat("~~ * **", 10)
	if got := cellMarkup(deep); !wellFormed(got) {
		t.Errorf("cellMarkup(%q) = %q, which is not well formed", deep, got)
	}
	deep = strings.Repeat("[", 30) + "x" + strings.Repeat("](u)", 30)
	if got := cellMarkup(deep); !wellFormed(got) {
		t.Errorf("cellMarkup of nested links = %q, which is not well formed", got)
	}
}

func TestTablePad(t *testing.T) {
	cases := []struct {
		px   int
		name string
		n    int
	}{
		{-5, "", 0},
		{0, "", 0},
		{1, "table-pad-4", 4},
		{4, "table-pad-4", 4},
		{5, "table-pad-8", 8},
		{100, "table-pad-100", 100},
		{101, "table-pad-104", 104},
	}
	for _, c := range cases {
		name, n := tablePad(c.px)
		if name != c.name || n != c.n {
			t.Errorf("tablePad(%d) = %q, %d, want %q, %d", c.px, name, n, c.name, c.n)
		}
		// The tag's own name gives back the space it stands for.
		if got := padPixels(name); got != c.n {
			t.Errorf("padPixels(%q) = %d, want %d", name, got, c.n)
		}
	}
}

func TestIsTableRow(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"| a |", true},
		{"a | b", true},
		{"|", true},
		{"a", false},
		{"", false},
		{"￼ | b", false},
	}
	for _, c := range cases {
		if got := isTableRow(c.line); got != c.want {
			t.Errorf("isTableRow(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
