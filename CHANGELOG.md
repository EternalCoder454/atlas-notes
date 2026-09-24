# Changelog

All notable changes to Atlas Notes are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.1] - 2026-09-24

A testing pass — fuzzing, soak runs, a race detector run and a look at the
filesystem paths — and the fixes it turned up. Three of these are the kind of
bug that only shows up after the app has been open for a while.

### Fixed
- **Memory no longer grows for as long as the app is open.** A 1500-cycle soak
  (open note · type · toggle task · save · refresh vault · home screen) grew the
  process from 73 MB to 1.36 GB. It now settles at around 180 MB and stays
  there — **92% less growth**, and the curve flattens instead of climbing. Three
  separate causes:
  - GTK 4 does not destroy a checkbox embedded in the text buffer when the line
    holding it is deleted; it stays parented to the editor, invisible, forever.
    Every note opened leaked one widget per task line. Rows are now removed when
    their anchor goes, and reused for the next note instead of being rebuilt.
  - The vault tree rebuilt its entire list — and with it every row widget — on
    any refresh, including after a save that changed nothing it displays. It now
    rebuilds only when what it shows actually differs.
  - The home screen was reconstructed from scratch on every visit. It is built
    once and its recent-notes list updated in place.
- **A note containing `<!-->` crashed the app.** The checklist parser searched
  for the closing comment marker from the start of the line, found the `-->`
  inside the opening `<!--`, and sliced backwards. Found by fuzzing; the corpus
  entry is now a regression test.
- **Opening a note marked it as edited.** A render pass cleared the flag that
  suppresses change notifications, so loading a note looked like typing in it:
  the indicator showed unsaved changes and an autosave was scheduled for a note
  nobody had touched.
- **Typing scheduled one timer per keystroke.** Each was harmless on its own,
  but the bindings keep every timer's callback alive for the life of the
  process, so a long writing session accumulated thousands. Autosave now keeps a
  single timer in flight, re-armed while typing continues.

### Security
- **Note and folder names can no longer escape the vault.** A note titled
  `../../secrets` was resolved literally: it wrote outside the vault directory,
  and renaming onto an existing path could overwrite a file elsewhere on the
  disk. Names are sanitized (`..` segments are dropped, not resolved) and every
  filesystem operation re-checks that the result is still inside the vault.
  Titles come straight from the title field, the tree's rename prompt, and the
  filenames in a synced vault, so this is now covered by tests.
- **Decompression is bounded.** A note is refused if it expands past 128 MiB, so
  a few kilobytes of hostile input in a synced vault can't exhaust memory.

### Added
- Fuzz tests for the checklist parser, the editor's markdown scanner and the
  assistant's Pango renderer (the last one checks that no markup the model emits
  can escape into the label).
- A concurrency test covering the real access pattern — the UI reading while the
  autosave goroutine writes and a vault scan runs — passing under `-race`.
- Tests for vault containment, corrupt and empty note files, and the
  decompression limit.
- `ATLAS_BENCH=soak=N` runs an editing session against the live UI and reports
  memory at checkpoints; `ATLAS_PPROF=file` writes a heap profile. Both are how
  the leaks above were found.

### Performance
- Opening a note is now faster than v0.4.3 (0.4 ms → 0.3 ms median on a
  2000-note vault) rather than slower, because rows are reused rather than
  rebuilt.
- Idle CPU is down a further ~25% against v0.4.3 (user time), with system time
  down ~45%.

[0.5.1]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.5.1

## [0.5.0] - 2026-09-23

A large interface pass, a new default model, and a performance pass across
startup, typing, memory and I/O.

### Added
- **A real home screen.** Launching with no note open (a fresh install, or after
  closing one) now shows a welcome screen — a greeting, four things you can
  actually do, your recent notes, and where your vault lives — instead of
  dropping you into a "Welcome" note that looked like a document you had somehow
  already written. Reachable any time with **Ctrl+H** or the home button.
