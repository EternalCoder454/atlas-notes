package diagram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Now is the clock the today line reads; tests set it.
var Now = time.Now

const (
	maxTasks    = 200
	maxSections = 40
	maxDuration = 100000 // the largest number in a duration
	maxWalk     = 200000 // day steps taken to skip excluded days
)

// Task is a bar, or with Milestone a diamond, on a Gantt chart. Start and End
// are set once the chart is parsed.
type Task struct {
	Name       string
	ID         string
	Done       bool
	Active     bool
	Crit       bool
	Milestone  bool
	Start, End time.Time
	Section    *Section

	line  int
	after []string // ids it starts after
	prev  *Task    // the task before it, when it says no start
	start time.Time
	hasSt bool
	endT  time.Time
	dur   time.Duration
	durD  int // whole days of dur, to be counted without excluded days
	byEnd bool
	state int
	unit  byte
}

// Section is a named band of tasks. Tasks before any section are in one with no
// name.
type Section struct {
	Name  string
	Tasks []*Task
}

// Gantt is a parsed Gantt chart.
type Gantt struct {
	Title      string
	Sections   []*Section
	Tasks      []*Task
	AxisFormat string // strftime-style, "" to choose by the zoom
	NoToday    bool

	format    []dateTok
	hasFormat bool
	exclWeek  [7]bool
	exclDates map[string]bool
	inclusive bool
	ids       map[string]*Task
}

type dateTok struct {
	kind byte // 'Y' year, 'y' two-digit year, 'M', 'D', 'H', 'm', 's', 0 literal
	n    int  // digits: 4, 2 or 1 (one or two)
	lit  byte
}

var dateTokens = []struct {
	s    string
	kind byte
	n    int
}{
	{"YYYY", 'Y', 4}, {"YY", 'y', 2}, {"MM", 'M', 2}, {"DD", 'D', 2}, {"HH", 'H', 2}, {"mm", 'm', 2}, {"ss", 's', 2},
	{"M", 'M', 1}, {"D", 'D', 1}, {"H", 'H', 1}, {"m", 'm', 1}, {"s", 's', 1},
}

