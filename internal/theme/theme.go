// Package theme is Atlas Notes' colour themes, the same ten Atlas Monitor has,
// so the two apps can be set to match.
//
// The default follows the desktop, which is what Atlas Notes always did. The
// choice adds ten themes: two are the ordinary light and dark that libadwaita
// already provides, and eight have palettes of their own, four light and four
// dark.
//
// Every palette is held to WCAG's contrast minimums by TestPalettesAreReadable:
// 4.5:1 for text, including the label on an accent-coloured button, and 3:1 for
// the status colours. When the test was first written for Atlas Monitor, two of
// the first three themes failed it, which a screenshot does not show: a button
// reads fine at a glance until you have to read the label on one.
//
// A theme is two things. It forces a light or dark colour scheme, so the
// widgets are drawn for the right side. And it overrides libadwaita's named
// colours, which is where the palette comes from: assets/style.css names no
// colours of its own beyond a few semantic ones (the save dot, the priority
// bars), so redefining the names re-themes the whole window.
//
// The overrides are written in both of libadwaita's syntaxes, because it uses
// both: some of its rules read @window_bg_color, others var(--accent-color).
// Overriding only one form re-themes about half the window.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Theme is one palette.
type Theme struct {
	// ID is what goes in the settings file. It is never translated and never
	// changes, because a renamed one would silently reset everybody's choice.
	ID string
	// Name is what the picker shows.
	Name string
	// Summary is one short line about what it is for.
	Summary string

	// Dark says which colour scheme to force. It is not cosmetic: libadwaita
	// draws its widgets, icons and focus rings for the scheme, so a theme with a
	// dark palette and a light scheme would draw them for the wrong background.
	Dark bool

	// Primary and Secondary are the two halves of the circle in the picker: the
	// window background and the accent. Together they are a fair preview of what
	// choosing it does, which a single colour would not be.
	Primary   string
	Secondary string

	// colors are libadwaita's named colours to override, keyed by the name in its
	// underscore form — "window_bg_color". Empty for the two themes that are
	// libadwaita's own palette, where the scheme is the whole of the change.
	colors map[string]string
}

