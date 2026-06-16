package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppName is the XDG application directory name.
const AppName = "atlas-notes"

// DefaultModel is the Ollama model used when config omits one.
const DefaultModel = "qwen2.5:3b"

// Update channels: Release follows the main branch, Beta follows the beta branch.
const (
	ChannelRelease = "release"
	ChannelBeta    = "beta"
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
	DefaultSystemPrompt = "You are the assistant inside Atlas Notes, a note-taking app. You work only with the user's current note; never invent facts, names, numbers, or sources that aren't in it. Be clear, concise, and faithful to the note's meaning. Output only the result itself — no preamble, no sign-off, no commentary about what you did."

	defaultSummarizePrompt = "Summarize the key points of this note, shorter than the note itself — a single sentence is enough for a brief note. State only what the note actually says; do not add benefits, implications, or speculation.\n\n{content}"

	defaultCleanPrompt = "Rewrite this note with correct grammar and spelling and tidy Markdown formatting. Fix only mistakes: preserve the meaning and the facts, keep the author's distinct points separate, and do not add information. Keep prose as prose and lists as lists, and keep existing headings. Output only the corrected note.\n\n{content}"

	DefaultSortPrompt = "Re-prioritize this checklist. For every item, assign a priority of \"high\", \"medium\", or \"low\" based on urgency and impact, and an \"order\" ranking the items from most urgent (1) to least. Return ONLY a JSON array, one object per item, copying each item's text exactly:\n[{\"text\":\"...\",\"priority\":\"high\",\"order\":1}, ...]\n\n{items}"
)

// Previous built-in default prompts. LoadConfig upgrades these to the current
// defaults so prompt improvements reach existing installs, while leaving any
// prompt the user has customized untouched.
const (
	oldSystemPrompt    = "You are a concise assistant. You only have access to the note provided. Do not reference external information. Be brief and precise."
	oldSummarizePrompt = "Summarize the following note in 3-5 sentences:\n\n{content}"
	oldCleanPrompt     = "Fix grammar, improve clarity, and clean the markdown formatting of this note. Return only the corrected note content, no explanation:\n\n{content}"
	oldSortPrompt      = "You are given a checklist. Re-evaluate and reorder items by urgency. Assign priority (high/medium/low) to each. Return a JSON array: [{\"text\":\"...\",\"priority\":\"high\",\"order\":1}, ...]. Return only valid JSON, no explanation:\n\n{items}"
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
	VaultPath     string `json:"vault_path"`
	LastNote      string `json:"last_note"`
	WindowWidth   int    `json:"window_width"`
	WindowHeight  int    `json:"window_height"`
	Model         string `json:"model"`
	AssistantName string `json:"assistant_name"`
	UpdateChannel string `json:"update_channel"`

	SystemPrompt        string     `json:"system_prompt"`
	Actions             []AIAction `json:"actions"`
	EnableTreeSummaries bool       `json:"enable_tree_summaries"`
}

func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", AppName)
}

// DataDir is the per-user data directory ($XDG_DATA_HOME/atlas-notes or
// ~/.local/share/atlas-notes). The vault, SQLite index, and the updater's source
// checkout all live under it.
func DataDir() string { return dataDir() }

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", AppName)
}

// ConfigPath is the absolute path of config.json.
func ConfigPath() string { return filepath.Join(configDir(), "config.json") }

// DefaultVaultPath is ~/.local/share/atlas-notes/vault.
func DefaultVaultPath() string { return filepath.Join(dataDir(), "vault") }

// DefaultDBPath is ~/.local/share/atlas-notes/index.db.
func DefaultDBPath() string { return filepath.Join(dataDir(), "index.db") }

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		VaultPath:     DefaultVaultPath(),
		WindowWidth:   1100,
		WindowHeight:  720,
		Model:         DefaultModel,
		AssistantName: DefaultAssistantName,
		UpdateChannel: ChannelRelease,
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
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.UpdateChannel == "" {
		cfg.UpdateChannel = ChannelRelease
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

// upgradePrompts replaces any prompt still equal to a previous built-in default
// with the current default, so prompt improvements reach existing installs
// without overwriting prompts the user has customized.
func upgradePrompts(cfg *Config) {
	if cfg.SystemPrompt == oldSystemPrompt {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	for i := range cfg.Actions {
		switch cfg.Actions[i].Prompt {
		case oldSummarizePrompt:
			cfg.Actions[i].Prompt = defaultSummarizePrompt
		case oldCleanPrompt:
			cfg.Actions[i].Prompt = defaultCleanPrompt
		case oldSortPrompt:
			cfg.Actions[i].Prompt = DefaultSortPrompt
		}
	}
}

// SaveConfig atomically writes cfg to config.json.
func SaveConfig(cfg Config) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ConfigPath(), data)
}