- **Search.** The vault panel has a search field: type to filter every note in
  the vault by name, with the folder shown beside each match. **Ctrl+K**.
- **A formatting toolbar** above the note — bold, italic, strikethrough, code,
  headings, bullets, tasks, quotes and dividers — so the Markdown the editor
  understands is visible rather than folklore.
- **Keyboard shortcuts for everything**, listed in a new shortcuts window
  (**Ctrl+?**): new note (Ctrl+N), new checklist (Ctrl+T), new folder
  (Ctrl+Shift+N), find (Ctrl+K), rename (F2), home (Ctrl+H), panels (F9/F10),
  ask the assistant (Ctrl+L), and the formatting commands.
- **A main menu and an About window** in the header bar.
- **Richer Markdown rendering**: bullet lists hang their text off the marker,
  quotes are indented and italic, `~~strikethrough~~` and `---` dividers render,
  and a ticked task's text is struck through.
- **Due dates are visible.** A task with a due date shows it as a small badge
  beside the checkbox, amber today and red once it is overdue.
- **A status bar that says something**: words, characters, reading time, how many
  tasks are done, and which file in your vault the note is.
- **Prompt suggestions** in the assistant panel when it has nothing to show yet.
- **Text rendering control** (Settings → Model & Prompt → Text rendering). See
  *Fixed* below.
- **Panel widths are remembered** along with the window size.

