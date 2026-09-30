package theme

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var hex = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// TestEveryThemeIsComplete: a theme missing a piece does not fail, it draws
// wrong — an empty Primary is a transparent half-circle in the picker, and a
// missing Name is a blank label under it.
func TestEveryThemeIsComplete(t *testing.T) {
	if len(Themes) != 10 {
		t.Errorf("got %d themes, want 10", len(Themes))
	}
	seen := map[string]bool{}
	for _, th := range Themes {
		if th.ID == "" || th.Name == "" || th.Summary == "" {
			t.Errorf("%+v is missing an id, name or summary", th)
		}
		if seen[th.ID] {
			t.Errorf("two themes share the id %q; the settings file could not tell them apart", th.ID)
		}
		seen[th.ID] = true

		// Lower-case six-digit hex, because SwatchCSS pastes these straight into a
		// gradient and CSS that does not parse leaves a circle with no colour at all.
		for label, c := range map[string]string{"Primary": th.Primary, "Secondary": th.Secondary} {
			if !hex.MatchString(c) {
				t.Errorf("%s: %s is %q, want lower-case #rrggbb", th.ID, label, c)
			}
		}
		if th.Primary == th.Secondary {
			t.Errorf("%s: both halves of the circle are %s, so it is not a split circle", th.ID, th.Primary)
		}
	}
}

// TestOverrideColoursAreValid: every value ends up in CSS, and a malformed one
// takes the rest of the declaration block with it.
func TestOverrideColoursAreValid(t *testing.T) {
	for _, th := range Themes {
		for name, c := range th.colors {
			if !hex.MatchString(c) {
				t.Errorf("%s: %s is %q, want lower-case #rrggbb", th.ID, name, c)
			}
			if strings.ContainsAny(name, " -") {
				t.Errorf("%s: colour name %q should be libadwaita's underscore form", th.ID, name)
			}
		}
	}
}

// TestLightAndDarkCarryNoOverrides: they are libadwaita's own palettes. Listing
// colours for them would be transcribing values that the toolkit already has and
// that change when it does.
func TestLightAndDarkCarryNoOverrides(t *testing.T) {
	for _, id := range []string{"light", "dark"} {
		th, ok := ByID(id)
		if !ok {
			t.Fatalf("%s is missing", id)
		}
		if len(th.colors) != 0 {
			t.Errorf("%s overrides %d colours; it should be libadwaita's own", id, len(th.colors))
		}
		if css := th.CSS(); css != "" {
			t.Errorf("%s has CSS of its own:\n%s", id, css)
		}
	}
}

// TestThemedCSSCarriesBothSyntaxes is the one that would have shipped broken.
//
// libadwaita reads different colours through different mechanisms: the window
// background comes from the named colour @window_bg_color, while its own rule for
// links is `color: var(--accent-color)`. Emitting one form and not the other
// re-themes about half the window and leaves links on libadwaita's blue, which is
// exactly what happened before both were emitted.
func TestThemedCSSCarriesBothSyntaxes(t *testing.T) {
	for _, th := range Themes {
		if len(th.colors) == 0 {
			continue
		}
		css := th.CSS()
		if !strings.Contains(css, ":root {") {
			t.Errorf("%s: no :root block, so nothing reading var(--accent-color) follows the theme", th.ID)
		}
		if !strings.Contains(css, "--accent-color:") {
			t.Errorf("%s: no --accent-color property; links would stay libadwaita's blue", th.ID)
		}
		if !strings.Contains(css, "@define-color accent_color ") {
			t.Errorf("%s: no @define-color accent_color; assets/style.css reads that form", th.ID)
		}
		if !strings.Contains(css, "@define-color window_bg_color ") {
			t.Errorf("%s: no @define-color window_bg_color", th.ID)
		}
		// Every colour appears in both forms, not just the two checked above.
		for name, value := range th.colors {
			variable := "--" + strings.ReplaceAll(name, "_", "-") + ": " + value + ";"
			named := "@define-color " + name + " " + value + ";"
			if !strings.Contains(css, variable) {
				t.Errorf("%s: %q missing from the :root block", th.ID, variable)
			}
			if !strings.Contains(css, named) {
				t.Errorf("%s: %q missing", th.ID, named)
			}
		}
	}
}

