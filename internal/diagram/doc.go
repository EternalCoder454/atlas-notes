package diagram

import "strings"

// Doc is a parsed diagram of any kind this package draws. Scene lays it out with
// m measuring text for a page avail wide (0 for its natural size). The exporters
// and the editor go through it, so a new kind of diagram needs no change to
// either.
type Doc interface {
	Scene(m Measure, avail float64) *Scene
}

// Scene lays the flowchart out, wrapping boxes narrower to fit avail.
func (g *Graph) Scene(m Measure, avail float64) *Scene {
	if avail <= 0 {
		return Layout(g, m)
	}
	return LayoutFit(g, m, avail)
}

// Kind names what a source is, from its first line: "flowchart", "gantt",
// "sequence" or "" when it is something else (or nothing).
func Kind(src string) string {
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		f := strings.Fields(line)
		switch strings.ToLower(strings.TrimRight(f[0], ";")) {
		case "flowchart", "graph":
			return "flowchart"
		case "gantt":
			return "gantt"
		case "sequencediagram":
			return "sequence"
		}
		return ""
	}
	return ""
}

// ParseDoc reads a Mermaid flowchart, Gantt chart or sequence diagram. What is
// not one of those, or uses something that is not supported, comes back as an
// *UnsupportedError saying what.
func ParseDoc(src string) (Doc, error) {
	switch Kind(src) {
	case "gantt":
		return ParseGantt(src)
	case "sequence":
		return ParseSequence(src)
	}
	g, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ParseDocDrawable is ParseDoc for what is going to be drawn: one that would
// come out absurdly large is refused, like one that cannot be read. The size is
// judged with estimated text, so it costs no font.
func ParseDocDrawable(src string) (Doc, error) {
	d, err := ParseDoc(src)
	if err != nil {
		return nil, err
	}
	if d.Scene(nil, 0).TooLarge() {
		return nil, &UnsupportedError{Msg: "the diagram is too large"}
	}
	return d, nil
}