### Changed
- **The default model is now `qwen3.5:9b`** (Ollama's Q4_K_M build, ~5.5 GB),
  replacing `qwen2.5:3b`. Installs that never chose a model are moved to it on
  upgrade; a model you picked yourself is left alone. Run
  `ollama pull qwen3.5:9b` — or set any other model in Settings.
- **The seeded guide note was rewritten** and renamed to *Getting Started*,
  covering the toolbar, the shortcuts and the search field.
- The note title is now a borderless title field with the folder above it, and
  the window title shows the note you are reading.
- The editor keeps the text at a readable width instead of stretching it across
  the whole window.
- A note opens at its top, fully rendered — the Markdown markers on the caret's
  line only appear once you move the caret or type.

### Fixed
- **Text is now crisp on a 1080p display.** GTK 4 renders glyphs unhinted, which
  looks right at HiDPI but soft and unevenly spaced at 1x. Atlas Notes now hints
  text (and rounds font metrics) on low-density screens, keeps GTK's defaults on
  HiDPI ones, and switches automatically when you drag the window between the
  two. Override it in Settings if you prefer one or the other.
- **Checkboxes line up with their text.** A widget embedded in the text buffer
  hangs off the line's baseline and cannot sit below it, so a stock-sized
  checkbox overhung the top of the text by a few pixels (and the old nudge
  margin only made the row taller). The checkbox, priority bar and due-date
  badge are now sized in em to the text's cap height, so each row spans exactly
  from the top of a capital letter down to the baseline, at any font size.
- The assistant's idle hint no longer renders as a block of selected text.
- Toolbar and card icons fall back to a glyph when the icon theme lacks them,
  instead of showing a broken-image icon.

### Performance
Measured with the built-in harness (`ATLAS_BENCH=…`, see the README), v0.4.3 and
v0.5.0 run interleaved on the same machine, median of three runs:

| | v0.4.3 | v0.5.0 |
| --- | --- | --- |
| Launch, 2000-note vault | 156 ms | **100 ms** (−36%) |
| — read syscalls / data read | 4768 · 2.0 MB | **739 · 0.74 MB** (−85% / −63%) |
| Launch, 200-note vault | 76 ms | 87 ms (richer window) |
| Keystroke + render, 50 KB note | 0.8 ms mean · 0.9 p95 | **0.4 ms · 0.5** (−51%) |
| Idle CPU, system time | 13-16 ms/6 s | **6-7 ms/6 s** (−55%) |
| Idle CPU, user time (200 notes) | 94 ms/6 s | **68 ms/6 s** (−28%) |
| Rebuild index, 1000 notes | 20.3 ms | **1.7 ms** (−92%) |
| Parse a task line | 2760 ns | **131 ns** (−95%) |
| Binary size | 33.2 MB | **21.7 MB** (−35%) |

What changed:

- **Launching no longer scales with the vault.** The vault scan used to run
  before the window appeared and decompressed every note on disk just to check
  it was readable. It now skips unchanged files by modification time, never
  decompresses anything, writes the index in one transaction, and — when an
  index already exists — runs after the first frame instead of before it.
- **Typing in a long note costs half as much.** A render pass re-tags only the
  lines an edit touched plus the caret's line, instead of re-tagging the whole
  document after every pause in typing. Loading a note also renders its
  checklists once rather than twice.
- **Checklist parsing is 20-100x faster** — a hand-written scanner instead of a
  regular expression. It runs over every line of the note on every pass.
- **The assistant's orb stops animating when idle**, so an untouched window
  costs essentially no CPU. Readiness polling backs off from 10s to 60s while
  Ollama is absent, and pauses entirely while the panel is hidden.
- **The vault tree indexes its rows by folder** instead of scanning every note
  each time GTK asks a folder for its children.
- **A smaller binary** (symbols and DWARF stripped), so there is less to read at
  launch.

Honest about the costs: the new window has more in it, so it takes roughly
10 ms longer to build and about 4 MB more memory, and opening a note went from
0.4 ms to 0.6 ms (the breadcrumb, window title, status bar and caret reset).
Capping the zstd worker pool was tried and reverted — it saved 2 MB but halved
decompression speed.

[0.5.0]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.5.0

## [0.4.3] - 2026-06-15

### Changed
- **Clean & Format applies more Markdown styling** — it now bolds lead-in labels
  and key terms and turns feature/step lists into bullets, instead of only fixing
  grammar. (How thoroughly depends on the model: small models style some of it;
  larger local models format the whole note — switch the model in Settings.)

[0.4.3]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.4.3

## [0.4.2] - 2026-06-15

### Fixed
- **Edit mode is now reliable.** Note edits run the model deterministically
  (temperature 0), so instructions like "add a checkbox to record a demo video"
  or "mark the SEO task done" are applied faithfully — no inventing tasks, echoing
  the instruction into the note, or dropping content.
- When an edit instruction makes no change (e.g. asking to reformat the whole
  note — that's Clean & Format's job), the assistant says so and points you there,
  instead of falsely reporting "Note updated".

[0.4.2]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.4.2

## [0.4.1] - 2026-06-15

### Added
- **Undo for AI changes.** After Clean & Format, Sort Priorities, or an Edit-mode
  instruction rewrites the note, a toast appears with an **Undo** button that
  restores the note to its previous content.

[0.4.1]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.4.1

## [0.4.0] - 2026-06-15

### Added
- **Edit mode for the assistant.** Toggle the pencil button in the input bar, type
  an instruction (e.g. "add a checkbox to record a demo video" or "mark the SEO
  task done"), and Atlas rewrites the **actual note** instead of just answering —
  adding `- [ ]` checkboxes on request and preserving the rest of the note. (Ask
  mode, for questions answered in the panel, is still the default.)

[0.4.0]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.4.0

## [0.3.2] - 2026-06-15

### Changed
- **Clean & Format now formats with Markdown** — it adds a heading, **bold** for
  key terms, and bullet/numbered lists for genuine lists, while preserving your
  facts and leaving existing task checkboxes (`- [ ]`) untouched.

### Fixed
- The assistant's answer area now renders fenced code blocks (```` ``` ````) as
  monospace, in addition to the inline `code`, bold, italics, headings, and
  bullets it already showed — so Markdown in replies displays cleanly.

[0.3.2]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.3.2

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
