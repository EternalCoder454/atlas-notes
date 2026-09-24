# Changelog

All notable changes to Atlas Notes are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

An interface pass over the words the app uses and where it puts its status.

### Changed
- **The Ollama card says it in two lines.** "Ollama required — run Ollama
  locally to use the assistant. Install it and pull <model> — this card clears
  once it's ready." It named the model rather than offering a choice: the app
  needs one specific model, and "pull your preferred model" would send someone
  down the wrong path.
- **The footer is the only place the app reports on your note.** Word and
  character counts, reading time, checklist progress, where the note lives, and
  whether it is saved. The save pill used to sit beside the title and the task
  count at the end of the formatting toolbar, so the header carried status the
  eye had to hunt through on the way to the note's name. The footer's own
  comment already claimed it showed checklist progress; now it does.
- **Recent notes are cards with two lines of the note in them**, so the home
  screen is a list you can recognise something in rather than a list of file
  names. Previews are cached against each note's modification time: the home
  screen refreshes whenever the vault changes underneath it, not only when it
  is opened, and re-reading four notes on every save would cost a long note its
  whole length to show two lines of it.
- **The assistant's starter prompts are four short chips in a 2×2 grid.** A chip
  wide enough to hold the whole question wraps onto two lines in a panel that
  narrow, and four of those stack into a wall. The question sent to the model is
  unchanged and sits in the tooltip. They also moved above the panel's
  explanatory sentence, because with the setup card taking room the scroller was
  cutting the second row in half.
- **Settings reads more plainly.** "Folder tree" is now "Hover previews";
  "Fetch the latest version from GitHub, rebuild, reinstall, and restart —
  automatically" is now "Automatically check and install updates from GitHub".
- **The install paths are folded away** behind a collapsed "Atlas Notes vX.Y.Z —
  system info" section. The version is what someone reporting a problem is asked
  for; the paths are for the rare occasion something has to be found on disk,
  and every visit to Settings was showing a block of somebody's home directory.
  They stay selectable, for pasting into a bug report.
- Text rendering's "Automatic (match the display)" is now "Automatic
  (recommended)", with the hint "Automatic picks per screen". It was suggested
  this be called "System Default", which would be untrue: automatic is this
  app's own per-display choice, not a setting the desktop provides.

- **Every icon in the app is now Material Symbols**, drawn from one set instead
  of nine hand-drawn ones beside nineteen borrowed from whatever icon theme the
  desktop happened to have. A toolbar assembled that way is a toolbar of
  mismatched weights. `scripts/import-icons.sh` turns Google's exports into
  what GTK wants — prefixed so they cannot lose a lookup to the system theme,
  filled black so anything that renders them outside GTK shows them, and sized
  16px — and refuses anything with a stroke or a `fill="none"` box, neither of
  which survives GTK's recolouring. Sources are kept in `assets/icons-src/`,
  attribution in `NOTICE`.

### Fixed
- **A change to the icons never reached disk.** The unpacked copy was stamped
  with the app's version, so any icon edited without a release going out — every
  icon change during development, and any release that redraws one without
  bumping the number — left the old file in place and the new name unresolvable.
  The stamp is now a digest of the icons themselves, and the directory is
  emptied before it is rewritten, so a renamed icon cannot leave its old name
  behind still resolving.

### Added
- `ATLAS_DEV_VIEW=home` and `ATLAS_DEV_VIEW=settings=<page>`, so the screenshot
  tooling can capture the home screen and a named settings section.
  `ATLAS_DEV_VIEW=icons` now lists whatever is embedded rather than a
  hand-written list that could fall behind it.

## [0.5.5] - 2026-09-24

Atlas Notes now tells you when a new version is out.

### Added
- **A launch-time update check.** A moment after the window opens, the app asks
  whether a newer version has been published on its channel. If there is one it
  presents **Update Found — v*x.y.z*** with a short, plain-language list of what
  changed and two buttons: **Update Later**, and **Update Now**, which runs the
  same fetch-build-reinstall-restart the Settings page has always offered, with
  its progress in a dialog of its own.
- **`WHATSNEW.md`**, the release notes the app reads and shows. They are written
  for people using Atlas Notes; this file remains the history for people working
  on it. A test fails the build if the two drift apart — if the newest entry in
  `WHATSNEW.md` is not the version being shipped, a release would either
  announce an update everyone already has or announce nothing at all.
- **Settings → App → "Check for updates when Atlas Notes starts"**, on by
  default, and `"check_updates"` in `config.json`. Turning it off means the app
  makes no network request of its own at all.
- **`internal/update`**, which knows how to read release notes and compare two
  version numbers and nothing else: no GTK, no installing, no side effects.
  Versions compare as numbers, so 0.5.10 is newer than 0.5.9, and anything that
  does not parse is treated as "not newer" — a check that cannot make sense of
  what it fetched must never offer an update.

### Changed
- The updater's fetch/build/restart is now one function, `installUpdate`, driven
  by both the Settings page and the new dialog rather than duplicated.
