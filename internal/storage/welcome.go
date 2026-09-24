package storage

// welcomeMarkdown is the note seeded into an empty vault. It is written the way
// the editor renders: every bold or italic run stays on one line, because the
// inline parser works line by line.
const welcomeMarkdown = `# Getting Started

Atlas Notes keeps your writing in plain Markdown files on this machine. No account, no cloud, no telemetry — and an optional AI assistant that also runs locally.

## Writing

Type Markdown and it renders as you go. The raw symbols appear only on the line you are editing, so a note reads like a document while you write it.

- ` + "`# `" + ` for a **title**, ` + "`## `" + ` for a **section**
- ` + "`**bold**`" + `, ` + "`*italic*`" + `, ` + "`~~strikethrough~~`" + ` and ` + "`` `code` ``" + `
- ` + "`- `" + ` for a bullet, ` + "`> `" + ` for a quote, ` + "`---`" + ` for a divider

The toolbar above the note does the same thing with one click, and **Ctrl+B**, **Ctrl+I** and **Ctrl+E** work as you would expect.

## Checklists

Start a line with ` + "`- [ ]`" + ` — or press **Ctrl+Shift+T** — and it becomes a real checkbox. Right-click a checkbox to set a priority or a due date.

- [ ] Tick this off when you have read it
- [ ] Set a priority — the colored bar shows it <!-- priority:medium -->
- [ ] Give a task a due date and it appears beside the box <!-- priority:high due:2030-01-01 -->

The counter above the note tracks how many are done.

## Finding things

- **Ctrl+K** searches your whole vault by name.
- **Ctrl+N** starts a note, **Ctrl+T** starts a checklist, **F2** renames one.
- **Ctrl+H** returns to the home screen; **Ctrl+?** lists every shortcut.

Drag a note onto a folder to move it. Right-click in the vault panel to create, rename or delete.

## The assistant (optional)

The right-hand panel talks to [Ollama](https://ollama.com) running on this machine — the default model is ` + "`qwen3.5:9b`" + `. The dot is **green** when Ollama is reachable and **red** when it is not; everything else works either way.

- **Summarize Note** — what this note says, briefly.
- **Clean & Format** — fixes grammar and tidies the Markdown in place.
- **Sort Priorities** — reorders a checklist by urgency.
- Or ask it anything about the note you have open.

No green dot? Install Ollama with ` + "`curl -fsSL https://ollama.com/install.sh | sh`" + `, then run ` + "`ollama pull qwen3.5:9b`" + `.

## Where your notes live

Notes are compressed Markdown files under ` + "`~/.local/share/atlas-notes/vault/`" + ` and your settings sit in ` + "`~/.config/atlas-notes/config.json`" + `. Point ` + "`vault_path`" + ` anywhere you like — a synced folder works fine.

Delete this note whenever you are ready. Happy writing.
`

// GuideMarkdown returns the text of the built-in walkthrough note, so the
// welcome screen can re-create it if the user deletes it.
func GuideMarkdown() string { return welcomeMarkdown }

// EnsureWelcome writes the guide note on first launch, i.e. when the vault
// contains no notes yet.
func (s *Store) EnsureWelcome() error {
	n, err := s.CountNotes()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.WriteNote("Getting Started", welcomeMarkdown)
}
