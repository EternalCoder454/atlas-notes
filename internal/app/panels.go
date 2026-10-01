package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The side panels slide in and out rather than appearing and vanishing: the
// pane's divider runs between closed and the panel's width, and the panel
// fades with it. A toggle pressed while one is still moving turns it round
// from wherever it has got to.

const (
	panelOpenMs  = 260
	panelCloseMs = 200
)

// panelSlide is one side panel's animation.
type panelSlide struct {
	anim  *adw.TimedAnimation
	width int  // what the panel opens to
	right bool // the assistant, at the end of the inner pane
}

// running reports whether the panel is part way in or out.
func (s *panelSlide) running() bool {
	return s != nil && s.anim != nil && s.anim.State() == adw.AnimationPlaying
}

// slide returns the left or right panel's animation, made on first use.
func (a *App) slide(right bool) *panelSlide {
	i := 0
	if right {
		i = 1
	}
	if a.slides[i] == nil {
		s := &panelSlide{right: right}
		if right {
			s.width = panePosition(a.cfg.RightPanelWidth, rightPanelWidth)
		} else {
			s.width = panePosition(a.cfg.LeftPanelWidth, leftPanelWidth)
		}
		a.slides[i] = s
	}
	return a.slides[i]
}

// shown is how much of a panel is on screen now, in pixels.
func (a *App) shown(s *panelSlide) int {
	if s.right {
		if !a.right.Visible() {
			return 0
		}
		return max(a.innerPaned.Width()-a.innerPaned.Position(), 0)
	}
	if !a.left.Visible() {
		return 0
	}
	return a.outerPaned.Position()
}

// place puts the divider so that v pixels of the panel show.
func (a *App) place(s *panelSlide, v float64) {
	if s.right {
		a.innerPaned.SetPosition(a.innerPaned.Width() - int(v))
	} else {
		a.outerPaned.SetPosition(int(v))
	}
}

// panelParts is the panel and the pane it sits in.
func (a *App) panelParts(s *panelSlide) (*gtk.Box, *gtk.Paned) {
	if s.right {
		return a.right, a.innerPaned
	}
	return a.left, a.outerPaned
}

// panes are the panes a panel's slide resizes, innermost first.
func (a *App) panes(s *panelSlide) []*gtk.Paned {
	if s.right {
		return []*gtk.Paned{a.innerPaned, a.outerPaned}
	}
	return []*gtk.Paned{a.outerPaned}
}

// setPanel shows or hides a side panel, sliding it when the window is on
// screen.
func (a *App) setPanel(right, show bool) {
	s := a.slide(right)
	panel, paned := a.panelParts(s)
	if panel == nil || paned == nil {
		return
	}
	from := a.shown(s)
	if s.running() {
		s.anim.Pause() // turned round where it is; its done handler never runs
	} else if from > 0 {
		s.width = from // at rest: reopen to the width it was dragged to
	}
	// Folding the assistant away for a window too narrow for it snaps: a
	// slide would hold the window wide while the user drags it narrower.
	if a.win == nil || !a.win.Mapped() || benchMode != "" || a.fitting || paned.Width() <= 0 {
		a.restPanel(s, show)
		return
	}
	to, ms := 0, panelCloseMs
	if show {
		to, ms = s.width, panelOpenMs
	}
	if from == to {
		a.restPanel(s, show)
		return
	}

	// While it moves, the panel keeps its full width and the pane lets it
	// be cut off, so it slides out from under the page like a drawer
	// instead of squeezing its contents first.
	if right {
		paned.SetShrinkEndChild(true)
	} else {
		paned.SetShrinkStartChild(true)
	}
	panel.SetSizeRequest(s.width, -1)
	if show && !panel.Visible() {
		panel.SetOpacity(0)
		panel.SetVisible(true)
		a.place(s, 0)
	}
	a.syncPageCorners()
	// Measured at its narrowest with a panel that may be cut off, a pane
	// comes up a pixel short for the side that may not, and GTK warns as the
	// window works out its minimum size. The page's new border, which the
	// stylesheet has not applied yet, is a pixel more. Two to spare while
	// the panel moves covers both.
	// A pane may be held already, by the other panel's slide or by this one
	// turning round. It is measured afresh without that, as the panels now
	// stand.
	for _, p := range a.panes(s) {
		p.SetSizeRequest(-1, -1)
		if mn, _, _, _ := p.Measure(gtk.OrientationHorizontal, -1); mn > 0 {
			p.SetSizeRequest(mn+2, -1)
		}
	}

	width := float64(max(s.width, 1))
	target := adw.NewCallbackAnimationTarget(func(v float64) {
		a.place(s, v)
		panel.SetOpacity(min(max(v/width, 0), 1))
	})
	anim := adw.NewTimedAnimation(paned, float64(from), float64(to), uint(ms), target)
	if show {
		anim.SetEasing(adw.EaseOutCubic)
	} else {
		anim.SetEasing(adw.EaseInOutCubic)
	}
	anim.ConnectDone(func() {
		if s.anim == anim {
			a.restPanel(s, show)
		}
	})
	s.anim = anim
	anim.Play()
}

// restPanel leaves a panel fully in or fully out, as it was before panels
// moved.
func (a *App) restPanel(s *panelSlide, show bool) {
	panel, paned := a.panelParts(s)
	s.anim = nil
	panel.SetOpacity(1)
	if s.right {
		paned.SetShrinkEndChild(false)
		panel.SetSizeRequest(rightMinWidth, -1)
	} else {
		paned.SetShrinkStartChild(false)
		panel.SetSizeRequest(leftMinWidth, -1)
	}
	panel.SetVisible(show)
	other := a.slide(!s.right).running()
	for _, p := range a.panes(s) {
		if p == a.outerPaned && other {
			continue // the other panel is still moving in it
		}
		p.SetSizeRequest(-1, -1)
	}
	if show && paned.Width() > 0 {
		a.place(s, float64(s.width))
	}
	a.syncPageCorners()
}

// panelWidth is the width a panel is to be remembered at: where it is heading
// while it moves, rather than wherever it happens to be.
func (a *App) panelWidth(right bool) (int, bool) {
	s := a.slides[0]
	if right {
		s = a.slides[1]
	}
	if s.running() {
		return s.width, true
	}
	return 0, false
}
