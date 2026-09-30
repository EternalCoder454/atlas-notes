package diagram

import (
	"regexp"
	"strings"
)

const (
	maxParticipants = 30
	maxSeqEvents    = 250
	maxSeqDepth     = 6
	maxSeqText      = 160
)

type seqKind int

const (
	evMsg seqKind = iota
	evNote
	evBlock
	evActivate
	evDeactivate
)

// Participant is a box at the top of a sequence diagram with a lifeline below.
type Participant struct {
	ID    string
	Label string
	Actor bool // drawn as a figure
}

// SeqEvent is one step of a sequence diagram: a message, a note, an activation
// change or a block of steps.
type SeqEvent struct {
	Kind seqKind
	A, B int // participants: the sender and receiver, or a note's one or two

	Text       string
	Dashed     bool
	Head       byte // '>' filled, ')' open, 'x' a cross, 0 none
	Both       bool // with a head at the sender too
	Activate   bool // the receiver becomes active
	Deactivate bool // the sender stops being active
	Side       byte // a note: 'l' left of, 'r' right of, 'o' over
	Block      *SeqBlock
}

// SeqBlock is loop, alt, opt, par, critical, break or rect: the frame round
// some steps, which Parts cut up (alt and else, par and and).
type SeqBlock struct {
	Kind  string
	Parts []*SeqPart
}

// SeqPart is the steps under one label of a block.
type SeqPart struct {
	Label  string
	Events []*SeqEvent
}

// Sequence is a parsed sequence diagram.
type Sequence struct {
	Title      string
	Parts      []*Participant
	Events     []*SeqEvent
	AutoNumber bool

	byID   map[string]int
	count  int // events so far
	nMsgs  int
	active []int
}

var (
	reSeqMsg  = regexp.MustCompile(`^(\S+?)\s*(<<-->>|<<->>|-->>|->>|--x|-x|--\)|-\)|-->|->)\s*([+-]?)\s*([^\s:]+)\s*(?::\s*(.*))?$`)
	reSeqNote = regexp.MustCompile(`(?i)^note\s+(left\s+of|right\s+of|over)\s+([^:]+?)\s*:\s*(.*)$`)
	reSeqPart = regexp.MustCompile(`^(participant|actor)\s+(\S+)(?:\s+as\s+(.+))?$`)
)

var seqBlocks = map[string]string{"loop": "else", "alt": "else", "opt": "", "par": "and", "critical": "option", "break": "", "rect": ""}

