package editor

import (
	"fmt"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The tint that shows where someone else has just changed the open note, such
// as Claude Code editing it: the changed lines light up, hold for a moment so
// the eye finds them, and fade out. It is a paragraph background and nothing
// else, so it never changes which characters are hidden, and is safe to change
// while text is being selected (see reparse).

const (
	flashHold  = 900 * time.Millisecond  // how long the tint stays at full strength
	flashFade  = 1100 * time.Millisecond // how long it then takes to fade out
	flashStep  = 40                      // ms between steps of the fade
	flashAlpha = 0.26                    // strongest the tint gets
)

// flashRGB is the tint's colour, a warm coral that reads on light and dark
// themes alike and is not the colour of anything else in a note.
const flashRGB = "217,119,87"

// changeFlash is the editor's one tint. A new flash replaces the last one.
type changeFlash struct {
	tag    *gtk.TextTag
	ranges [][2]*gtk.TextMark // start and end of each tinted run of lines
	gen    uint64             // which flash the running timer belongs to
}

// FlashLines tints the given runs of lines, each [from, to] counted from 0 and
// inclusive, and fades the tint out. Lines past the end are ignored. With the
// desktop's animations off the tint does not fade; it is there, then gone.
func (e *Editor) FlashLines(runs [][2]int) {
	if e.view == nil || len(runs) == 0 {
		return
	}
	f := e.flash
	if f == nil {
		// Made after every other tag, so its background wins over a code
		// block's while it lasts.
		f = &changeFlash{tag: gtk.NewTextTag("change-flash")}
		e.buffer.TagTable().Add(f.tag)
		e.flash = f
	}
	f.stop(e)
	last := e.buffer.LineCount() - 1
	for _, r := range e.outsideBlocks(runs) {
		from, to := max(r[0], 0), min(r[1], last)
		if from > to {
			continue
		}
		start, _ := e.buffer.IterAtLine(from)
		// The run ends at the start of the line after it, so an empty line is
		// tinted too: a paragraph background needs a character to hang on.
		end, ok := e.buffer.IterAtLine(to + 1)
		if !ok || to == last {
			_, end = e.buffer.Bounds()
		}
		f.ranges = append(f.ranges, [2]*gtk.TextMark{
			e.buffer.CreateMark("", start, true),
			// Left gravity at the end too, so typing at the start of the next
			// line does not pull that line into the run.
			e.buffer.CreateMark("", end, true),
		})
	}
	e.flashBlocks(runs)
	if len(f.ranges) == 0 {
		return
	}
	// Above every tag, including ones made since the last flash, such as the
	// find highlights, so its background is the one that shows.
	f.tag.SetPriority(e.buffer.TagTable().Size() - 1)
	f.setAlpha(flashAlpha)
	f.apply(e)

	f.gen++
	gen := f.gen
	if !animationsOn() {
		coreglib.TimeoutAdd(uint((flashHold + flashFade).Milliseconds()), func() bool {
			if f.gen == gen {
				f.stop(e)
			}
			return false
		})
		return
	}
	began := time.Now()
	coreglib.TimeoutAdd(flashStep, func() bool {
		if f.gen != gen {
			return false // replaced by a newer flash, or stopped
		}
		since := time.Since(began)
		if since < flashHold {
			return true
		}
		p := float64(since-flashHold) / float64(flashFade)
		if p >= 1 {
			f.stop(e)
			return false
		}
		// Eased out, so the tint lets go quickly at first and lingers faintly.
		f.setAlpha(flashAlpha * (1 - p) * (1 - p))
		return true
	})
}

func (f *changeFlash) setAlpha(a float64) {
	f.tag.SetObjectProperty("paragraph-background", fmt.Sprintf("rgba(%s,%.3f)", flashRGB, a))
}

// apply puts the tag on its runs. A render pass strips every tag from the
// lines it re-tags, this one included, so reparse calls it again afterwards.
func (f *changeFlash) apply(e *Editor) {
	for _, r := range f.ranges {
		e.buffer.ApplyTag(f.tag, e.buffer.IterAtMark(r[0]), e.buffer.IterAtMark(r[1]))
	}
}

// restore re-applies a flash still showing after a render pass.
func (f *changeFlash) restore(e *Editor) {
	if f != nil && len(f.ranges) > 0 {
		f.apply(e)
	}
}

// stop takes the tint off and lets go of its marks.
func (f *changeFlash) stop(e *Editor) {
	if f == nil {
		return
	}
	f.gen++
	if len(f.ranges) == 0 {
		return
	}
	start, end := e.buffer.Bounds()
	e.buffer.RemoveTag(f.tag, start, end)
	for _, r := range f.ranges {
		e.buffer.DeleteMark(r[0])
		e.buffer.DeleteMark(r[1])
	}
	f.ranges = nil
}

// outsideBlocks is runs less the lines of drawn tables and diagrams, which
// flashBlocks shows instead.
func (e *Editor) outsideBlocks(runs [][2]int) [][2]int {
	var blocks [][2]int
	for _, it := range e.tbl.items {
		if it.grid != nil && it.mark != nil {
			l := e.markLine(it.mark)
			blocks = append(blocks, [2]int{l, l + it.t.lines - 1})
		}
	}
	for _, it := range e.dia.items {
		if it.box != nil && it.mark != nil {
			l := e.markLine(it.mark)
			blocks = append(blocks, [2]int{l, l + it.lines - 1})
		}
	}
	out := runs
	for _, b := range blocks {
		var next [][2]int
		for _, r := range out {
			if r[1] < b[0] || r[0] > b[1] {
				next = append(next, r)
				continue
			}
			if r[0] < b[0] {
				next = append(next, [2]int{r[0], b[0] - 1})
			}
			if r[1] > b[1] {
				next = append(next, [2]int{b[1] + 1, r[1]})
			}
		}
		out = next
	}
	return out
}

// flashBlocks pulses the tables and diagrams a change landed in. Their source
// lines are hidden behind the drawn widget, so a tint on those lines would show
// only as a sliver; the widget takes the "change-flash" class instead, whose
// animation is in style.css.
func (e *Editor) flashBlocks(runs [][2]int) {
	hit := func(first, n int) bool {
		for _, r := range runs {
			if r[0] <= first+n-1 && r[1] >= first {
				return true
			}
		}
		return false
	}
	var widgets []*gtk.Widget
	for _, it := range e.tbl.items {
		if it.grid != nil && it.mark != nil && hit(e.markLine(it.mark), it.t.lines) {
			widgets = append(widgets, gtk.BaseWidget(it.grid.grid))
		}
	}
	for _, it := range e.dia.items {
		if it.box != nil && it.mark != nil && hit(e.markLine(it.mark), it.lines) {
			widgets = append(widgets, gtk.BaseWidget(it.box.root))
		}
	}
	if e.pulses == nil {
		e.pulses = map[*gtk.Widget]uint64{}
	}
	for _, w := range widgets {
		// The pulse a widget is on: an older pulse's timer finds a newer one
		// running and leaves it alone.
		e.pulses[w]++
		gen := e.pulses[w]
		// Taken off now and put back on the next turn of the main loop, so a
		// change while the last pulse runs starts it again: GTK works out the
		// style once a frame, and would see the same class both times.
		w.RemoveCSSClass("change-flash")
		coreglib.IdleAdd(func() bool {
			if e.pulses[w] == gen {
				w.AddCSSClass("change-flash")
			}
			return false
		})
		coreglib.TimeoutAdd(uint((flashHold + flashFade).Milliseconds()), func() bool {
			if e.pulses[w] == gen {
				w.RemoveCSSClass("change-flash")
				delete(e.pulses, w)
			}
			return false
		})
	}
}

// animationsOn reports whether the desktop wants things to move.
func animationsOn() bool {
	if st := gtk.SettingsGetDefault(); st != nil {
		if on, ok := st.ObjectProperty("gtk-enable-animations").(bool); ok {
			return on
		}
	}
	return true
}