// TestResolveFallsBackToTheDesktop: an unset setting, and one naming a theme this
// build does not have — a settings file written by a newer version — both mean
// "follow the desktop" rather than a fixed theme.
func TestResolveFallsBackToTheDesktop(t *testing.T) {
	for _, id := range []string{Follow, "", "chartreuse", "Nord"} { // note: ids are case-sensitive
		if !IsFollowing(id) {
			t.Errorf("IsFollowing(%q) = false, want true", id)
		}
		if got := Resolve(id, true); got.ID != "dark" {
			t.Errorf("Resolve(%q, dark) = %q, want dark", id, got.ID)
		}
		if got := Resolve(id, false); got.ID != "light" {
			t.Errorf("Resolve(%q, light) = %q, want light", id, got.ID)
		}
	}

	// A known id is used whatever the desktop is set to: choosing one is the
	// point at which Atlas stops following.
	for _, th := range Themes {
		id := th.ID
		if IsFollowing(id) {
			t.Errorf("IsFollowing(%q) = true", id)
		}
		for _, systemDark := range []bool{true, false} {
			if got := Resolve(id, systemDark); got.ID != id {
				t.Errorf("Resolve(%q, %v) = %q", id, systemDark, got.ID)
			}
		}
	}
}

// TestSwatchCSSCoversEveryTheme: the circles are generated from the table so that
// adding a theme cannot leave its circle blank. That only holds if this stays true.
func TestSwatchCSSCoversEveryTheme(t *testing.T) {
	css := SwatchCSS()
	for _, th := range Themes {
		selector := ".atlas-swatch." + SwatchClass(th.ID)
		if !strings.Contains(css, selector) {
			t.Errorf("no rule for %s", selector)
		}
		if !strings.Contains(css, th.Primary) || !strings.Contains(css, th.Secondary) {
			t.Errorf("%s: its colours are not in the generated CSS", th.ID)
		}
	}
	// Both classes in the selector on purpose: one ties with Adwaita's own button
	// rules and the background-image is the whole widget.
	if strings.Contains(css, "\n.atlas-swatch-") {
		t.Error("a swatch rule uses one class, which does not outrank Adwaita's button styling")
	}
}

// TestThereIsAnAlternativeToBothDefaults: the three added themes are not much use
// if they are all dark, because then somebody who prefers a light window has
// nothing to move to.
func TestThereIsAnAlternativeToBothDefaults(t *testing.T) {
	// And evenly: four of each, so neither preference gets the leftovers.
	var extraLight, extraDark int
	for _, th := range Themes {
		if th.ID == "light" || th.ID == "dark" {
			continue
		}
		if th.Dark {
			extraDark++
		} else {
			extraLight++
		}
	}
	if extraLight == 0 {
		t.Error("every added theme is dark; there is no alternative to Light")
	}
	if extraDark == 0 {
		t.Error("every added theme is light; there is no alternative to Dark")
	}
	if extraLight != extraDark {
		t.Errorf("%d added light themes and %d dark; the split is meant to be even", extraLight, extraDark)
	}
}

// TestSwatchIsTheAccent: the circle's second half is a promise about what the
// buttons, switches and selection ring will be, so it has to be the colour they
// actually get. Sage's circle was a shade lighter than its buttons until this.
func TestSwatchIsTheAccent(t *testing.T) {
	for _, th := range Themes {
		if len(th.colors) == 0 {
			continue // libadwaita's own; its accent is not in the table
		}
		if got := th.colors["accent_bg_color"]; got != th.Secondary {
			t.Errorf("%s: the circle shows %s but the buttons are %s", th.ID, th.Secondary, got)
		}
		if got := th.colors["window_bg_color"]; got != th.Primary {
			t.Errorf("%s: the circle shows %s but the window is %s", th.ID, th.Primary, got)
		}
	}
}

