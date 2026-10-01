package storage

import (
	"strings"
	"testing"
)

// These are the prompts earlier versions shipped as their defaults, spelled
// out here independently of the constants in config.go. A config still holding
// one of them has to be upgraded to the current default on load.
//
// The point of the separate copy is to catch an edit to a legacy constant.
// 0.6.0 took the dashes out of the interface with a search-and-replace that
// also rewrote cleanPromptV032, and from then on a config holding the real
// 0.3.2 prompt matched nothing and never upgraded again. Nothing failed; the
// upgrade just stopped happening.
var shippedDefaults = []struct {
	version, field, text string
	want                 func() string
}{
	{"0.5.10", "system prompt", "You are the assistant inside Atlas Notes, a note-taking app. You work only with the user's current note; never invent facts, names, numbers, or sources that aren't in it. Be clear, concise, and faithful to the note's meaning. Output only the result itself \u2014 no preamble, no sign-off, no commentary about what you did.", func() string { return DefaultSystemPrompt }},
	{"0.5.10", "Summarize Note", "Summarize the key points of this note, shorter than the note itself \u2014 a single sentence is enough for a brief note. State only what the note actually says; do not add benefits, implications, or speculation.\n\n{content}", func() string { return defaultSummarizePrompt }},
	{"0.9.0", "system prompt", "You are the assistant inside Atlas Notes, a note-taking app. You work only with the user's current note; never invent facts, names, numbers, or sources that aren't in it. Be clear, concise, and faithful to the note's meaning. Output only the result itself, with no preamble, no sign-off, and no commentary about what you did.", func() string { return DefaultSystemPrompt }},
	{"0.9.0", "Summarize Note", "Summarize the key points of this note, shorter than the note itself; a single sentence is enough for a brief note. State only what the note actually says; do not add benefits, implications, or speculation.\n\n{content}", func() string { return defaultSummarizePrompt }},
	{"0.3.2", "Clean & Format", "Clean up and format this note using Markdown, keeping its meaning and facts intact. Fix grammar and spelling. Add a '# ' heading if the note has a clear title, use **bold** for key terms, and use '- ' bullet or '1. ' numbered lists only where the note is genuinely listing items or steps \u2014 keep ordinary sentences as paragraphs. Do NOT add task checkboxes; copy any existing '- [ ]' or '- [x]' lines through unchanged, and do not add new content. Output only the formatted note.\n\n{content}", func() string { return defaultCleanPrompt }},
}

func TestShippedDefaultPromptsUpgrade(t *testing.T) {
	for _, d := range shippedDefaults {
		cfg := Config{
			SystemPrompt: d.text,
			Actions:      []AIAction{{Name: d.field, Prompt: d.text}},
		}
		upgradePrompts(&cfg)
		if cfg.Actions[0].Prompt != d.want() {
			t.Errorf("the %s default from %s is no longer recognised, so installs "+
				"holding it will never be upgraded; was a legacy constant edited?",
				d.field, d.version)
		}
	}
}

// The current defaults are shown in Settings, where they are part of the
// interface, and the interface has no dashes in it.
func TestDefaultPromptsHaveNoDashes(t *testing.T) {
	for name, p := range map[string]string{
		"system":    DefaultSystemPrompt,
		"summarize": defaultSummarizePrompt,
		"clean":     defaultCleanPrompt,
		"sort":      DefaultSortPrompt,
	} {
		if strings.ContainsAny(p, "\u2014\u2013") {
			t.Errorf("the default %s prompt contains an em or en dash", name)
		}
	}
}

// A customised prompt is the user's, and must survive an upgrade untouched.
func TestCustomPromptIsLeftAlone(t *testing.T) {
	const mine = "Summarise this for me, briefly."
	cfg := Config{SystemPrompt: mine, Actions: []AIAction{{Prompt: mine}}}
	upgradePrompts(&cfg)
	if cfg.SystemPrompt != mine || cfg.Actions[0].Prompt != mine {
		t.Error("a prompt the user wrote was replaced by a default")
	}
}
