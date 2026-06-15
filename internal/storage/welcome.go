package storage

const welcomeMarkdown = `# Welcome to Atlas Notes

Atlas Notes is a **local-first** notes & checklist app with an optional **local
AI** assistant. Everything you write stays in plain files on your own machine —
nothing is ever uploaded.

## The basics

- The **left panel** is your vault. Create folders and notes, drag a note into a
  folder, or rename and delete from the toolbar at the bottom.
- Write in **Markdown** and it renders as you type — # headings, **bold**,
  *italic*, and ` + "`code`" + `. The raw symbols only show on the line you're editing.
- **Ctrl + S** saves instantly, and Atlas Notes autosaves in the background. The
  marker by the title reads *Saved* or *Unsaved*.

## Checklists

Start any line with ` + "`- [ ]`" + ` and it turns into a real checkbox:

- [ ] Tick me off when you're done
- [ ] Right-click an item to set a **priority** <!-- priority:medium -->
- [ ] …or to give it a **due date** <!-- priority:high due:2026-07-01 -->

Priority shows as a colored bar to the left of the checkbox.

## The AI assistant (optional)

The **right panel** is powered by a local [Ollama](https://ollama.com) model —
` + "`qwen2.5:3b`" + ` by default. The dot is **green** when Ollama is reachable and
**red** when it isn't. The app works perfectly either way.

- **Summarize Note** — a short summary of what you're reading.
- **Clean & Format** — fix grammar and tidy the Markdown, in place.
- **Sort Priorities** — reorder a checklist by urgency.
- Or just **ask a question** about this note in the box.

No green dot? Install Ollama with ` + "`curl -fsSL https://ollama.com/install.sh | sh`" + `,
then run ` + "`ollama pull qwen2.5:3b`" + `.

Open **Settings** (the gear, top-right) to change the model, edit the system
prompt, or add your own AI buttons.

## Where your notes live

Your notes are plain files under ` + "`~/.local/share/atlas-notes/vault/`" + ` and your
settings under ` + "`~/.config/atlas-notes/config.json`" + `.

Delete this note whenever you're ready. Happy writing!
`

// EnsureWelcome writes the Welcome note on first launch, i.e. when the vault
// contains no notes yet.
func (s *Store) EnsureWelcome() error {
	notes, err := s.ListNotes()
	if err != nil {
		return err
	}
	if len(notes) > 0 {
		return nil
	}
	return s.WriteNote("Welcome", welcomeMarkdown)
}
