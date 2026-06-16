# Changelog

All notable changes to Atlas Notes are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1] - 2026-06-15

### Changed
- **Sharper AI prompts.** The system prompt and the built-in actions (Summarize,
  Clean & Format, Sort Priorities) were tuned for small local models: summaries
  scale to the note and avoid speculation; Clean & Format fixes mistakes without
  adding facts or turning prose into lists; "Ask" says when the answer isn't in
  the note. Installs still on the old defaults upgrade automatically — prompts
  you've customized are kept.

### Fixed
- **Sort Priorities now reorders the list** — items are ordered by their assigned
  priority (high → low), so urgent items rise to the top even when the model
  returns them in their original order.

[0.3.1]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.3.1

## [0.3.0] - 2026-06-15

A redesigned AI assistant with a focused, conversational layout.

### Added
- **Animated assistant orb** — a glowing mark that drifts gently when idle and
  swells with faster ripples while the model is generating.
- **Editable assistant name** (Settings → Assistant name; defaults to "Atlas").
- **Streaming replies** — answers type in live, with a caption showing real-time
  throughput (`generating… N tok, X tok/s`) and the final stats.
- **Markdown-rendered answers** — bold, italics, inline `code`, bullets, headings.
- **Ollama setup card** — when Ollama is unreachable or the model isn't installed,
  a card shows the exact commands to run (with Copy and Recheck) and clears itself
  once everything is ready.

### Changed
- The note actions (Summarize, Clean & Format, Sort Priorities) moved from
  standalone buttons into a menu in the input bar, beside the question box and a
  Send button.
- The assistant shows the model and its throughput in a caption under the name.

[0.3.0]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.3.0

## [0.2.0] - 2026-06-15

The first public release of Atlas Notes — a local-first notes & checklist app for
Linux with an optional local AI assistant. Your notes stay on your machine.

### Added
- **Local-first notes & checklists**, stored as zstd-compressed Markdown in a
  vault on your machine — no account, no cloud, no telemetry.
- **WYSIWYG Markdown editor**: headings, **bold**, *italic*, `code`, and live
  checkboxes with priority colors and per-item due dates.
- **Local AI assistant** via [Ollama](https://ollama.com): Summarize, Clean &
  Format, Sort Priorities, and free-form "Ask about this note". Every call is
  async, and the model, system prompt, and action buttons are configurable.
- **Three-panel layout** (folder tree · editor · AI assistant), each pane
  resizable and collapsible.
- **Folder tree** management: right-click menu (new note/folder, rename, delete),
  double-click a note to rename, and drag a note into a folder.
- **Ctrl + S** to save instantly, plus background autosave, with a red/amber/green
  indicator (unsaved / saving / saved).
- **In-app updater** with a **Release / Beta channel** selector (Settings → App)
  that follows the `main` or `beta` branch.
- **One-command Fedora installer** (`scripts/install-fedora.sh`).

### Performance & reliability
- Note saves run **off the UI thread** (async), so typing and saving never block
  the interface — backed by a thread-safe, race-tested storage layer.
- The SQLite index uses **WAL + `synchronous=NORMAL`**, keeping `fsync` off the
  writer path; the index is a rebuildable cache (the Markdown files are the
  source of truth).
- Per-keystroke work and autosave are debounced; notes are written atomically
  (temp file + rename) so a note is never half-saved.

### Notes
- A note's **title is its filename** — the tree label matches the title field
  above the editor; an in-body `# H1` is treated as content.

[0.2.0]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.2.0
