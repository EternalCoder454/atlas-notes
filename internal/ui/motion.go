package ui

import (
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Entrances are CSS keyframe animations, named in the stylesheet as a pair:
// "rise-a" and "rise-b" run the same keyframes under two names. GTK starts an
// animation when a widget's animation name changes and not otherwise, so
// swapping one class of the pair for the other is what plays it again.
//
// Animations follow the desktop's setting: with animations turned off, GTK
// draws the end state at once.

// MaxStagger is how many steps of delay the stylesheet defines (stagger-1 to
// stagger-MaxStagger); later widgets share the last.
const MaxStagger = 9

// Replay plays the named entrance on w from the start.
func Replay(w gtk.Widgetter, name string) {
	ReplayAt(w, name, 0)
}

// ReplayAt plays the named entrance on w after step steps of the stagger
// delay, so that a column of widgets can arrive one after another.
func ReplayAt(w gtk.Widgetter, name string, step int) {
	base := gtk.BaseWidget(w)
	if base == nil {
		return
	}
	a, b := name+"-a", name+"-b"
	if base.HasCSSClass(a) {
		base.RemoveCSSClass(a)
		base.AddCSSClass(b)
	} else {
		base.RemoveCSSClass(b)
		base.AddCSSClass(a)
	}
	for i := 1; i <= MaxStagger; i++ {
		base.RemoveCSSClass("stagger-" + strconv.Itoa(i))
	}
	if step = min(step, MaxStagger); step > 0 {
		base.AddCSSClass("stagger-" + strconv.Itoa(step))
	}
}

// ReplayChildren plays the entrance on each visible child of box in turn,
// starting at step first.
func ReplayChildren(box gtk.Widgetter, name string, first int) int {
	step := first
	for c := gtk.BaseWidget(box).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if !gtk.BaseWidget(c).Visible() {
			continue
		}
		ReplayAt(c, name, step)
		step++
	}
	return step
}