- **The version has one source of truth.** `internal/app/version.go` had drifted
  to 0.4.3 while the Makefile shipped 0.5.4, so a plain `go build .` produced a
  binary that misreported itself — harmless until an update check compares that
  number against what has been published. The Makefile now reads the version out
  of the source file.

### Fixed
- **Opening a note leaked memory, without bound.** Replacing a note's text and
  then putting the caret at the top emits `mark-set`; the text view answers
  that by updating the input method's spot location, which asks for the
  cursor's location, which lays the line out and keeps the result in GTK's
  line-display cache — a cached layout per note opened that nothing released.
  The cost scales with the length of the note: on a vault of long notes, 1,200
  opens grew memory by 271 MB. The text is now replaced with the view detached
  from the buffer, so there is no handler to answer, and the same 1,200 opens
  cost 81 MB — 70% less, with no measurable change to how long opening a note
  takes (0.43 ms at the median on a 2,000-note vault). On ordinary notes the
  difference is a few KB per open; this is insurance for the person with a very
  long note.

  Found with heaptrack, which named the allocation site outright. The remaining
  growth is about 10 KB per note opened on an ordinary vault: cairo's X11
  surface pools, GSK text nodes, and one binding-level reference per embedded
  checkbox that the GTK bindings never release. None of it is reachable from
  this side without changing how checkboxes are embedded.

### Privacy
The check is a single anonymous GET of a text file from the project's
repository, with an 8-second timeout, on a background thread. No identifier, no
version ping, nothing about the machine or its notes is sent, and a failure is
logged rather than shown. Being offline behaves exactly like being up to date:
silently. The README says so too, where it promises the app works offline.

### Verified
The four paths the check can take were each exercised against a local server on
a real launch: a newer version presents the dialog; the same version, an
unreachable server, and the setting turned off all leave the window untouched.
The parser and the version comparison were fuzzed for 16 million executions
without a crash, a malformed version reaching the dialog title, or a pair of
versions each newer than the other. Tests, `go vet`, `staticcheck`, `deadcode`
and `govulncheck` are clean, and the stability battery still passes 7/7.

The check costs what one HTTPS request costs, all of it after the window is up:
about 50 ms of CPU on a background thread, and about 5 MB of resident memory
that does not come back (measured over a 45-second idle run against the real
endpoint — it is mostly the TLS and certificate code becoming resident, which
is the price of making any HTTPS request at all). Time to first frame is
unchanged, because the check does not start until 1.5 seconds after it. Turning
the setting off costs nothing at all: no client is built and no request is made.

## [0.5.4] - 2026-09-24

A cleanup pass: dead code removed, files split along the seams they had grown
past, and a few hot paths simplified. No behaviour changes.

### Changed
- **The footer's counters walk the note once.** Words, characters and task
  progress were three separate passes over the document; they are now one,
  and the line-level task test (`checklist.TaskLine`) replaced the
  whole-document counter that wrapped it.
- **The vault panel builds its caches in a single pass** instead of walking the
  note list three times, and no longer keeps a second copy of it.
- **Large files were split along what they actually do**: the vault panel into
  the panel, its row factory and its editing menu; the assistant into its
  widgets and the calls it makes; the editor's checklist into rendering and its
  menu; and `app.go` into the application's lifecycle and the open note.
- The assistant's replies are escaped for Pango in one pass rather than three.

### Removed
- `checklist.HasItems`, `checklist.Progress` and `Item.Meta`, the editor's
  `View`, and the app's unread save-state field — all unreachable.
- The `title` column in the note index. It stored each note's file name, which
  is the tail of the path it sits beside, and nothing read it.
- The widget- and anchor-cost probes from the harness. They existed to measure
  what the GTK bindings retain during the leak hunt, and that question is
  answered; `ATLAS_BENCH` now offers only what the README documents.
- A stale import anchor and a comment left over from an earlier implementation.

### Verified
`staticcheck` and `deadcode` report nothing across the tree. Tests, race
detector, the stability battery (7/7), 3,000 randomized operations with GLib
criticals fatal, and the benchmarks all hold: typing 0.08 ms, search 0.07 ms,
opening a note 0.37 ms, launch 85 ms on a 2,000-note vault.

[0.5.4]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.5.4

## [0.5.3] - 2026-09-24

Consistent icons, and a stability pass that drove the app at random until
something broke.

### Changed
- **The toolbar's icons are now one set.** The desktop icon theme has no icon
  for a heading, inline code, a quote or a divider, so those buttons fell back
  to letters and punctuation — bold "H1", a pilcrow, an em dash — sitting next
  to the theme's icons at a different weight and size. Atlas Notes now carries
  its own symbolic icons for those, drawn on Adwaita's 16px grid, so the whole
  bar reads as one family and recolors with the theme in light and dark.
- **The assistant's button in the header is the assistant's own mark** — the
  orb from the panel it opens — rather than a second sidebar arrow.
- Bundled icons are unpacked into the app's data directory on first run and
  registered with the icon theme, so they work from any build without an
  install step. Buttons still fall back to text if a theme ever hides them.

