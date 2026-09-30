package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppName is the XDG application directory name.
const AppName = "atlas-notes"

// DefaultModel is the Ollama model used when config omits one. Ollama serves
// the plain tag as the Q4_K_M build, which is the quantization Atlas Notes is
// tuned for: good instruction-following at ~5.5 GB.
const DefaultModel = "qwen3.5:9b"

// oldDefaultModel was the default before DefaultModel. A config still holding
// it means the user never chose a model, so it is upgraded on load (a model the
// user picked themselves is never touched).
const oldDefaultModel = "qwen2.5:3b"

// Update channels: Release follows the main branch, Beta follows the beta branch.
const (
	ChannelRelease = "release"
	ChannelBeta    = "beta"
)

// Font-rendering modes. "crisp" hints glyphs onto the pixel grid, which is what
// a 1x (e.g. 1080p) display needs; "smooth" leaves GTK's unhinted defaults,
// which suit a HiDPI screen; "auto" picks per display.
const (
	FontRenderingAuto   = "auto"
	FontRenderingCrisp  = "crisp"
	FontRenderingSmooth = "smooth"
)

// DefaultAssistantName is the AI assistant's display name when config omits one.
const DefaultAssistantName = "Atlas"

// Action modes control what happens to an AI action's result.
const (
	ActionModeShow    = "show"    // display the result in the AI panel
	ActionModeReplace = "replace" // overwrite the note with the result
	ActionModeSort    = "sort"    // reorder/re-prioritise the note's checklist
)

// Default AI prompts ({content} = note text, {items} = checklist text), tuned for
// small local models: concise, faithful to the note, and free of preamble.
const (
	DefaultSystemPrompt = "You are the assistant inside Atlas Notes, a note-taking app. You work only with the user's current note; never invent facts, names, numbers, or sources that aren't in it. Be clear, concise, and faithful to the note's meaning. Output only the result itself, with no preamble, no sign-off, and no commentary about what you did."

	defaultSummarizePrompt = "Summarize the key points of this note, shorter than the note itself; a single sentence is enough for a brief note. State only what the note actually says; do not add benefits, implications, or speculation.\n\n{content}"

	defaultCleanPrompt = "Reformat this note as clean, well-structured Markdown. Fix grammar and spelling, keep the '# ' title, use **bold** for the lead-in label and the key terms of each point, and use '- ' bullet lists for any series of features, items, or steps. Keep narrative paragraphs as paragraphs, preserve every fact (do not invent or drop content), and copy any existing '- [ ]' / '- [x]' task lines through unchanged (never add new checkboxes). Output only the formatted note.\n\n{content}"

	DefaultSortPrompt = "Re-prioritize this checklist. For every item, assign a priority of \"high\", \"medium\", or \"low\" based on urgency and impact, and an \"order\" ranking the items from most urgent (1) to least. Return ONLY a JSON array, one object per item, copying each item's text exactly:\n[{\"text\":\"...\",\"priority\":\"high\",\"order\":1}, ...]\n\n{items}"
)

// Previous built-in default prompts. LoadConfig upgrades these to the current
// defaults so prompt improvements reach existing installs, while leaving any
// prompt the user has customized untouched.
//
// These are historical records, not text anyone sees: each must stay
// byte-for-byte what that version shipped, or configs still holding it stop
// matching and never upgrade again. The em dashes in them are spelled \u2014
// on purpose, so a search-and-replace over the interface's punctuation cannot
// reach them. One already did, in 0.6.0.
const (
	oldSystemPrompt    = "You are a concise assistant. You only have access to the note provided. Do not reference external information. Be brief and precise."
	oldSummarizePrompt = "Summarize the following note in 3-5 sentences:\n\n{content}"
	oldSortPrompt      = "You are given a checklist. Re-evaluate and reorder items by urgency. Assign priority (high/medium/low) to each. Return a JSON array: [{\"text\":\"...\",\"priority\":\"high\",\"order\":1}, ...]. Return only valid JSON, no explanation:\n\n{items}"
	cleanPromptV2      = "Fix grammar, improve clarity, and clean the markdown formatting of this note. Return only the corrected note content, no explanation:\n\n{content}"
	cleanPromptV031    = "Rewrite this note with correct grammar and spelling and tidy Markdown formatting. Fix only mistakes: preserve the meaning and the facts, keep the author's distinct points separate, and do not add information. Keep prose as prose and lists as lists, and keep existing headings. Output only the corrected note.\n\n{content}"
	cleanPromptV032    = "Clean up and format this note using Markdown, keeping its meaning and facts intact. Fix grammar and spelling. Add a '# ' heading if the note has a clear title, use **bold** for key terms, and use '- ' bullet or '1. ' numbered lists only where the note is genuinely listing items or steps \u2014 keep ordinary sentences as paragraphs. Do NOT add task checkboxes; copy any existing '- [ ]' or '- [x]' lines through unchanged, and do not add new content. Output only the formatted note.\n\n{content}"

	// The defaults through 0.5.10. 0.6.0 took the dashes out of them.
	systemPromptV0510    = "You are the assistant inside Atlas Notes, a note-taking app. You work only with the user's current note; never invent facts, names, numbers, or sources that aren't in it. Be clear, concise, and faithful to the note's meaning. Output only the result itself \u2014 no preamble, no sign-off, no commentary about what you did."
	summarizePromptV0510 = "Summarize the key points of this note, shorter than the note itself \u2014 a single sentence is enough for a brief note. State only what the note actually says; do not add benefits, implications, or speculation.\n\n{content}"
)