// Themes is every theme, in the order the picker shows them.
//
// Light and dark come first because they are what most people want and what the
// desktop itself offers. The rest are split evenly, four light and four dark, so
// that whichever of the ordinary two somebody prefers there are alternatives to it
// rather than to the other one. Each of the eight has an accent of its own —
// only Light and Dark share one, libadwaita's blue — because two themes that
// differ only in how dark the grey is are one theme.
//
// Secondary is always the theme's accent_bg_color, which TestSwatchIsTheAccent
// holds it to: the circle is a promise about what the buttons and switches will be,
// and a preview that is a shade off is a small lie.
var Themes = []Theme{
	{
		ID:      "light",
		Name:    "Light",
		Summary: "libadwaita's own light palette",
		Dark:    false,
		// Adwaita's light window background, and its blue.
		Primary:   "#ffffff",
		Secondary: "#3584e4",
	},
	{
		ID:      "dark",
		Name:    "Dark",
		Summary: "libadwaita's own dark palette",
		Dark:    true,
		// Adwaita's dark window background, and the same blue.
		Primary:   "#242424",
		Secondary: "#3584e4",
	},
	{
		ID:      "nord",
		Name:    "Nord",
		Summary: "Cool blue-grey, low contrast",
		Dark:    true,
		// The Nord palette: polar night for the backgrounds, snow storm for text,
		// frost for the accent. Its whole point is that nothing in it is fully
		// saturated, which is easy to read for a long time and unusually kind to
		// a screen full of charts.
		Primary:   "#2e3440",
		Secondary: "#88c0d0",
		colors: map[string]string{
			"window_bg_color":    "#2e3440",
			"window_fg_color":    "#eceff4",
			"view_bg_color":      "#272c36",
			"view_fg_color":      "#eceff4",
			"sidebar_bg_color":   "#292e39",
			"sidebar_fg_color":   "#eceff4",
			"headerbar_bg_color": "#2e3440",
			"headerbar_fg_color": "#eceff4",
			"card_bg_color":      "#3b4252",
			"card_fg_color":      "#eceff4",
			"dialog_bg_color":    "#2e3440",
			"dialog_fg_color":    "#eceff4",
			"popover_bg_color":   "#3b4252",
			"popover_fg_color":   "#eceff4",
			// Frost buttons with polar-night labels. Nord's darker blue, #5e81ac,
			// with light text is what most Nord themes use, and it is 3.5:1 —
			// under what a button label needs to be read rather than recognised.
			// The lighter frost with dark text is 6.2:1 and just as much Nord.
			"accent_bg_color": "#88c0d0",
			"accent_fg_color": "#2e3440",
			"accent_color":    "#88c0d0",
			"warning_color":   "#ebcb8b",
			"error_color":     "#bf616a",
			"success_color":   "#a3be8c",
		},
	},
	{
		ID:      "ember",
		Name:    "Ember",
		Summary: "Warm dark, no blue light",
		Dark:    true,
		// Warm greys rather than neutral ones, and an amber accent, so there is
		// almost no blue in it. That is the point: this is the one to leave Atlas
		// open in at night.
		Primary:   "#1c1917",
		Secondary: "#e8913a",
		colors: map[string]string{
			"window_bg_color":    "#1c1917",
			"window_fg_color":    "#ede4dc",
			"view_bg_color":      "#231f1d",
			"view_fg_color":      "#ede4dc",
			"sidebar_bg_color":   "#211d1b",
			"sidebar_fg_color":   "#ede4dc",
			"headerbar_bg_color": "#1c1917",
			"headerbar_fg_color": "#ede4dc",
			"card_bg_color":      "#2a2523",
			"card_fg_color":      "#ede4dc",
			"dialog_bg_color":    "#231f1d",
			"dialog_fg_color":    "#ede4dc",
			"popover_bg_color":   "#2a2523",
			"popover_fg_color":   "#ede4dc",
			// Amber with a dark label rather than a burnt orange with a white
			// one. The white-on-#b86f28 buttons this shipped with were 3.9:1;
			// this is 7.1:1, and the button is now the colour the circle shows.
			"accent_bg_color": "#e8913a",
			"accent_fg_color": "#1c1917",
			"accent_color":    "#e8913a",
			"warning_color":   "#e0a458",
			"error_color":     "#d4675a",
			"success_color":   "#a3a55c",
		},
	},
	{
		ID:      "sage",
		Name:    "Sage",
		Summary: "Light, warm paper and green",
		Dark:    false,
		// The light alternative. Paper rather than white, which takes the glare
		// off a bright room without going grey, and a muted green accent that
		// does not fight the chart colours.
		Primary:   "#f4f3ec",
		Secondary: "#4e7850",
		colors: map[string]string{
			"window_bg_color":    "#f4f3ec",
			"window_fg_color":    "#2d3a2e",
			"view_bg_color":      "#fbfaf5",
			"view_fg_color":      "#2d3a2e",
			"sidebar_bg_color":   "#eae9df",
			"sidebar_fg_color":   "#2d3a2e",
			"headerbar_bg_color": "#eae9df",
			"headerbar_fg_color": "#2d3a2e",
			"card_bg_color":      "#ffffff",
			"card_fg_color":      "#2d3a2e",
			"dialog_bg_color":    "#fbfaf5",
			"dialog_fg_color":    "#2d3a2e",
			"popover_bg_color":   "#ffffff",
			"popover_fg_color":   "#2d3a2e",
			"accent_bg_color":    "#4e7850",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#3f6641",
			"warning_color":      "#a1661b",
			"error_color":        "#b3392f",
			"success_color":      "#3f6641",
		},
	},
	{
		ID:      "dracula",
		Name:    "Dracula",
		Summary: "Deep grey, purple accent",
		Dark:    true,
		// The Dracula palette as its authors publish it. Its purple is light
		// enough that a white label on it would be 2.3:1, so the buttons take the
		// dark background colour for their text instead — which is also how the
		// palette's own reference themes draw them.
		Primary:   "#282a36",
		Secondary: "#bd93f9",
		colors: map[string]string{
			"window_bg_color":    "#282a36",
			"window_fg_color":    "#f8f8f2",
			"view_bg_color":      "#21222c",
			"view_fg_color":      "#f8f8f2",
			"sidebar_bg_color":   "#21222c",
			"sidebar_fg_color":   "#f8f8f2",
			"headerbar_bg_color": "#282a36",
			"headerbar_fg_color": "#f8f8f2",
			"card_bg_color":      "#343746",
			"card_fg_color":      "#f8f8f2",
			"dialog_bg_color":    "#282a36",
			"dialog_fg_color":    "#f8f8f2",
			"popover_bg_color":   "#343746",
			"popover_fg_color":   "#f8f8f2",
			"accent_bg_color":    "#bd93f9",
			"accent_fg_color":    "#21222c",
			"accent_color":       "#bd93f9",
			"warning_color":      "#ffb86c",
			"error_color":        "#ff5555",
			"success_color":      "#50fa7b",
		},
	},
	{
		ID:      "rose",
		Name:    "Rose",
		Summary: "Light, warm with a rose accent",
		Dark:    false,
		// After Rosé Pine's Dawn variant: warm off-white, muted violet-grey text.
		// Its "love" rose is #b4637a, which carries a white label at 4.2:1; this
		// is a step deeper at 5.1:1, close enough that nobody would call it a
		// different colour.
		Primary:   "#faf4ed",
		Secondary: "#a5566d",
		colors: map[string]string{
			"window_bg_color":    "#faf4ed",
			"window_fg_color":    "#575279",
			"view_bg_color":      "#fffaf3",
			"view_fg_color":      "#575279",
			"sidebar_bg_color":   "#f2e9e1",
			"sidebar_fg_color":   "#575279",
			"headerbar_bg_color": "#f2e9e1",
			"headerbar_fg_color": "#575279",
			"card_bg_color":      "#fffaf3",
			"card_fg_color":      "#575279",
			"dialog_bg_color":    "#fffaf3",
			"dialog_fg_color":    "#575279",
			"popover_bg_color":   "#fffaf3",
			"popover_fg_color":   "#575279",
			"accent_bg_color":    "#a5566d",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#9c4f66",
			"warning_color":      "#9a6412",
			// A true red rather than the palette's rose, so that an error still
			// looks like an error next to an accent that is already pinkish-red.
			"error_color":   "#b42318",
			"success_color": "#2d7a4f",
		},
	},
	{
		ID:      "solarized",
		Name:    "Solarized",
		Summary: "The classic light palette, teal accent",
		Dark:    false,
		// Solarized Light's base3 background. Its accents are designed for text,
		// not for filling buttons, and its cyan (#2aa198) under a white label is
		// 3.0:1, so the accent is that cyan taken darker until the label reads.
		// Body text is base02 rather than the palette's usual base01, which is
		// deliberately soft and too soft for small numbers in a dense table.
		Primary:   "#fdf6e3",
		Secondary: "#1f7f78",
		colors: map[string]string{
			"window_bg_color":    "#fdf6e3",
			"window_fg_color":    "#073642",
			"view_bg_color":      "#fffdf6",
			"view_fg_color":      "#073642",
			"sidebar_bg_color":   "#eee8d5",
			"sidebar_fg_color":   "#073642",
			"headerbar_bg_color": "#eee8d5",
			"headerbar_fg_color": "#073642",
			"card_bg_color":      "#fffdf6",
			"card_fg_color":      "#073642",
			"dialog_bg_color":    "#fdf6e3",
			"dialog_fg_color":    "#073642",
			"popover_bg_color":   "#fffdf6",
			"popover_fg_color":   "#073642",
			"accent_bg_color":    "#1f7f78",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#1a6f69",
			"warning_color":      "#8a6800",
			"error_color":        "#c42b28",
			"success_color":      "#5f6e00",
		},
	},
	{
		ID:      "ink",
		Name:    "Ink",
		Summary: "Near-monochrome, graphite accent",
		Dark:    false,
		// No colour at all except where something is wrong. Everything a theme
		// would normally tint — the switches, the selection ring, the links — is
		// graphite, which leaves the chart colours and the warning colours as the
		// only colour on the screen, and so the only things that draw the eye.
		Primary:   "#f6f6f4",
		Secondary: "#2b2b2b",
		colors: map[string]string{
			"window_bg_color":    "#f6f6f4",
			"window_fg_color":    "#1a1a1a",
			"view_bg_color":      "#ffffff",
			"view_fg_color":      "#1a1a1a",
			"sidebar_bg_color":   "#ececea",
			"sidebar_fg_color":   "#1a1a1a",
			"headerbar_bg_color": "#ececea",
			"headerbar_fg_color": "#1a1a1a",
			"card_bg_color":      "#ffffff",
			"card_fg_color":      "#1a1a1a",
			"dialog_bg_color":    "#ffffff",
			"dialog_fg_color":    "#1a1a1a",
			"popover_bg_color":   "#ffffff",
			"popover_fg_color":   "#1a1a1a",
			"accent_bg_color":    "#2b2b2b",
			"accent_fg_color":    "#ffffff",
			"accent_color":       "#2b2b2b",
			"warning_color":      "#9a6700",
			"error_color":        "#c0322b",
			"success_color":      "#2d7a3e",
		},
	},
	{
		ID:      "contrast",
		Name:    "Contrast",
		Summary: "Black and white, yellow accent",
		Dark:    true,
		// As legible as colour alone can make it: pure black, pure white, and a
		// yellow accent that reads at 14:1 against the background. It is last
		// because it is the one with a job to do rather than a mood.
		//
		// It is not libadwaita's high-contrast mode, which also thickens borders
		// and outlines. That follows the desktop's accessibility setting and
		// still applies on top of this if it is on.
		Primary:   "#000000",
		Secondary: "#ffd60a",
		colors: map[string]string{
			"window_bg_color":    "#000000",
			"window_fg_color":    "#ffffff",
			"view_bg_color":      "#0a0a0a",
			"view_fg_color":      "#ffffff",
			"sidebar_bg_color":   "#0f0f0f",
			"sidebar_fg_color":   "#ffffff",
			"headerbar_bg_color": "#000000",
			"headerbar_fg_color": "#ffffff",
			"card_bg_color":      "#161616",
			"card_fg_color":      "#ffffff",
			"dialog_bg_color":    "#0a0a0a",
			"dialog_fg_color":    "#ffffff",
			"popover_bg_color":   "#161616",
			"popover_fg_color":   "#ffffff",
			"accent_bg_color":    "#ffd60a",
			"accent_fg_color":    "#000000",
			"accent_color":       "#ffd60a",
			"warning_color":      "#ff9f1a",
			"error_color":        "#ff6b6b",
			"success_color":      "#3ddc84",
		},
	},
}