// ParseSequence reads a Mermaid sequence diagram.
func ParseSequence(src string) (s *Sequence, err error) {
	if len(src) > maxSource {
		return nil, &UnsupportedError{Msg: "the diagram is too large"}
	}
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, &UnsupportedError{Msg: "the diagram could not be read"}
		}
	}()
	s = &Sequence{byID: map[string]int{}}
	var stack []*SeqBlock
	add := func(e *SeqEvent) {
		if len(stack) == 0 {
			s.Events = append(s.Events, e)
			return
		}
		p := stack[len(stack)-1].Parts
		p[len(p)-1].Events = append(p[len(p)-1].Events, e)
	}
	header := false
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
		lw := strings.ToLower(word)
		if _, ok := seqBlocks[lw]; ok && !reSeqMsg.MatchString(line) || lw == "end" && rest == "" {
			if lw == "end" {
				if len(stack) == 0 {
					return nil, errf(n, "end closes nothing")
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if len(stack) >= maxSeqDepth {
				return nil, errf(n, "blocks are nested too deep")
			}
			if err := s.count1(n); err != nil {
				return nil, err
			}
			b := &SeqBlock{Kind: lw, Parts: []*SeqPart{{Label: plainLabel(rest)}}}
			add(&SeqEvent{Kind: evBlock, Block: b})
			stack = append(stack, b)
			continue
		}
		if len(stack) > 0 {
			b := stack[len(stack)-1]
			if sep := seqBlocks[b.Kind]; sep != "" && lw == sep {
				b.Parts = append(b.Parts, &SeqPart{Label: plainLabel(rest)})
				continue
			}
		}
		switch lw {
		case "else", "and", "option":
			return nil, errf(n, "%s is outside the block it belongs to", lw)
		case "title":
			s.Title = plainLabel(strings.TrimPrefix(rest, ":"))
			continue
		case "autonumber":
			s.AutoNumber = !strings.EqualFold(rest, "off")
			continue
		case "activate", "deactivate":
			if rest == "" || strings.ContainsAny(rest, " ,") {
				return nil, errf(n, "%s needs one participant", lw)
			}
			if err := s.count1(n); err != nil {
				return nil, err
			}
			k, err := s.part(n, rest)
			if err != nil {
				return nil, err
			}
			ev := &SeqEvent{Kind: evActivate, A: k}
			if lw == "deactivate" {
				ev.Kind = evDeactivate
			}
			if err := s.track(n, ev); err != nil {
				return nil, err
			}
			add(ev)
			continue
		case "create", "destroy", "box", "link", "links", "properties", "details", "accdescr", "acctitle":
			return nil, errf(n, "%q is not supported", word)
		}
		if m := reSeqPart.FindStringSubmatch(line); m != nil {
			if strings.Contains(m[2], "@{") {
				return nil, errf(n, "participant options are not supported")
			}
			k, err := s.part(n, m[2])
			if err != nil {
				return nil, err
			}
			s.Parts[k].Actor = m[1] == "actor"
			if m[3] != "" {
				s.Parts[k].Label = plainLabel(m[3])
			}
			continue
		}
		if m := reSeqNote.FindStringSubmatch(line); m != nil {
			if err := s.count1(n); err != nil {
				return nil, err
			}
			ev := &SeqEvent{Kind: evNote, Text: plainLabel(m[3])}
			switch strings.ToLower(m[1])[0] {
			case 'l':
				ev.Side = 'l'
			case 'r':
				ev.Side = 'r'
			default:
				ev.Side = 'o'
			}
			ids := strings.Split(m[2], ",")
			if len(ids) > 2 || (ev.Side != 'o' && len(ids) > 1) {
				return nil, errf(n, "a note is beside one participant, or over one or two")
			}
			for j, id := range ids {
				k, err := s.part(n, strings.TrimSpace(id))
				if err != nil {
					return nil, err
				}
				if j == 0 {
					ev.A, ev.B = k, k
				} else {
					ev.B = k
				}
			}
			if ev.A > ev.B {
				ev.A, ev.B = ev.B, ev.A
			}
			add(ev)
			continue
		}
		if m := reSeqMsg.FindStringSubmatch(line); m != nil {
			if err := s.count1(n); err != nil {
				return nil, err
			}
			from, err := s.part(n, m[1])
			if err != nil {
				return nil, err
			}
			to, err := s.part(n, m[4])
			if err != nil {
				return nil, err
			}
			ev := &SeqEvent{Kind: evMsg, A: from, B: to, Text: truncate(plain(m[5]), maxSeqText)}
			arrow := m[2]
			ev.Both = strings.HasPrefix(arrow, "<<")
			ev.Dashed = strings.Contains(arrow, "--")
			switch {
			case strings.HasSuffix(arrow, ">>"):
				ev.Head = '>'
			case strings.HasSuffix(arrow, "x"):
				ev.Head = 'x'
			case strings.HasSuffix(arrow, ")"):
				ev.Head = ')'
			}
			ev.Activate, ev.Deactivate = m[3] == "+", m[3] == "-"
			if err := s.track(n, ev); err != nil {
				return nil, err
			}
			s.nMsgs++
			add(ev)
			continue
		}
		return nil, errf(n, "cannot read %q", clip(line, 24))
	}
	if !header {
		return nil, &UnsupportedError{Msg: "the diagram is empty"}
	}
	if len(stack) > 0 {
		return nil, &UnsupportedError{Msg: "a " + stack[len(stack)-1].Kind + " block is never closed with end"}
	}
	if len(s.Parts) == 0 {
		return nil, &UnsupportedError{Msg: "the diagram has no participants"}
	}
	return s, nil
}

func (s *Sequence) count1(line int) error {
	if s.count++; s.count > maxSeqEvents {
		return errf(line, "too many steps")
	}
	return nil
}

// part finds a participant by id, adding one that has not been named yet.
func (s *Sequence) part(line int, id string) (int, error) {
	if k, ok := s.byID[id]; ok {
		return k, nil
	}
	if id == "" || len(id) > 60 {
		return 0, errf(line, "cannot read the participant %q", clip(id, 20))
	}
	if len(s.Parts) >= maxParticipants {
		return 0, errf(line, "too many participants")
	}
	s.Parts = append(s.Parts, &Participant{ID: id, Label: truncate(plain(id), 40)})
	s.byID[id] = len(s.Parts) - 1
	s.active = append(s.active, 0)
	return len(s.Parts) - 1, nil
}

// track keeps count of who is active, so that a deactivation that has nothing to
// end is an error rather than a bar drawn upside down.
func (s *Sequence) track(line int, ev *SeqEvent) error {
	switch {
	case ev.Kind == evActivate:
		s.active[ev.A]++
	case ev.Kind == evDeactivate:
		if s.active[ev.A] == 0 {
			return errf(line, "%s is deactivated without being active", clip(s.Parts[ev.A].ID, 20))
		}
		s.active[ev.A]--
	case ev.Kind == evMsg:
		if ev.Activate {
			s.active[ev.B]++
		}
		if ev.Deactivate {
			if s.active[ev.A] == 0 {
				return errf(line, "%s is deactivated without being active", clip(s.Parts[ev.A].ID, 20))
			}
			s.active[ev.A]--
		}
	}
	return nil
}