// AIAction is a user-configurable AI button shown in the sidebar.
type AIAction struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Mode   string `json:"mode"`
}

func defaultActions() []AIAction {
	return []AIAction{
		{Name: "Summarize Note", Mode: ActionModeShow, Prompt: defaultSummarizePrompt},
		{Name: "Clean & Format", Mode: ActionModeReplace, Prompt: defaultCleanPrompt},
		{Name: "Sort Priorities", Mode: ActionModeSort, Prompt: DefaultSortPrompt},
	}
}

// ensureSortAction guarantees a sort-mode action exists, migrating configs made
// before Sort became a regular editable action.
func ensureSortAction(actions []AIAction) []AIAction {
	for _, a := range actions {
		if a.Mode == ActionModeSort {
			return actions
		}
	}
	return append(actions, AIAction{Name: "Sort Priorities", Mode: ActionModeSort, Prompt: DefaultSortPrompt})
}

// Config holds user settings persisted to ~/.config/atlas-notes/config.json.
type Config struct {
	VaultPath    string `json:"vault_path"`
	LastNote     string `json:"last_note"`
	WindowWidth  int    `json:"window_width"`
	WindowHeight int    `json:"window_height"`
	// Panel widths are remembered so the layout survives a restart.
	LeftPanelWidth  int    `json:"left_panel_width"`
	RightPanelWidth int    `json:"right_panel_width"`
	Model           string `json:"model"`
	FontRendering   string `json:"font_rendering"` // auto | crisp | smooth
	AssistantName   string `json:"assistant_name"`
	UpdateChannel   string `json:"update_channel"`
	// CheckUpdates asks GitHub on launch whether a newer version has been
	// published. A config written before this setting existed keeps the
	// default, because LoadConfig starts from DefaultConfig and unmarshals
	// over it.
	CheckUpdates bool `json:"check_updates"`

	// ShowFormatBar keeps the formatting toolbar above the note. It is on by
	// default: someone who does not know the Markdown has no other way to
	// discover what the editor understands. Someone who does can turn it off.
	ShowFormatBar bool `json:"show_format_bar"`

	// Theme is one of internal/theme's IDs, or "" to follow the desktop.
	Theme string `json:"theme"`

	// WindowTransparency lets the desktop show through the window's frame and
	// page: off, subtle, medium or strong. Only where the display composites;
	// see TransparencyLevels.
	WindowTransparency string `json:"window_transparency"`

	// DueReminders sends a desktop notification about checklist items that
	// are due today or overdue. On by default: a due date nobody is told about
	// is only a label.
	DueReminders bool `json:"due_reminders"`

	// Favourites are a preference rather than vault content: they live here
	// rather than in the vault, so they follow the person rather than a copy
	// of the notes. A vault synced to another machine does not carry them.
	FavouriteNotes   []string `json:"favourite_notes"`
	FavouriteFolders []string `json:"favourite_folders"`

	SystemPrompt        string     `json:"system_prompt"`
	Actions             []AIAction `json:"actions"`
	EnableTreeSummaries bool       `json:"enable_tree_summaries"`
}

