package app

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

func testPaletteInput() paletteInput {
	return paletteInput{
		Notes:    []string{"Projects/Atlas Roadmap", "Meeting Notes", "Ideas", "Work/Settings notes"},
		Recent:   []string{"Ideas", "Meeting Notes", "Projects/Atlas Roadmap"},
		Tags:     []paletteTag{{"planning", 2}, {"project/atlas", 1}},
		Actions:  paletteActions,
		Sections: paletteSections,
		ContentHits: func(q string) []string {
			if q == "quarterly" {
				return []string{"Meeting Notes", "Ideas"}
			}
			return nil
		},
	}
}

func titles(es []palEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Title)
	}
	return out
}

// Every action the app registers is in the box, or is left out on purpose.
func TestPaletteListsEveryAction(t *testing.T) {
	src, err := os.ReadFile("actions.go")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, a := range paletteActions {
		if a.Label == "" {
			t.Errorf("action %q has no label", a.Name)
		}
		have[a.Name] = true
	}
	registered := regexp.MustCompile(`\{"([a-z0-9-]+)", (?:nil|\[\]string)`).FindAllStringSubmatch(string(src), -1)
	if len(registered) < 20 {
		t.Fatalf("found only %d registered actions; has the binding table changed shape?", len(registered))
	}
	for _, m := range registered {
		if !have[m[1]] && !paletteSkip[m[1]] {
			t.Errorf("action %q is registered but not in the command box", m[1])
		}
	}
	names := map[string]bool{}
	for _, m := range registered {
		names[m[1]] = true
	}
	for _, a := range paletteActions {
		if !names[a.Name] {
			t.Errorf("the box lists %q, which is not registered", a.Name)
		}
	}
}

func TestBuildPaletteMixed(t *testing.T) {
	in := testPaletteInput()
	got := buildPalette("set", in)
	// A setting and a note with "Settings" in its name both match; the one that
	// starts with the query comes first, and the note wins the tie.
	if len(got) < 2 || got[0].Kind != palNote || got[0].Title != "Settings notes" {
		t.Errorf("set: %v", titles(got))
	}
	if !slices.ContainsFunc(got, func(e palEntry) bool {
		return e.Kind == palSetting && e.Target == sectionNotes || e.Kind == palAction && e.Target == "settings"
	}) {
		t.Errorf("set finds no setting or command: %v", titles(got))
	}

	got = buildPalette("road", in)
	if len(got) == 0 || got[0].Target != "Projects/Atlas Roadmap" {
		t.Errorf("road: %v", titles(got))
	}
	// A note found by its text follows those found by name, once.
	got = buildPalette("quarterly", in)
	if !slices.Equal(titles(got), []string{"Meeting Notes", "Ideas"}) {
		t.Errorf("quarterly: %v", titles(got))
	}
	if got := buildPalette("planning", in); len(got) == 0 || got[0].Kind != palTag {
		t.Errorf("planning: %v", titles(got))
	}
	if got := buildPalette("zzzz", in); len(got) != 0 {
		t.Errorf("zzzz: %v", titles(got))
	}
}

func TestBuildPaletteEmpty(t *testing.T) {
	got := buildPalette("", testPaletteInput())
	if len(got) != paletteMixedLimit || got[0].Title != "Ideas" || got[0].Kind != palNote {
		t.Errorf("empty box: %v", titles(got))
	}
	if got[paletteRecent].Kind != palAction {
		t.Errorf("commands should follow the recent notes: %v", titles(got))
	}
}

func TestBuildPalettePrefixes(t *testing.T) {
	in := testPaletteInput()
	for _, e := range buildPalette(">new", in) {
		if e.Kind != palAction {
			t.Errorf("> gave %v", e)
		}
	}
	if got := buildPalette(">new", in); len(got) == 0 || got[0].Target != "new-note" {
		t.Errorf(">new: %v", titles(got))
	}
	if got := buildPalette(">", in); len(got) != paletteListLimit {
		t.Errorf("> alone lists %d", len(got))
	}
	got := buildPalette("#plan", in)
	if !slices.Equal(titles(got), []string{"#planning"}) {
		t.Errorf("#plan: %v", titles(got))
	}
	got = buildPalette("? what is due today", in)
	if len(got) != 1 || got[0].Kind != palAsk || got[0].Target != "what is due today" {
		t.Errorf("?: %v", got)
	}
	if got := buildPalette("?", in); len(got) != 1 || got[0].Target != "" {
		t.Errorf("? alone should offer nothing to run: %v", got)
	}
}

func TestPaletteHidesNoteActionsWithoutANote(t *testing.T) {
	in := testPaletteInput()
	in.Actions = nil
	for _, a := range paletteActions {
		if !a.NeedsNote {
			in.Actions = append(in.Actions, a)
		}
	}
	for _, e := range buildPalette(">", in) {
		if e.Target == "bold" || e.Target == "history" {
			t.Errorf("%s offered with no note open", e.Target)
		}
	}
}