// Follow is the setting's value when no theme has been chosen: the app tracks the
// desktop, which is what it did before any of this existed. Choosing a theme
// replaces it. There is deliberately no circle for it in the picker — "whatever
// the desktop says" is not a palette, and would be a different kind of thing from
// the ten beside it — so it is a plain button underneath instead.
const Follow = ""

// ByID returns a theme by its settings value.
func ByID(id string) (Theme, bool) {
	for _, t := range Themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// Resolve turns a setting into the theme to actually draw.
//
// An unknown ID resolves the same way an unset one does rather than falling back
// to a fixed theme: a settings file from a newer version naming a theme this build
// does not have should leave the app looking like the desktop, not force it light.
func Resolve(id string, systemDark bool) Theme {
	if t, ok := ByID(id); ok {
		return t
	}
	if systemDark {
		dark, _ := ByID("dark")
		return dark
	}
	light, _ := ByID("light")
	return light
}

// IsFollowing reports whether a setting means "track the desktop".
func IsFollowing(id string) bool {
	_, ok := ByID(id)
	return !ok
}

// CSS is the colour overrides for this theme. The two themes that are
// libadwaita's own palette override nothing: the colour scheme is the whole of
// what choosing them does.
func (t Theme) CSS() string {
	if len(t.colors) == 0 {
		return ""
	}
	return colorCSS(t.colors)
}

// colorCSS writes one set of overrides.
//
// Sorted, so the output is stable and a change to it is readable in a diff.
func colorCSS(colors map[string]string) string {
	names := make([]string, 0, len(colors))
	for name := range colors {
		names = append(names, name)
	}
	sort.Strings(names) // stable output, so the CSS is diffable

	var b strings.Builder

	// The custom properties. libadwaita's own rules read several colours only
	// through these — links are `var(--accent-color)` — so a theme that set the
	// named colours alone would leave them on libadwaita's blue.
	b.WriteString(":root {\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  --%s: %s;\n", strings.ReplaceAll(name, "_", "-"), colors[name])
	}
	b.WriteString("}\n")

	// And the named colours, which are what assets/style.css is written in and
	// what most of libadwaita's own rules still use.
	for _, name := range names {
		fmt.Fprintf(&b, "@define-color %s %s;\n", name, colors[name])
	}
	return b.String()
}

// SwatchCSS is the styling for every theme's circle in the picker.
//
// It is generated from the table rather than written in assets/style.css so that
// adding a theme cannot leave its circle the wrong colour, or absent. Each is a
// disc split on the diagonal: background on one side, accent on the other.
func SwatchCSS() string {
	var b strings.Builder
	b.WriteString("/* Generated by internal/theme. See SwatchCSS. */\n")
	for _, t := range Themes {
		// Both classes in the selector on purpose. A single class ties with
		// Adwaita's own `.toggle` rules and loses to anything more specific, and
		// the background-image is the whole point of the widget.
		fmt.Fprintf(&b, ".atlas-swatch.%s {\n", SwatchClass(t.ID))
		// A hard stop at the midpoint rather than a blend: the two colours are
		// two facts about the theme, and a gradient between them would invent a
		// third that is not in it.
		fmt.Fprintf(&b, "  background-image: linear-gradient(135deg, %s 0%%, %s 50%%, %s 50%%, %s 100%%);\n",
			t.Primary, t.Primary, t.Secondary, t.Secondary)
		b.WriteString("}\n")
	}
	return b.String()
}

// SwatchClass is the CSS class carrying one theme's colours.
func SwatchClass(id string) string { return "atlas-swatch-" + id }