func parseDateFormat(f string) ([]dateTok, bool) {
	var out []dateTok
	hasYear := false
	for i := 0; i < len(f); {
		matched := false
		for _, t := range dateTokens {
			if strings.HasPrefix(f[i:], t.s) {
				out = append(out, dateTok{kind: t.kind, n: t.n})
				if t.kind == 'Y' || t.kind == 'y' {
					hasYear = true
				}
				i += len(t.s)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		c := f[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= 0x80 {
			return nil, false // a token this reader does not know
		}
		out = append(out, dateTok{lit: c})
		i++
	}
	return out, hasYear && len(out) <= 40
}

var defaultFormat, _ = parseDateFormat("YYYY-MM-DD")

func readDigits(s string, pos, exact, most int) (int, int, bool) {
	n, v := 0, 0
	for pos+n < len(s) && n < most && s[pos+n] >= '0' && s[pos+n] <= '9' {
		v = v*10 + int(s[pos+n]-'0')
		n++
	}
	if n == 0 || (exact > 0 && n != exact) {
		return 0, pos, false
	}
	return v, pos + n, true
}

// parseDate reads s in the given format, or reports that it is not one.
func parseDate(toks []dateTok, s string) (time.Time, bool) {
	y, mo, d, h, mi, sec := 0, 1, 1, 0, 0, 0
	pos := 0
	for _, t := range toks {
		if t.kind == 0 {
			if pos >= len(s) || s[pos] != t.lit {
				return time.Time{}, false
			}
			pos++
			continue
		}
		exact, most := 0, 2
		if t.kind == 'Y' {
			exact, most = 4, 4
		} else if t.n == 2 {
			exact = 2
		}
		v, np, ok := readDigits(s, pos, exact, most)
		if !ok {
			return time.Time{}, false
		}
		pos = np
		switch t.kind {
		case 'Y':
			y = v
		case 'y':
			y = 2000 + v
		case 'M':
			mo = v
		case 'D':
			d = v
		case 'H':
			h = v
		case 'm':
			mi = v
		case 's':
			sec = v
		}
	}
	if pos != len(s) || y < 1 || mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC)
	if t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

var reDuration = regexp.MustCompile(`^(\d+(?:\.\d+)?)(ms|s|m|h|d|w|M|y)$`)

// parseDuration reads 3d, 1w, 12h; the last result is whole days when the unit
// is days or weeks.
func parseDuration(s string) (time.Duration, int, byte, bool) {
	m := reDuration.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n > maxDuration {
		return 0, 0, 0, false
	}
	var unit time.Duration
	var u byte
	switch m[2] {
	case "ms":
		unit, u = time.Millisecond, 'x'
	case "s":
		unit, u = time.Second, 'x'
	case "m":
		unit, u = time.Minute, 'x'
	case "h":
		unit, u = time.Hour, 'x'
	case "d":
		unit, u = 24*time.Hour, 'd'
	case "w":
		unit, u = 7*24*time.Hour, 'd'
	case "M":
		unit, u = 30*24*time.Hour, 'x'
	case "y":
		unit, u = 365*24*time.Hour, 'x'
	}
	if n*float64(unit) > 3e18 {
		return 0, 0, 0, false
	}
	d := time.Duration(n * float64(unit))
	days := 0
	if u == 'd' && n == float64(int(n)) {
		days = int(d / (24 * time.Hour))
	}
	return d, days, u, true
}

// ParseGantt reads a Mermaid Gantt chart.
func ParseGantt(src string) (g *Gantt, err error) {
	if len(src) > maxSource {
		return nil, &UnsupportedError{Msg: "the diagram is too large"}
	}
	defer func() {
		if r := recover(); r != nil {
			g, err = nil, &UnsupportedError{Msg: "the diagram could not be read"}
		}
	}()
	g = &Gantt{ids: map[string]*Task{}, exclDates: map[string]bool{}, format: defaultFormat}
	var sec *Section
	header := false
	var prev *Task
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r", ""), "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		if !header {
			header = true
			continue
		}
		word, rest := line, ""
		if k := strings.IndexAny(line, " \t"); k >= 0 {
			word, rest = line[:k], strings.TrimSpace(line[k:])
		}
		switch strings.ToLower(word) {
		case "title":
			g.Title = plainLabel(rest)
		case "dateformat":
			f, ok := parseDateFormat(rest)
			if !ok {
				return nil, errf(n, "the date format %q is not supported", clip(rest, 20))
			}
			g.format, g.hasFormat = f, true
		case "axisformat":
			g.AxisFormat = clip(rest, 40)
		case "todaymarker":
			if strings.EqualFold(rest, "off") {
				g.NoToday = true
			} else if rest != "" {
				g.NoToday = false // a style for the line; it keeps the theme's
			}
		case "excludes":
			for _, w := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' }) {
				if err := g.exclude(strings.ToLower(w)); err != nil {
					return nil, errf(n, "%v", err)
				}
			}
		case "includes", "weekday", "tickinterval", "topaxis", "accdescr", "acctitle":
			// Nothing in them changes what is drawn here.
		case "inclusiveenddates":
			g.inclusive = true
		case "click", "vert":
			return nil, errf(n, "%q is not supported", word)
		case "section":
			if len(g.Sections) >= maxSections {
				return nil, errf(n, "too many sections")
			}
			sec = &Section{Name: plainLabel(rest)}
			g.Sections = append(g.Sections, sec)
		default:
			k := strings.LastIndex(line, ":")
			if k < 0 {
				return nil, errf(n, "cannot read %q", clip(line, 24))
			}
			if len(g.Tasks) >= maxTasks {
				return nil, errf(n, "too many tasks")
			}
			if sec == nil {
				sec = &Section{}
				g.Sections = append(g.Sections, sec)
			}
			t, err := g.task(n, strings.TrimSpace(line[:k]), line[k+1:])
			if err != nil {
				return nil, err
			}
			t.Section, t.prev = sec, prev
			sec.Tasks = append(sec.Tasks, t)
			g.Tasks = append(g.Tasks, t)
			if t.ID != "" {
				if g.ids[t.ID] != nil {
					return nil, errf(n, "the task id %q is used twice", t.ID)
				}
				g.ids[t.ID] = t
			}
			prev = t
		}
	}
	if !header {
		return nil, &UnsupportedError{Msg: "the diagram is empty"}
	}
	if len(g.Tasks) == 0 {
		return nil, &UnsupportedError{Msg: "the chart has no tasks"}
	}
	for _, t := range g.Tasks {
		if err := g.resolve(t); err != nil {
			return nil, err
		}
	}
	return g, nil
}

var weekdays = map[string]int{"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4, "friday": 5, "saturday": 6}

func (g *Gantt) exclude(w string) error {
	if w == "weekends" || w == "weekend" {
		g.exclWeek[0], g.exclWeek[6] = true, true
		return nil
	}
	if d, ok := weekdays[w]; ok {
		g.exclWeek[d] = true
		return nil
	}
	if t, ok := parseDate(defaultFormat, w); ok {
		if len(g.exclDates) < 400 {
			g.exclDates[t.Format("2006-01-02")] = true
		}
		return nil
	}
	return fmt.Errorf("cannot exclude %q", clip(w, 20))
}

func (g *Gantt) excluded(t time.Time) bool {
	if g.exclWeek[t.Weekday()] {
		return true
	}
	return len(g.exclDates) > 0 && g.exclDates[t.Format("2006-01-02")]
}

func (g *Gantt) anyExcluded() bool {
	for _, b := range g.exclWeek {
		if b {
			return true
		}
	}
	return len(g.exclDates) > 0
}

var reID = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,40}$`)

func (g *Gantt) task(line int, name, spec string) (*Task, error) {
	t := &Task{Name: plainLabel(name), line: line}
	if t.Name == "" {
		return nil, errf(line, "a task needs a name")
	}
	var f []string
	for _, s := range strings.Split(spec, ",") {
		f = append(f, strings.TrimSpace(s))
	}
	for len(f) > 0 {
		switch f[0] {
		case "done":
			t.Done = true
		case "active":
			t.Active = true
		case "crit":
			t.Crit = true
		case "milestone":
			t.Milestone = true
		default:
			goto tags
		}
		f = f[1:]
	}
tags:
	isStart := func(s string) bool {
		if _, ok := parseDate(g.format, s); ok {
			return true
		}
		return strings.HasPrefix(s, "after ") || s == "after"
	}
	var startS, endS string
	switch len(f) {
	case 0:
		return nil, errf(line, "the task %q has no dates", clip(t.Name, 24))
	case 1:
		endS = f[0]
	case 2:
		if !isStart(f[0]) && reID.MatchString(f[0]) {
			t.ID, endS = f[0], f[1]
		} else {
			startS, endS = f[0], f[1]
		}
	case 3:
		if !reID.MatchString(f[0]) {
			return nil, errf(line, "%q is not a task id", clip(f[0], 20))
		}
		t.ID, startS, endS = f[0], f[1], f[2]
	default:
		return nil, errf(line, "too many fields in the task %q", clip(t.Name, 24))
	}
	if startS != "" {
		if d, ok := parseDate(g.format, startS); ok {
			t.start, t.hasSt = d, true
		} else if rest, ok := strings.CutPrefix(startS, "after"); ok && (rest == "" || rest[0] == ' ') {
			t.after = strings.Fields(rest)
			if len(t.after) == 0 || len(t.after) > 20 {
				return nil, errf(line, "after needs a task id")
			}
		} else {
			return nil, errf(line, "cannot read the start %q", clip(startS, 20))
		}
	}
	if d, ok := parseDate(g.format, endS); ok {
		t.endT, t.byEnd = d, true
		if g.inclusive {
			t.endT = t.endT.AddDate(0, 0, 1)
		}
	} else if dur, days, u, ok := parseDuration(endS); ok {
		t.dur, t.durD, t.unit = dur, days, u
	} else {
		return nil, errf(line, "cannot read the end %q", clip(endS, 20))
	}
	return t, nil
}

// resolve works out a task's dates, after those it follows.
func (g *Gantt) resolve(t *Task) error {
	if t.state == 2 {
		return nil
	}
	if t.state == 1 {
		return errf(t.line, "the task %q follows itself", clip(t.Name, 24))
	}
	t.state = 1
	switch {
	case t.hasSt:
		t.Start = t.start
	case len(t.after) > 0:
		for i, id := range t.after {
			d := g.ids[id]
			if d == nil {
				return errf(t.line, "there is no task %q to start after", clip(id, 20))
			}
			if err := g.resolve(d); err != nil {
				return err
			}
			if i == 0 || d.End.After(t.Start) {
				t.Start = d.End
			}
		}
	case t.prev != nil:
		if err := g.resolve(t.prev); err != nil {
			return err
		}
		t.Start = t.prev.End
	default:
		return errf(t.line, "the task %q needs a start date", clip(t.Name, 24))
	}
	if t.byEnd {
		t.End = t.endT
		if t.End.Before(t.Start) {
			return errf(t.line, "the task %q ends before it starts", clip(t.Name, 24))
		}
	} else if t.unit == 'd' && t.durD > 0 && g.anyExcluded() {
		d, n := t.Start, 0
		for steps := 0; n < t.durD; steps++ {
			if steps > maxWalk {
				return errf(t.line, "every day is excluded")
			}
			if !g.excluded(d) {
				n++
			}
			d = d.AddDate(0, 0, 1)
		}
		t.End = d
	} else {
		t.End = t.Start.Add(t.dur)
	}
	if t.End.Year() > 9999 || t.End.Year() < 1 {
		return errf(t.line, "the dates are out of range")
	}
	t.state = 2
	return nil
}
