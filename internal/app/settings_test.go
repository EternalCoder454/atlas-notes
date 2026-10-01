package app

import (
	"testing"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/theme"
)

func TestSettingsSectionFor(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"appearance": sectionAppearance,
		"Notes":      sectionNotes,
		" assistant": sectionAssistant,
		"UPDATES":    sectionUpdates,
		"about":      sectionAbout,
		// The three-page dialog's names, which screenshot scripts still use.
		"general":   sectionAppearance,
		"shortcuts": sectionAssistant,
		"app":       sectionUpdates,
		// Anything else is the top of the page, not an error.
		"nonsense": "",
	}
	for in, want := range cases {
		if got := settingsSectionFor(in); got != want {
			t.Errorf("settingsSectionFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampScroll(t *testing.T) {
	cases := []struct{ y, upper, page, want float64 }{
		{50, 1000, 300, 50},
		{-20, 1000, 300, 0},
		{900, 1000, 300, 700},
		{10, 200, 300, 0}, // shorter than the viewport: nothing to scroll
	}
	for _, c := range cases {
		if got := clampScroll(c.y, c.upper, c.page); got != c.want {
			t.Errorf("clampScroll(%v, %v, %v) = %v, want %v", c.y, c.upper, c.page, got, c.want)
		}
	}
}

// fakeClock stands in for the main loop's timers: it holds what was scheduled
// and lets a test say when they fire.
type fakeClock struct {
	next      int
	scheduled map[int]func()
	cancelled int
}

func newFakeClock() *fakeClock { return &fakeClock{scheduled: map[int]func(){}} }

func (c *fakeClock) after(_ uint, f func()) func() {
	c.next++
	id := c.next
	c.scheduled[id] = f
	return func() {
		delete(c.scheduled, id)
		c.cancelled++
	}
}

// fireAll runs what is scheduled, once, as the timers running out would.
func (c *fakeClock) fireAll() {
	pending := c.scheduled
	c.scheduled = map[int]func(){}
	for _, f := range pending {
		f()
	}
}

func TestDebouncerRunsOnceAfterTheLastTrigger(t *testing.T) {
	clock := newFakeClock()
	runs := 0
	d := &debouncer{delay: 300, fn: func() { runs++ }, after: clock.after}

	d.Trigger()
	d.Trigger()
	d.Trigger()
	if runs != 0 {
		t.Fatalf("ran %d times before the wait was up", runs)
	}
	if len(clock.scheduled) != 1 {
		t.Fatalf("%d timers live, want 1: each trigger must replace the last", len(clock.scheduled))
	}
	clock.fireAll()
	if runs != 1 {
		t.Fatalf("ran %d times, want 1", runs)
	}

	// Once it has fired it is idle, and starts afresh.
	d.Trigger()
	clock.fireAll()
	if runs != 2 {
		t.Fatalf("ran %d times after a second change, want 2", runs)
	}
}

func TestDebouncerFlush(t *testing.T) {
	clock := newFakeClock()
	runs := 0
	d := &debouncer{delay: 300, fn: func() { runs++ }, after: clock.after}

	d.Flush() // nothing pending: nothing to run
	if runs != 0 {
		t.Fatalf("flush with nothing pending ran %d times", runs)
	}

	d.Trigger()
	d.Flush()
	if runs != 1 {
		t.Fatalf("flush ran %d times, want 1", runs)
	}
	if len(clock.scheduled) != 0 {
		t.Fatalf("flush left %d timers, which would run it again", len(clock.scheduled))
	}
	d.Flush()
	if runs != 1 {
		t.Fatalf("a second flush ran it again: %d", runs)
	}
}

// A timer that has fired is finished; cancelling it afterwards is what GLib
// complains about, so the debouncer must not.
func TestDebouncerDoesNotCancelAFiredTimer(t *testing.T) {
	clock := newFakeClock()
	d := &debouncer{delay: 300, fn: func() {}, after: clock.after}
	d.Trigger()
	clock.fireAll()
	d.Trigger()
	d.Flush()
	if clock.cancelled != 1 {
		t.Fatalf("cancelled %d timers, want only the one still live at the flush", clock.cancelled)
	}
}

func TestComboIndexes(t *testing.T) {
	for i, c := range noteFormats {
		if got := noteFormatIndex(c); got != i {
			t.Errorf("noteFormatIndex(%q) = %d, want %d", c, got, i)
		}
	}
	if len(noteFormats) != len(noteFormatLabels) {
		t.Errorf("%d note formats but %d labels", len(noteFormats), len(noteFormatLabels))
	}
	if got := noteFormatIndex(storage.Compression("unknown")); got != 0 {
		t.Errorf("an unknown format shows as index %d, want plain Markdown at 0", got)
	}

	for i, l := range storage.TransparencyLevels {
		if got := transparencyIndex(l); got != i {
			t.Errorf("transparencyIndex(%q) = %d, want %d", l, got, i)
		}
	}
	if len(storage.TransparencyLevels) != len(transparencyLabels) {
		t.Errorf("%d transparency levels but %d labels", len(storage.TransparencyLevels), len(transparencyLabels))
	}
	if got := transparencyIndex("bogus"); got != 0 {
		t.Errorf("an unknown level shows as index %d, want off at 0", got)
	}

	for i, m := range fontRenderingModes {
		if got := fontModeIndex(m); got != i {
			t.Errorf("fontModeIndex(%q) = %d, want %d", m, got, i)
		}
	}
	if got := fontModeIndex("bogus"); got != 0 {
		t.Errorf("an unknown font mode shows as index %d, want automatic at 0", got)
	}

	modes := []string{storage.ActionModeShow, storage.ActionModeReplace, storage.ActionModeSort}
	if len(modes) != len(modeLabels) {
		t.Errorf("%d modes but %d labels", len(modes), len(modeLabels))
	}
	for i, m := range modes {
		if got := modeIndex(m); got != uint(i) {
			t.Errorf("modeIndex(%q) = %d, want %d", m, got, i)
		}
		if got := modeFromIndex(uint(i)); got != m {
			t.Errorf("modeFromIndex(%d) = %q, want %q", i, got, m)
		}
	}
}

func TestThemeDescription(t *testing.T) {
	if got := themeDescription("nord"); got != "Nord: Cool blue-grey, low contrast" {
		t.Errorf("themeDescription(nord) = %q", got)
	}
	// Following, and an id from a newer version this build lacks, both say so.
	for _, id := range []string{theme.Follow, "not-a-theme"} {
		if got := themeDescription(id); got == "" || got[0] == ':' {
			t.Errorf("themeDescription(%q) = %q, want the following line", id, got)
		}
	}
}

func TestUpdateStatusLine(t *testing.T) {
	if got := updateStatusLine(storage.ChannelRelease); got != "On the Release channel" {
		t.Errorf("release: %q", got)
	}
	if got := updateStatusLine(storage.ChannelBeta); got != "On the Beta channel" {
		t.Errorf("beta: %q", got)
	}
	// A channel from a newer version this build lacks is taken as Release,
	// as channelBranch takes it.
	if got := updateStatusLine("nightly"); got != "On the Release channel" {
		t.Errorf("unknown: %q", got)
	}
}