// dataDir and configDir are per-platform; see paths_*.go. Both honour an
// explicit override first, which is how a host application that owns its own
// storage (Android) says where the vault lives, and how the test and
// measurement harnesses keep out of a real vault.
func dataDir() string {
	if x := os.Getenv("ATLAS_DATA_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	return platformDataDir()
}

// DataDir is the per-user data directory ($XDG_DATA_HOME/atlas-notes or
// ~/.local/share/atlas-notes). The vault, SQLite index, and the updater's source
// checkout all live under it.
func DataDir() string { return dataDir() }

func configDir() string {
	if x := os.Getenv("ATLAS_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	return platformConfigDir()
}

// ConfigPath is the absolute path of config.json.
func ConfigPath() string { return filepath.Join(configDir(), "config.json") }

// DefaultVaultPath is ~/.local/share/atlas-notes/vault.
func DefaultVaultPath() string { return filepath.Join(dataDir(), "vault") }

// isDefaultVault reports whether path is the default vault, however it is
// spelled. An empty path means the default too.
func isDefaultVault(path string) bool {
	return path == "" || filepath.Clean(path) == filepath.Clean(DefaultVaultPath())
}

// DefaultDBPath is ~/.local/share/atlas-notes/index.db.
func DefaultDBPath() string { return filepath.Join(dataDir(), "index.db") }

// The window transparency levels, in the order Settings lists them.
const (
	TransparencyOff    = "off"
	TransparencySubtle = "subtle"
	TransparencyMedium = "medium"
	TransparencyStrong = "strong"
)

// TransparencyLevels are the stored values, in order.
var TransparencyLevels = []string{TransparencyOff, TransparencySubtle, TransparencyMedium, TransparencyStrong}

// NormalizeTransparency maps a stored level to a known one; anything else is
// off, so a config from a newer version with a level this one lacks opens
// opaque rather than half-applied.
func NormalizeTransparency(level string) string {
	for _, l := range TransparencyLevels {
		if level == l {
			return l
		}
	}
	return TransparencyOff
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		VaultPath:     DefaultVaultPath(),
		WindowWidth:   1100,
		WindowHeight:  720,
		Model:         DefaultModel,
		FontRendering: FontRenderingAuto,
		AssistantName: DefaultAssistantName,
		UpdateChannel: ChannelRelease,
		CheckUpdates:  true,
		ShowFormatBar: true,
		DueReminders:  true,
		SystemPrompt:  DefaultSystemPrompt,
		Actions:       defaultActions(),
	}
}

// LoadConfig reads config.json, writing and returning defaults when it is
// missing. Zero-valued fields are backfilled with defaults.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, SaveConfig(cfg)
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	if cfg.VaultPath == "" {
		cfg.VaultPath = DefaultVaultPath()
	}
	if cfg.Model == "" || cfg.Model == oldDefaultModel {
		cfg.Model = DefaultModel
	}
	if cfg.UpdateChannel == "" {
		cfg.UpdateChannel = ChannelRelease
	}
	switch cfg.FontRendering {
	case FontRenderingCrisp, FontRenderingSmooth:
	default:
		cfg.FontRendering = FontRenderingAuto
	}
	if cfg.AssistantName == "" {
		cfg.AssistantName = DefaultAssistantName
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if len(cfg.Actions) == 0 {
		cfg.Actions = defaultActions()
	} else {
		cfg.Actions = ensureSortAction(cfg.Actions)
	}
	if cfg.WindowWidth == 0 {
		cfg.WindowWidth = 1100
	}
	if cfg.WindowHeight == 0 {
		cfg.WindowHeight = 720
	}
	upgradePrompts(&cfg)
	return cfg, nil
}

// supersededPrompts maps each retired built-in default prompt to its current
// replacement.
func supersededPrompts() map[string]string {
	return map[string]string{
		oldSystemPrompt:      DefaultSystemPrompt,
		oldSummarizePrompt:   defaultSummarizePrompt,
		oldSortPrompt:        DefaultSortPrompt,
		cleanPromptV2:        defaultCleanPrompt,
		cleanPromptV031:      defaultCleanPrompt,
		cleanPromptV032:      defaultCleanPrompt,
		systemPromptV0510:    DefaultSystemPrompt,
		summarizePromptV0510: defaultSummarizePrompt,
	}
}

// upgradePrompts replaces any prompt still equal to a previous built-in default
// with the current default, so prompt improvements reach existing installs
// without overwriting prompts the user has customized.
func upgradePrompts(cfg *Config) {
	repl := supersededPrompts()
	if v, ok := repl[cfg.SystemPrompt]; ok {
		cfg.SystemPrompt = v
	}
	for i := range cfg.Actions {
		if v, ok := repl[cfg.Actions[i].Prompt]; ok {
			cfg.Actions[i].Prompt = v
		}
	}
}

// SaveConfig atomically writes cfg to config.json.
func SaveConfig(cfg Config) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	// The default vault is written as "", which LoadConfig reads back as the
	// default. Writing it out in full pinned the vault to wherever the data
	// directory happened to be the first time the app ran: restore a backup
	// under another user, or copy a data directory somewhere else, and the
	// config went on pointing at the old location. A vault the user chose is
	// written as it is, because that path means something.
	if isDefaultVault(cfg.VaultPath) {
		cfg.VaultPath = ""
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ConfigPath(), data)
}
