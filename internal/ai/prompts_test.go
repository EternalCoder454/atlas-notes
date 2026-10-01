package ai

import (
	"strings"
	"testing"
)

// A request to write something must not be framed as a question the note
// either answers or does not: "Suggest a better title" was answered with "the
// note contains no title suggestions".
func TestAskPromptLetsTheModelWrite(t *testing.T) {
	p := askPrompt("Trip ideas", "- Lisbon in May\n- Porto after", "Suggest 3 better titles")
	if strings.Contains(p, "If the note does not contain the answer") {
		t.Error("the prompt still tells the model to refuse what the note does not contain")
	}
	if !strings.Contains(p, "Never reply that the note does not contain it") {
		t.Error("the prompt does not say that writing from the note is wanted")
	}
	if !strings.Contains(p, "Title: Trip ideas\n") {
		t.Error("the note's title is not in the prompt")
	}
	if !strings.HasSuffix(p, "Request: Suggest 3 better titles") {
		t.Errorf("the request is not last, where a small model keeps it: %q", p[len(p)-60:])
	}
	if strings.Index(p, "Lisbon") > strings.Index(p, "Request:") {
		t.Error("the note comes after the request")
	}
}

func TestAskPromptEmptyNote(t *testing.T) {
	p := askPrompt("", "  \n", "What is this about?")
	if !strings.Contains(p, "(the note is empty)") {
		t.Error("an empty note is not said to be empty")
	}
	if strings.Contains(p, "Title:") {
		t.Error("a note with no name still gets a Title line")
	}
}

func TestFillTemplate(t *testing.T) {
	if got := fillTemplate("Shorten:\n\n{content}", "abc"); got != "Shorten:\n\nabc" {
		t.Errorf("with {content}: %q", got)
	}
	// A shortcut written without the placeholder still gets the note.
	if got := fillTemplate("Translate this into French\n", "abc"); got != "Translate this into French\n\nNote:\nabc" {
		t.Errorf("without {content}: %q", got)
	}
}

func TestEditPromptPutsTheInstructionAfterTheNote(t *testing.T) {
	p := editPrompt("# Groceries\n- milk\n", "add eggs")
	if strings.Index(p, "- milk") > strings.Index(p, "Instruction: add eggs") {
		t.Error("the instruction comes before the note")
	}
	if !strings.Contains(p, "ENTIRE updated note") {
		t.Error("the prompt no longer asks for the whole note back")
	}
}

func TestUnquoteNote(t *testing.T) {
	for in, want := range map[string]string{
		"\"\"\"\n# A\n- b\n\"\"\"": "# A\n- b",
		"# A\n- b":                 "# A\n- b",
		"\"\"\"":                   "\"\"\"", // too short to be a quoted note
	} {
		if got := unquoteNote(in); got != want {
			t.Errorf("unquoteNote(%q) = %q, want %q", in, got, want)
		}
	}
}