### Fixed
- **Memory no longer balloons when notes are created, renamed or deleted.** The
  vault tree rebuilt its entire list — and with it a widget per row — on every
  change. Two thousand mixed operations grew the process to 1.9 GB; the list is
  now updated in place, touching only the rows that differ, and the same run
  settles at 102 MB.
- **A damaged index database is rebuilt instead of disabling the vault.** An
  index that could not be opened (truncated by a full disk, mangled by a sync
  client) used to leave the app running with no notes at all and no way out but
  deleting the file by hand. It is moved aside — kept, not deleted — and rebuilt
  from the notes, which are the source of truth.
- Removing a checklist row that GTK had already taken off the editor logged a
  warning; the row's parent is checked first.

### Added
- `ATLAS_BENCH=chaos=N` drives the app through N randomized operations —
  creating, renaming and deleting notes, searching, typing, ticking boxes,
  switching notes, toggling panels, undoing AI edits — for stability testing.
  Both bugs above were found with it.

### Testing
- 10,000 randomized operations: no crashes, no GTK warnings (with GLib
  criticals made fatal), goroutines flat, memory stable.
- A battery of hostile conditions, all survived: a read-only vault, a corrupt
  index, a missing vault directory, an unreadable config, the vault deleted
  mid-session, and two copies of the app on one vault at once.
- A vault of deliberately awkward notes — a 200,000-word line, 3,000 tasks,
  unterminated markers, control characters, mixed line endings, emoji, a
  50,000-character word — opens and edits without complaint.
- Fuzzing of all three parsers (165 s this round), the race detector across
  every package, and a 1,500-cycle soak: +52 MB, flat.

[0.5.3]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.5.3

## [0.5.2] - 2026-09-24

A performance pass driven by profiles rather than guesswork: CPU profiles of
typing, startup and idle, plus heap profiles of a long editing session.

### Performance
Against v0.4.3, interleaved runs, median of three:

| | v0.4.3 | v0.5.2 |
| --- | --- | --- |
| Keystroke + render, 50 KB note | 1.2 ms mean · 1.6 p95 | **0.07 ms · 0.16** (−94%) |
| Keystroke + render, small note | — | −54% |
| Launch, 2000-note vault | 156 ms | **91 ms** (−42%) |
| — read syscalls / data read | 4768 · 2.0 MB | **718 · 0.74 MB** (−85% / −63%) |
| Launch, small vault | 77 ms | 78 ms (was +30% before this pass) |
| Open a note, 2000-note vault | 0.4 ms mean · 0.6 p95 | **0.3 ms · 0.3** (−26% / −40%) |
| Search keystroke, 2000-note vault | — | **2.5 ms → 0.08 ms** (−97%) |
| Idle CPU, system time | 14 ms/6 s | **7 ms/6 s** (−53%) |
| Memory over a 1500-cycle session | +1284 MB | **+48 MB** (−96%) |
| Go heap during an editing session | 53.7 MB | **6.0 MB** |

What changed:

- **The status bar was costing more than the editor.** Recomputing the word,
  character and task counts pulled the whole document out of the text buffer
  and walked it twice — on every keystroke. It was 82% of the cost of typing a
  character into a 50 KB note. The footer now refreshes on its own short timer.
- **The markdown scanner no longer converts each line to a rune slice.** Every
  marker it looks for is ASCII, and a UTF-8 continuation byte can't be mistaken
  for one, so it scans bytes and converts the few offsets it emits — and only
  when the line actually contains multi-byte text. Lines with no inline markup
  skip the scan altogether.
- **Searching the vault no longer re-reads it.** Each keystroke walked the
  vault directory, re-queried the index and lower-cased every note's name. The
  vault is snapshotted instead, in the form the filter needs, and re-read only
  when something changes it.
- **The assistant panel is built after the window is on screen.** It is a third
  of the window to lay out and paint, including a Cairo-drawn orb, and none of
  it is needed to show the note you came back to. This removes the startup cost
  the new interface had added on small vaults.
- **The compressor no longer allocates a worker per CPU core** — over 40 MB of
  heap on a many-core machine, for files a few kilobytes long. The decompressor
  keeps the default, because that is the path you wait on when opening a note.
- **A save that would not change the file is skipped.** Typing and deleting
  again, or ticking a box twice, no longer recompresses and rewrites the note,
  touches its modification time, or disturbs the vault index.
- Smaller: icon-theme lookups are memoized, the assistant's action menu and
  setup card are built on first use, and the resting orb draws a simpler frame.

### Fixed
- Benchmark and screenshot runs no longer hand off to a running copy of Atlas
  Notes (and can no longer disturb it), which had been silently swallowing
  measurements.

### Added
- `ATLAS_CPUPROF=file` records a CPU profile; `ATLAS_BENCH=search=N` measures
  the vault search. Both documented in the README.

[0.5.2]: https://github.com/EternalCoder454/atlas-notes/releases/tag/v0.5.2

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