// TestEachThemeHasItsOwnAccent: two themes that differ only in the shade of their
// grey are one theme twice. Light and Dark are exempt, being libadwaita's own and
// sharing its blue by definition.
func TestEachThemeHasItsOwnAccent(t *testing.T) {
	seen := map[string]string{}
	for _, th := range Themes {
		if th.ID == "light" || th.ID == "dark" {
			continue
		}
		if other, dup := seen[th.Secondary]; dup {
			t.Errorf("%s and %s share the accent %s", th.ID, other, th.Secondary)
		}
		seen[th.Secondary] = th.ID
	}
}

// TestPalettesAreReadable holds every palette to WCAG 2.1's contrast minimums.
//
// Text needs 4.5:1 and non-text marks — status icons, the selection ring — need
// 3:1. The case that matters most is the one a screenshot hides: the label on an
// accent-filled button. Nord and Ember both shipped at 3.5:1 and 3.9:1 there, and
// both looked fine, because a button is recognised long before it is read.
func TestPalettesAreReadable(t *testing.T) {
	type pair struct {
		what    string
		fg, bg  string
		minimum float64
	}
	for _, th := range Themes {
		c := th.colors
		if len(c) == 0 {
			continue
		}
		for _, p := range []pair{
			{"body text", "window_fg_color", "window_bg_color", 4.5},
			{"list text", "view_fg_color", "view_bg_color", 4.5},
			{"sidebar text", "sidebar_fg_color", "sidebar_bg_color", 4.5},
			{"card text", "card_fg_color", "card_bg_color", 4.5},
			{"dialog text", "dialog_fg_color", "dialog_bg_color", 4.5},
			{"popover text", "popover_fg_color", "popover_bg_color", 4.5},
			{"button label", "accent_fg_color", "accent_bg_color", 4.5},
			{"link text", "accent_color", "window_bg_color", 4.5},
			{"link text on a card", "accent_color", "card_bg_color", 4.5},
			{"selection ring", "accent_bg_color", "window_bg_color", 3},
			{"warning", "warning_color", "window_bg_color", 3},
			{"error", "error_color", "window_bg_color", 3},
			{"success", "success_color", "window_bg_color", 3},
		} {
			fg, bg := c[p.fg], c[p.bg]
			if fg == "" || bg == "" {
				t.Errorf("%s: %s or %s is missing", th.ID, p.fg, p.bg)
				continue
			}
			if r := contrast(t, fg, bg); r < p.minimum {
				t.Errorf("%s: %s is %.2f:1 (%s on %s), under the %.1f:1 minimum",
					th.ID, p.what, r, fg, bg, p.minimum)
			}
		}
	}
}

// contrast is WCAG 2.1's contrast ratio between two #rrggbb colours.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := relativeLuminance(t, a), relativeLuminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// relativeLuminance is WCAG's, with the sRGB transfer curve undone first — not
// the rough perceived brightness above, which is enough to tell dark from light
// but not to put a number on how far apart two colours are.
func relativeLuminance(t *testing.T, c string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		t.Fatalf("parsing %q: %v", c, err)
	}
	channel := func(v uint64) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(n>>16&0xff) + 0.7152*channel(n>>8&0xff) + 0.0722*channel(n&0xff)
}

// TestDarkThemesAreActuallyDark checks the flag against the palette.
//
// The flag is not decoration: it decides the colour scheme, and so whether
// libadwaita draws its widgets for a light background or a dark one. A theme
// with a dark background and Dark set false would draw dark widgets' outlines
// and icons meant for a light window onto a dark one.
func TestDarkThemesAreActuallyDark(t *testing.T) {
	for _, th := range Themes {
		lum := luminance(t, th.Primary)
		if th.Dark && lum > 0.5 {
			t.Errorf("%s is marked dark but its background %s is light (%.2f)", th.ID, th.Primary, lum)
		}
		if !th.Dark && lum < 0.5 {
			t.Errorf("%s is marked light but its background %s is dark (%.2f)", th.ID, th.Primary, lum)
		}
	}
}

// luminance is a rough perceived brightness, 0..1, from a #rrggbb string.
func luminance(t *testing.T, c string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		t.Fatalf("parsing %q: %v", c, err)
	}
	r, g, b := float64(n>>16&0xff), float64(n>>8&0xff), float64(n&0xff)
	return (0.299*r + 0.587*g + 0.114*b) / 255
}
