# Changelog

All notable changes to Atlas Notes are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **Opening a note with tables and diagrams is about 40% faster**: 2.71 ms
  where 0.10.0 took 4.62 (median of six interleaved runs of 300 opens, across
  30 different notes of 12 tables and 12 Mermaid diagrams each). A table's grid
  keeps its cells when the note closes, and the next table it shows sets only
  the cells that differ rather than taking every one apart and building it
  again; a cell of plain words is set as text, without Pango reading it as
  markup; and a diagram's source comes from the lines the render pass already
  holds, rather than from the editor a line at a time, twice. Screenshots
  before and after, switching between notes whose tables differ in shape,
  match pixel for pixel. Typing beside or inside a table or diagram measured
  the same before and after (0.02 to 0.1 ms a keystroke).
- `ATLAS_BENCH_LINE=L` makes `ATLAS_BENCH=editor` type at the start of line L,
  so typing beside a table or a diagram can be measured, not only at the top.

## [0.10.0] - 2026-09-30

### Changed
- **Settings is a page of the window, laid out like Atlas Monitor's.** It
  opens in place of the note, from Settings at the foot of the vault panel
  (still Ctrl+,), rather than as a dialog. Each setting is a card of its own,
  with an icon, a title, one line saying what it does and its control at the
  end, under the headings Appearance, Notes, Assistant, Updates and About;
  the model sits under the assistant's name, and the theme circles under
  "Use system setting". The search and the list of sections are gone: the
  page is short enough to scroll, and the command box still jumps to each
  section. Text applies on Enter, on leaving the field, or on leaving the
  page. Updates shows the version with an Update button beside it, and the
  channel says in a line what each one is.

### Fixed
- **The assistant refused to write anything the note did not already hold.**
  Every question went to the model as "answer using only the note; if it
  does not contain the answer, say so", so Better title came back with "this
  note contains no title suggestions". Requests to write something (titles,
  a summary, an explanation) are now framed as that, with the note's title
  included; questions about the note are still answered from it, and only
  those the note truly does not cover get "the note doesn't say". The four
  chips send precise requests (Better title asks for three, as a numbered
  list), the default system prompt no longer forbids "inventing names",
  which a small model read as forbidding titles, and an edit's instruction
  now comes after the note so a long note cannot bury it. A prompt shortcut
  written without {content} gets the note anyway. Installs still using the
  old default system prompt or Summarize prompt are moved to the new ones.

### Added
- **Things move rather than jump.** A folder's notes slide open beneath it
  and fade in one after another, and close up again before it collapses; its
  arrow turns, and a second click while it closes opens it back up. The side
  panels slide in and out and turn round mid-way if toggled again. A note's
  title comes up as it opens, Home, Tasks and Settings rise in, the note
  opened beside another fades in, and the assistant's answer fades in as it
  starts. All of it follows the desktop's animation setting.

## [0.9.0] - 2026-09-30

### Added
- **A new icon, and an intro when the app opens.** Atlas Notes and Atlas
  Monitor share the new Atlas mark, on the desktop, in the title bar and on
  Android. Opening the window plays the mark drawing itself, then "Atlas
  Notes" sliding out beside it, and fades into the app in under three
  seconds. A click or any key skips it; Settings, Appearance, Intro at startup
  turns it off, and it never plays when the desktop has animations off.
- **Select several notes and folders and delete them together.** Ctrl+click
  marks a row, Shift+click a run of rows, and once anything is marked a plain
  click marks or unmarks; long-press or Select in a row's menu starts it too,
  and Ctrl+Space, Space and Shift+arrows do it from the keyboard. A bar under
  the list counts them and has Select All and Delete; Escape stops. One
  dialog lists what goes to the Trash, by path, and says when some are inside
  marked folders or hidden by a search. Edits to an open note are saved first.

### Fixed
- **A task's due date showed over another note.** GTK cannot take a widget
  laid over a text view off it again, so every chip, table, picture, diagram
  and embed the editor put away stayed on the view: a "Jul 1" from one note
  floated over the next. They are now hidden when done with.
- **A folder made while empty could not be opened** once notes were put in
  it, so the notes could not be seen, and the panel could not highlight the
  open one. Folders, and folders in folders, now open whenever they have
  something in them, and an open folder shows notes added to it.
- **A click on a table** puts the caret in the cell that was clicked, at the
  place it was clicked, instead of on the table's first line.

### Changed
- **The outline sits beside the page**, in the margin left of the text, rather
  than on the document at its right edge.

## [0.8.2] - 2026-09-30

### Added
- **One installer for every distro**: `scripts/install.sh` detects Fedora,
  Debian, Ubuntu, Linux Mint, Arch and openSUSE (and their relatives), installs
  the build dependencies with `dnf`, `apt`, `pacman` or `zypper`, fetches Go from
  go.dev (checksum verified) when the distro's is too old, checks that GTK and
  libadwaita are new enough, then builds and installs. `--update` rebuilds,
  `--uninstall` removes the app and keeps your notes and settings, and `--purge`
  can delete those too after an explicit confirmation. The old
  `install-fedora.sh` URL still works.
- **Diagrams in notes.** A ```` ```mermaid ```` flowchart is drawn in place: boxes
  with a bold title and a quieter second line, groups with a heading, arrows
  with labels, and highlighted boxes (any `style` or `classDef` colour becomes
  the theme's accent). It is laid out automatically, fits the page (scrolling
  sideways when it must), follows the theme in light and dark, and shows its
  text when the caret is in it. Obsidian and GitHub draw the same text. The `/`
  menu inserts a starter diagram. **Gantt charts** (sections, done, active and
  critical tasks, milestones, `after` dependencies, weekends excluded, a line
  for today) and **sequence diagrams** (participants and actors, every arrow
  kind, activation bars, notes, and loop, alt, opt, par, critical, break and
  rect frames) are drawn too, each with its own `/` menu entry.
- **Draw flowcharts by hand.** The edit button on a flowchart (or Edit diagram
  in the command box, or Diagram in the `/` menu) opens it on a canvas: drag
  boxes, drag from a box's edge to another box to connect them, double-click
  to rename a box or label an arrow, change shapes, highlight, group, undo,
  zoom, and connect or move boxes from the keyboard. Done writes the Mermaid
  back as one undo step, keeping the lines it did not change; where you put
  boxes is saved as `%% atlas:pos` comments, which Obsidian and GitHub ignore.
  Done checks the block is still where it was, so an edit to the note while
  the editor is open is never overwritten, and a protected note is refused. Exports carry it: SVG in HTML, a picture in
  Word and OpenDocument. Anything else it cannot draw stays text, labelled
  with the reason; oversized or malformed diagrams from a synced note are
  refused rather than slowing the editor.
- **A Flatpak, for distros too old to build Atlas Notes.** It needs GLib 2.88,
  GTK 4.22 and libadwaita 1.9, which Debian 13, Ubuntu 24.04 and Linux Mint 22
  don't have. Each release now carries an `atlas-notes-<version>.flatpak` on
  the GNOME 50 runtime, and the installer picks it by itself there: it asks
  the package manager which libadwaita it offers before installing anything,
  and installs Flatpak and the bundle instead. `--update` replaces the bundle
  with the latest release's and `--uninstall` removes it, keeping the notes.
  The Flatpak asks for no access to your files; pictures and exports go
  through the file chooser portal.
- **Packages**: `packaging/PKGBUILD` for Arch and `packaging/atlas-notes.spec` for
  RPM and COPR.
- The Update button knows how the app was installed. A copy owned by `pacman`,
  `dnf`, `apt` or `zypper` is given that manager's update command instead of
  being rebuilt over, and a copy with no source is pointed at the installer.
  Settings shows how this copy was installed. In the Flatpak it gives the
  one command that updates it.

### Fixed
- The welcome note said notes are compressed and live in `~/.local/share`;
  they are plain Markdown by default, and in the Flatpak they live elsewhere.
  It now points at the folder shown on the home screen.

### Changed
- **CI runs the tests.** A Tests workflow runs `go vet` and `go test` on every
  push to beta and every pull request, in a Fedora 44 container (Ubuntu's GTK
  is too old); about a minute when its cache is warm. Every job has a time
  limit, and a push that only changes documentation builds nothing.
- **Typing is about seven times faster**: 0.014 ms a keystroke where 0.8.1
  took 0.102 (bench.sh, 10,000-note vault). An edit that stays inside one
  plain line no longer re-reads the whole note for its blocks, headings and
  fences, and the pointer is looked up once rather than on every redraw.
- **Indexing a large vault for the first time is about 13% faster**
  (2.34 s to 2.05 s for 10,000 notes): its statements are prepared once per
  batch rather than on every write.

## [0.8.1] - 2026-09-30

### Fixed
- **The phone showed an error under every note without a task**: "Value null
  of type org.json.JSONObject$1 cannot be converted to JSONArray". The shared
  core returned an empty list as null, which the app cannot read as a list,
  and 0.8.0 began reporting the failure where earlier versions hid it. The
  core now always sends a list, and the app reads a missing one as empty.

## [0.8.0] - 2026-09-30

### Added
- **Live preview.** Markdown's symbols show only around what the caret is in:
  the `**` of the bold word being edited, the brackets of that one link, a
  heading's `#` with the caret at the line's start. Everything else reads as it
  will print, so text no longer jumps as the caret moves between lines. List
  markers are drawn as bullets (the file keeps `-`, `*` or `+`), numbered
  lists indent, a quote keeps a dimmed bar, and fenced code blocks are set in
  monospace on a grey ground, where `- [ ]` stays code. Tables draw as
  tables, with a bold header, hairlines and each column's alignment, and turn
  back into their Markdown when the caret is in them. Enter continues a
  list, a numbered list with the next number, a checklist with a new box and
  a quote with `> `; Enter on an empty item ends it. Undo and redo step over
  the drawing of bullets, and the caret stays out of a task's hidden
  metadata, so a new line never takes its due date with it.
- **A properties card for front matter.** A note that starts with a YAML
  block shows it as a card: text, numbers, dates with a calendar, true or
  false as a switch, lists and tags as chips, and Add property. An edit
  rewrites only that property's lines, as one undo step, keeping quotes,
  order, comments and line endings; anything the card cannot edit safely
  (nested maps, block text) is shown and left alone. Tags in front matter
  now count as the note's tags, and existing notes are read again once so
  theirs do too.
- **An outline beside the page.** A thin column of marks at the right edge,
  one per heading, the current section in the accent colour; hover it for the
  names and click one to go there. It appears once a note has three headings.
  Ctrl+Shift+O shows it from the keyboard.
- **Obsidian's Markdown reads as it should.** Callouts (`> [!note] Title`,
  and tip, info, warning, danger, success, question, quote, example, todo)
  are tinted cards with an icon, folded with `-` and unfolded with `+`, and
  open or close with a click without touching the file. `==highlights==` are
  highlighted and footnotes set small. `![[Note]]` and `![[Note#Heading]]`
  show that note or section in a bordered block, kept current when it
  changes; a protected note shows as such rather than its text. Code blocks
  carry their language and a copy button, and room inside their background.
  An embed draws what it shows: tables as tables, tasks with their boxes
  and dates, code as code. `$$E = mc^2$$` and `$x$` are set in serif italic
  with their dollar signs hidden (prices such as $5 stay prices), footnote
  definitions read as a raised number and their text, and Mermaid blocks
  are labelled as diagrams. Headings fold from a chevron in
  the margin, and a fold opens by itself when the caret, a find or a
  selection reaches into it.
- **A command box on Ctrl+P.** One field for notes, commands (each with its
  shortcut), settings and tags; `>` narrows to commands, `#` to tags, and `?`
  asks the assistant. Ctrl+K and Ctrl+Shift+F still search the vault.
- **A `/` menu in the editor.** At the start of a line or after a space, `/`
  offers headings, lists, a task, a quote, a callout, a table, a code block, a
  divider, an image and a link, and the assistant's Summarise, Continue
  writing and Make a checklist. It stays out of code and of paths like
  `a/b`.
- **Mentioned, not linked.** Under a note, *Mentioned in* lists notes that
  name it without linking to it, each with a Link button that turns the
  first plain mention into a link. It leaves code, links, front matter and
  tables' cells alone, keeps a version of the note it changes, and does
  nothing to a note that already links.
- **A Tasks page** (Ctrl+J, or Tasks under Home): every open task in the
  vault, grouped Overdue, Today, This week, Later and No date, with its note,
  priority and date. Ticking one completes it in its file.
- **Open to the side.** A second note beside the first, from a note's menu in
  the vault panel, a link's right-click menu, Ctrl+Shift+click or Alt+click on
  a link, Ctrl+\\ or the command box. It saves, renames, moves, locks and
  reloads like the main one, and the same note is never open in both.
- **Split and merge notes.** Move a selection to a new note (Ctrl+Shift+M)
  and a link to it takes its place, as one undo step. Merge a note into
  another (Ctrl+Alt+M, or Merge into… in the vault panel): its text goes
  under its own heading at the end, its front matter tags join the target's,
  picture paths are fixed for the new folder, links to it point at that
  heading, and it goes to the Trash. A version of both notes is kept.
- **Version History shows what changed.** Beside Preview, Changes compares a
  version with the note now, lines removed and added tinted and unchanged
  runs folded away. It says so when two versions differ only in line endings.
- **Find and replace in the open note.** Ctrl+F opens a find bar with a match
  count, previous and next, Match case, and a replace row (Ctrl+R) with Replace
  and Replace All; one undo reverts a Replace All. Matching is literal, by
  character, and never lands on a checkbox. The vault search keeps Ctrl+K and
  Ctrl+P and gains Ctrl+Shift+F; on the home screen Ctrl+F still means it.
- **Links between notes.** `[[Name]]`, `[[Folder/Name]]`, `[[Name#Heading]]` and
  `[[Name|shown text]]` draw as links, open on click (Ctrl+click on the line
  being edited), and make the note when it does not exist yet. Typing `[[`
  suggests notes. Renaming or moving a note, or a folder, rewrites the links
  that named it, and a bare name that meant another note of the same name is
  left alone. *Linked from* under a note lists the notes that link to it. Web
  addresses and `[text](url)` links open in the browser; other schemes are
  refused.
- **Tags.** `#tag` anywhere outside code tags a note; `#project/atlas` nests
  under `#project`. Clicking a tag, a tag on the home screen, or searching
  `#tag` in the vault panel lists the notes that carry it. Typing `#` suggests
  existing tags. A locked note's tags and links are never indexed, and a
  trigger enforces it as it does for search.
- **Pictures in notes.** Paste or drop an image, or use Insert Image
  (Ctrl+Shift+I). It is stored in the vault's `attachments` folder: the longest
  side capped at 3840 px, JPEG location and camera data removed losslessly
  (APP1, APP13 and COM segments), PNG text and eXIf chunks and WebP EXIF and XMP
  removed, EXIF orientation applied, and a clipboard screenshot kept as PNG
  unless it is photographic, when a JPEG under 60% of its size wins. Headers
  claiming more than 100 megapixels are refused before decoding. A protected
  note's images are encrypted with it, and follow it through lock, unlock and a
  password change. Exports carry the pictures: embedded in Word and
  OpenDocument files, inline in HTML.
- **Due tasks.** The home screen lists unfinished items that are overdue, due
  today and due this week, from a new index of due items. The desktop sends a
  notification for what is due (Settings, Reminders), and the phone one each
  morning at nine.
- **Daily notes and templates.** Ctrl+D opens `Daily/<today>`, starting from
  `Templates/Daily` when there is one. Any note in `Templates` can start a new
  note (Ctrl+Alt+N), with `{{title}}`, `{{date}}`, `{{time}}`, `{{datetime}}`,
  `{{weekday}}` and `{{longdate}}` filled in. The home screen has a card for
  today's note, and the phone a Today button and shortcut.
- **Version history.** An earlier version of a note is kept at most every ten
  minutes, for 90 days and up to 50 per note, in the data directory, not the
  vault, so it never syncs. Version History (Ctrl+Shift+H) shows them with a
  preview and restores one; the text replaced is kept too. A locked note's
  versions are sealed, locking seals the plain ones, and a password change
  reseals them.
- **A bar along the bottom of the phone app**, where a thumb reaches: back
  and forward through the notes you have opened, search, a new note, today's
  note, and where your notes are kept. It hides while the keyboard is up.
- **Capture on the phone.** Share text into Atlas Notes, or start a note from a
  launcher shortcut, a quick-settings tile or a home-screen widget; a new note
  opens with the keyboard up.
- **`[[links]]` in exports** read as their text, and tags survive.

### Changed
- **A new look, after Windows 11 and Atlas Monitor 0.12.** The title bar and
  the side panels are one surface, with the app's name at the start of the
  title bar, and the note sits on a page of its own above them, rounded where
  they meet. Home is the first entry in the vault panel and Settings the last,
  in Task Manager's rows with an accent pill on the current one; the vault's
  notes take the same shape. Titles are set in a light weight, and every card
  and dialog list shares one 7px radius.
- **Ten colour themes**, Atlas Monitor's: Light and Dark, and Nord, Ember,
  Sage, Dracula, Rose, Solarized, Ink and Contrast, each held to WCAG's
  contrast minimums by a test. The default still follows the desktop. Links,
  tags and web addresses in a note take the theme's accent.
- **Icons are one Material Symbols set** (Outlined, weight 400, as Atlas
  Monitor's), none borrowed from the desktop's theme any more: the find bar's
  four were. Each is sized for where it sits, and dimmed at one of two levels,
  quiet for secondary icons and full for the title bar and navigation. The
  home screen's Today card has a calendar rather than a history clock. The
  title bar keeps the app's name alone at its start; the panel toggles, New
  note and the menu are grouped at the end.
- **Settings is one page that applies as you change it.** Appearance, Notes,
  Assistant, Updates and About in one scroll, with a list of the five down the
  left to jump between them and a search box over every setting. No Save
  button: each change takes effect at once and is saved. Nothing hides behind
  a sub-page or an expander: the prompt shortcuts are cards on the page, and
  the paths that were folded into "System info" are in About. The theme picker
  is ten circles under Appearance, with "Follow the desktop" beneath.
- **Window transparency** (Settings, Appearance): off, subtle, medium or strong.
  True translucency rather than a blur: the frame and the page let the desktop
  through, text stays solid, and dialogs follow. Only where the desktop
  composites, and not on Windows; elsewhere the setting says why.
- **Task dates and priorities are written as Obsidian's Tasks plugin writes
  them**, `⏫ 📅 2026-10-01` after the text, so they read cleanly in Obsidian
  and anywhere else; the old `<!-- due:... -->` comment is still read, and a
  line is only rewritten when its date or priority is changed here. 🔺 and ⏬
  read as high and low. A task's date chip sits after its text, so every
  task's text starts right after its box, with a date or without.
- **Notes are plain Markdown files by default.** The format is a setting of the
  vault (`.atlas-vault.json`, so every synced device agrees): plain `.md`,
  Zstandard `.md.zst`, Gzip `.md.gz` or XZ `.md.xz`. Every form is read whatever
  the setting, saving a note writes it in the vault's format and removes the
  old file, and changing the setting converts the vault in the background,
  keeping modification times. A vault from before this (all `.md.zst`) is
  converted to plain Markdown on first launch. Locked notes keep their payload
  (zstd inside the seal), so older versions still open them. Gzip and XZ reads
  are capped like zstd's, so a decompression bomb is an error.
- **The vault scan skips folders whose names start with a dot.** Syncthing keeps
  old versions of every note in `.stversions`, and they were being listed as
  notes.

### Fixed
- **Creating a note could overwrite a protected one.** `UniqueName` only looked
  for the unprotected form, so a new "Untitled" could be written over a locked
  "Untitled". It looks for every form now.
- **A folder named with `_` or `%` matched other folders in the index**, so
  deleting or renaming "a_b" also moved or dropped "axb"'s notes from the index.
  The patterns are escaped.
- **Renaming a folder around the open note** left the editor pointing at the
  old path.
- **Renaming a note onto another note's name replaced that note.** The file
  rename overwrote the target without a word. It is refused now, and a rename
  moves every form of a note, rolling back if one cannot move.
- **A failed password change could leave locked notes under a key nobody could
  derive**, when it stopped part-way: every note was resealed before the new
  key's salt was saved. It now opens and reseals everything in memory first,
  writes it all, rolls back on a write error, and saves the new salt last.
- **Renaming a folder with a non-ASCII name corrupted its notes' paths in the
  index**: the rewrite counted bytes where SQLite counts characters.
- **Selecting, clicking or hovering could close the app** ("byte index off the
  end of the line"). Hidden Markdown was GTK "invisible" text, which GTK drops
  from a line's layout and maps around on every hit-test; when a line's hidden
  runs changed between a layout and a hit-test (the caret reaching a line, a
  drag in progress), GTK mapped past the end of the line and aborted. A gdb
  backtrace put it in GTK's own selection drag. Hidden markers are now shrunk
  to nothing and drawn transparent instead, so they stay in the layout and
  there is nothing to map around; tag changes also wait for a drag to end.
  Stress runs that died within two rounds now pass 40, over and over.

### Security
- **A note in two formats keeps both.** When a sync leaves `Plan.md` and
  `Plan.md.zst` saying different things, saving keeps the other as a conflict
  copy instead of deleting it, and the newest form is the one read.
- **Earlier versions of a note locked elsewhere are sealed here** when the vault
  is unlocked, and are hidden until then.
- **Pictures cannot reach outside the vault**: symbolic links are refused, and
  the folder a picture is really in must be inside the vault. A picture another
  locked note shows stays sealed when one note is unlocked.
- **Links cannot overwrite notes.** Following `[[Idea.md]]`, or a link the index
  has not caught up with, opens the note rather than writing a stub over it.

## [0.7.1] - 2026-09-28

### Changed
- **gotk4 0.4.1**, whose release reworks how Go wrappers own and release GTK
  objects. It drops go4.org/unsafe/assume-no-moving-gc, which reached into the Go
  runtime through a linkname and would stop the app at launch if a Go release
  ever let heap objects move, for the standard library's weak pointers. In a
  4,000 cycle soak nothing leaks with either version: heaptrack finds no growth
  in live C memory between 200 and 2,000 cycles once released objects are let
  go. Resident memory creeps by 11 KB a cycle, against 8 KB before, which is the
  allocator keeping freed pages rather than memory in use.
- **Built with Go 1.27.1**, named by a toolchain line in go.mod. Releases had
  been built with Go 1.26.0 exactly: setup-go v5 read only the go line, which is
  the oldest Go the code accepts. v6 reads the toolchain line.
- **The Android app builds on Java 25** (Temurin), with Android Gradle Plugin
  9.4 and Gradle 9.8. The app itself still targets Java 17 bytecode.
- **Beta pushes skip the Windows build**, which a release or a manual run still
  does, and the Windows job pins Go through setup-go rather than taking MSYS2's.

### Fixed
- **The soak benchmark no longer reports a leak that is not there.** It exited
  straight after its last garbage collection, before gotk4 had released what
  that collection found, so a longer soak held more at exit and looked like it
  leaked. It now lets releases finish before the final sample.

## [0.7.0] - 2026-09-28

### Added
- **Search inside notes.** The vault panel finds notes by what they say as well
  as by name. Names match as you type; text matches follow once typing pauses,
  newest first, from three letters. The index is SQLite FTS5 in its contentless
  form, which stores which words are in which note and not the notes' text. At
  10,000 notes it adds 9 MB and answers in 2 to 7 ms.
- **Locked notes stay out of it, provably.** Triggers on the notes table take a
  note out of the index whatever marks it locked. Because deleting from an index
  does not erase the bytes, locking also optimises the index, runs with
  secure_delete, and truncates the WAL; a test greps the raw database files for a
  locked note's words, and fails if any one of the three is removed.
- **Export** a note as a Word document, OpenDocument text, Markdown, a web page
  or plain text: from the main menu (Ctrl+Shift+E), a note's right-click menu, or
  the share button on the phone. Headings, lists, checklists with their priority
  and due date, quotes, code and links carry over; Word and OpenDocument files
  use the office suites' own heading styles and real lists. LibreOffice opens all
  three in a test. Exporting a protected note warns that the copy is not.
- **Deleted notes and folders go to the Trash**, where they can be restored,
  instead of being gone. Where a drive has no Trash, nothing is deleted until you
  agree to delete it permanently. Locking a note never sends its plaintext to the
  Trash; a test holds that.
- **The phone can use a shared folder**, so a sync app such as Syncthing can keep
  the same notes on a phone and a computer. The password file lives in the vault,
  so locked notes open with the same password on both. Each folder has its own
  index in private storage, and the guide note is never written into a shared
  folder. The phone now checks the folder for changes when it opens and when it
  comes back to the front, and finds notes by their text too.
- **`make bench`**, against a vault generated from a seed, on a virtual display,
  sandboxed from the real vault with a canary that fails the run if anything
  real changed.

### Changed
- **The assistant folds away below 1200 px again**, as 0.5.8 promised, but only
  when the window crosses that width, so opening it in a narrow window keeps it
  open. It has a minimum width now, and its width is only saved while it is
  showing; it had been saved as 11 px, the width of the drag handle.
- **Icons are named `atlasnotes-*`.** Atlas Monitor installs five icons with the
  same `atlas-*` names into the shared user theme, which is searched first, so
  Atlas Notes had been drawing Monitor's.
- **Reading notes after the first frame is off the main thread.** A first launch
  with 10,000 notes stopped the window for 185 ms; with the search index to fill
  as well it now stops it for 48.5 ms at most.
- **The default vault is saved as `""`**, so a restored or copied data directory
  finds its own vault rather than the old path.
- **The release builds also run on main, for the caches.** Actions scopes a
  cache to the ref that saved it: a run on a tag cannot read one saved on beta,
  and only the default branch's caches are readable from every ref. So the
  v0.6.0 tag built gotk4 from cold and took twenty minutes, while the same
  commit on beta had taken three. Merging to main warms the cache the tag build
  then restores.

## [0.6.0] - 2026-09-26


### Performance
Measured against a frozen 10,000-note vault, before and after, median of
repeated interleaved runs. Everything below was tuned against 2,000 notes
before this pass.

| | before | after |
| --- | --- | --- |
| First launch, no index yet | 242 ms | **168 ms** (-31%) |
| Search, per keystroke | 0.59 ms | **0.28 ms** (-53%) |
| Warm launch | 130 ms | 133 ms (within noise) |
| Open a note | 0.25 ms | 0.26 ms |
| Resident memory | 101 MB | 100 MB |

- **Working out which notes are checklists left the launch path.** The vault
  scan had started opening and decompressing every changed note to find its
  task lines, and on a first launch — when the index is built before the window
  appears — that was 68 ms of a 10,000-note vault's 242 ms. The scan is back to
  reading names and modification times and opening nothing; it marks what
  changed, and `ResolveTaskFlags` reads those notes once the window is up. In a
  normal session there is nothing to resolve, because the write path records
  the flag from content it already holds.
- **The vault panel's refresh signature stopped formatting.** Lock, checklist
  and favourite state were added to it with a `fmt.Fprintf` per note, which is
  a reflective call per note; on 10,000 notes that was about 7 ms on every
  refresh. Search refreshes on every keystroke, which is why it is the path
  that gained most. It writes bytes now.


### Security
- **The version a server sends can no longer carry anything into the update
  dialog.** A heading of `## 9.9.9 and your vault is corrupt, see evil.example`
  parsed as 9.9.9 for the comparison but kept the whole line as the version,
  and that line goes straight into a window heading. Only the canonical number
  is kept now, so what is displayed is digits and dots by construction. Pinned
  in the fuzzer, not just a test.
- **Release notes that are not text are refused.** A body with invalid UTF-8 or
  a NUL byte was still picked over for a version heading; a binary file
  containing the bytes `## 1` produced an update offer.
- A version may have at most six numbers, so a heading of ten thousand dots
  cannot become a heading of ten thousand zeroes.


### Added
- **The phone app is a real Android app**: Kotlin and Jetpack Compose, Material
  3, in `packaging/android`. It replaces the Gio build, which drew its own
  widgets and could not reach anything Android only offers through Java — the
  system installer first among them, which is what an app nobody can update
  needs most.

  It is not a second implementation of Atlas Notes. The vault, the compressed
  Markdown, the SQLite index, the checklist model and the encryption are the
  same Go code the desktop runs, compiled for Android by `gomobile` into an
  `.aar` the Kotlin app links against. Two implementations of a vault format
  are two chances to disagree about it, and the one that disagrees about
  encryption loses notes.

  The interface is a list of notes and one of them open, because that is what a
  phone screen has room for. Locked notes ask for the password and then open;
  setting one for the first time asks twice, since it cannot be recovered and a
  typo would encrypt a note against a string nobody knows. Checklist items are
  real checkboxes, which is much easier than putting an `x` between two brackets
  with a touch keyboard, and they edit the same Markdown the desktop reads.

  Light and dark follow the system. Both schemes are built around the accent the
  desktop uses rather than the phone's wallpaper, so it looks like the same
  application rather than a relative of it.

- **`mobile/`, the Go core as Android calls it.** An ordinary Go package with no
  build tags, so it compiles and its tests run on every machine: a change that
  breaks the phone app now fails on a laptop rather than on a phone. It is
  covered by tests for the write/read round trip, the checklist line mapping, a
  locked note refusing to open without the password and opening with it, and
  every call made before the vault is open failing instead of crashing.

- Tests for the update check against a hostile server: a body that never ends,
  a single line longer than the read limit, a server that accepts the
  connection and never answers, an endless redirect, binary and HTML bodies,
  and versions built to be rendered rather than read.
- `TestConcurrentLockingIsSafe`, which drives saving, reading, locking,
  unlocking and dropping the key from many goroutines at once. The two mutexes
  are taken in both orders across the package, which is only safe because the
  key accessor releases one before it returns; this holds that property in
  place under `-race`.
- `TestScanDoesNotReadNotes`, which makes the notes unreadable and then scans.
  A scan that opens them fails. It is how the launch path is kept honest.

- **The Android build is signed with a fixed key**, held in the repository's
  secrets and read by Gradle from the environment. Android refuses to replace
  an app with one signed by a different key, so the key is what makes one build
  an update to the last rather than a different app wearing its name. The key
  is the same one the other Atlas apps use, under the same secret names, so one
  certificate covers them.

  A build without the secrets still works and still installs, signed with the
  debug key; it just cannot update a release-signed one, so a fork or a local
  build is not broken by it. Every release prints the certificate's SHA-256
  digest in its log, so "this release can update the last" is something the
  build shows rather than asserts.

- **The phone build checks for updates**, using the same check as the desktop:
  one anonymous read of a text file from this repository, with nothing about
  the device or its notes sent, and silence when there is nothing new or the
  network is not there. What it finds appears as a dismissible strip above the
  note list with the first few changes.

  It installs it, too. Android installs packages through the system installer
  and will not let an application hand itself a new version without going
  through it, so the app downloads the release and hands it over: the installer
  shows what it is about to do, and refuses a build signed with a different key
  from the one already on the phone. A file altered in transit is rejected by
  Android rather than by Atlas Notes, which is the right place for that
  decision.

- **Atlas Notes runs on Windows.** The same GTK interface, built on a Windows
  runner under MSYS2 rather than cross-compiled: MSYS2 packages GTK4 and
  libadwaita for Windows and Linux distributions package neither for mingw, so
  cross-compiling would mean assembling and maintaining that sysroot by hand.
  The release job bundles the DLLs, the compiled schemas, the icon themes and
  the pixbuf loaders with the executable, because a GTK application on Windows
  does not run from its executable alone, and starts it once on the runner to
  catch the packaging mistake that produces an executable which opens nothing.
- **Atlas Notes runs on Android**, as a separate interface over the same core.
  GTK has no Android backend — its backends are Broadway, Wayland and X11 — so
  the phone interface is its own, written in Kotlin (see above). It reads the
  same vault, the same compressed Markdown, the same SQLite index and the same
  encrypted notes: a vault copied between a laptop and a phone opens on both.

  What is there: the note list with search, notes and checklists drawn with
  their own icons, an editor that saves as you type, checklist items as real
  checkboxes with a thumb-sized target, and the password prompt for protected
  notes. The system back gesture leaves the note rather than the application.

  What is not: the assistant. It needs a model running on the same machine,
  which a phone does not have, and sending notes to a remote one would break
  the promise the app is built on. There is no network code in the phone build.

- `.github/workflows/release.yml` builds both on a tag and attaches them to the
  release, and can be run against any branch to check a build before tagging.
- `make aar` and `make apk`: the Go bindings, and the phone app built around
  them. `mobile/` has no build tags, so an ordinary `go build ./...` covers it
  on any machine.


### Changed
- **The Settings gear is back in the header.** The interface overhaul in 0.5.0
  moved Settings into the hamburger menu and deleted the button; the README went
  on telling people to click "the gear icon, top-right" for five releases after
  it stopped being there. It sits beside the menu again, with Ctrl+, in its
  tooltip, and the menu entry stays. The icon is the Material Symbols gear, run
  through `scripts/import-icons.sh` like every other one.
- **Dashes are gone from everything the app shows**: the header, the toolbar,
  Settings, the assistant panel, the update and password dialogs, the guide note
  written on first launch, the assistant's built-in prompts, and `WHATSNEW.md`,
  which the update dialog puts on screen. Sentences were rewritten rather than
  having their punctuation swapped, so none of them read like they lost a word.

  Two were left alone, because neither is prose: the unicode fixture in the
  checklist tests and the hostile version heading in the update tests both exist
  to prove those characters are handled. The divider button's fallback glyph did
  change, from an em dash to U+2500, which draws the same rule without being a
  dash.
- **The first Settings section is called "General"**, not "Model & Prompt". It
  holds the assistant's name, model and prompt, but also hover previews, the
  formatting toolbar and text rendering, none of which are either. The name had
  stopped describing the contents.

- **Where notes live is decided per platform.** Linux is untouched and still
  follows the XDG specification exactly, so no existing vault moves. Windows
  gets the vault under `%LOCALAPPDATA%` and settings under `%APPDATA%`, which
  is what those two are for; macOS gets Application Support; Android is told
  its directory by the host, because there is nothing to guess at there.
  `ATLAS_DATA_HOME` and `ATLAS_CONFIG_HOME` override any of it.
- **The two things the app asks of the operating system directly are split per
  platform**: how much processor time it has used, and how to become the newly
  installed version of itself. Unix replaces the running process so the window
  manager sees one continuous application; Windows cannot do that, and will not
  let a running executable be overwritten either, so it hands over to the new
  binary and ends.

With that, everything except the interface compiles for Windows, macOS and
Android: storage, the checklist model, the update check, the encryption and the
assistant client. `internal/editor`, `internal/ui` and `internal/app` are GTK,
and are what each platform still needs.

- **The phone app draws its own icons**, the Material Symbols from
  `assets/icons-src` that the desktop already embeds, so the two show the same
  padlock rather than two that merely resemble each other. They replace
  `material-icons-extended`, which ships every Material icon there is -- some
  two thousand -- to supply the nine this app draws, and was most of a 32 MB
  `classes.dex`. The download is about 4 MB smaller for it.

  The password field says "Show" and "Hide" rather than drawing the usual
  crossed-out eye, which is not in Material's core set and was not worth two
  thousand others.

- **The Gio phone interface is gone**, along with `internal/mobile`,
  `cmd/atlas-mobile` and the `gogio` build, replaced by the Kotlin app above.
  `gogio` signed every APK with a key it invented on the spot, so the build had
  to strip that signature and re-apply the real one afterwards; Gradle signs
  once, with the configured key, and there is nothing left to undo.

## [0.5.10] - 2026-09-24

The actual fix for the crash. v0.5.9 said it fixed this and did not.

### Fixed
- **Selecting or copying text could close the app.** Three things had to line
  up, which is why no harness ever hit it.

  The editor hides Markdown markers with GTK's `invisible` tag. Selecting with
  the mouse makes GTK hit-test the lines under the pointer, and hit-testing a
  line that carries invisible text converts a layout byte offset back into a
  buffer position. Meanwhile the editor re-tagged on every move of the insert
  mark — and dragging a selection moves it with every pixel, so a slow drag
  scheduled a re-tag every 50 ms *underneath the selection being made*. Each
  one changed which characters were invisible while GTK was measuring against
  them, and the conversion then ran off the end of the line. That is an
  error-level GLib message, which calls `abort()`, so the app vanished with no
  Go stack and the crash landing in cgo.

  The rule now is that the editor never re-tags while text is selected. The
  markers stay as they are until the selection collapses, and the pass that was
  owed runs then. Nothing is removed and nothing looks different.

  Found by elimination with the person it was happening to: `ATLAS_NO_HIDE=1`,
  which dims markers instead of hiding them, stopped the crash, and that
  narrowed it to the invisible tags. Reproducing it needed a real mouse drag,
  which is why chaos, soak, editor, resize and copy harnesses all came back
  clean against a copy of the very vault it happened on.

### Added
- `ATLAS_NO_HIDE=1` is kept and documented in the README as a fallback: it dims
  Markdown markers rather than hiding them, keeping the app out of GTK's
  invisible-text handling entirely.

### Note on v0.5.9
That release removed the assistant panel's automatic collapse on narrow
windows, on the reasoning that it was the newest code in the first build that
crashed. It was not the cause, and the crash continued. The collapse stays
removed regardless: it changed what was visible from inside a size
notification, which is its own re-entrancy problem, and it can come back later
built on `AdwBreakpoint` rather than a hand-rolled handler.

## [0.5.9] - 2026-09-24

A crash fix. v0.5.8 could abort while a note was being edited.

### Fixed
- **The app could close itself while you were typing.** GTK aborted with
  "byte index off the end of the line" from inside the text iterator, which is
  an error-level GLib message and so calls `abort()`. It appeared only on
  builds from v0.5.8 and never before it, and the byte offset differed each
  time and exceeded any line in the saved notes, which puts it in the editor
  while text was being changed rather than on opening a note.

  The assistant panel's automatic collapse on narrow windows is removed. It was
  the newest code in the build that started crashing, and it changed what was
  visible from inside a size notification — re-entering GTK's layout while it
  was in the middle of one. The same build logged "Trying to snapshot GtkGizmo
  without a current allocation", which is that class of problem. Hiding a panel
  when a window is narrow is a nicety; the app closing itself is not, so it is
  gone rather than deferred and hoped for.

  This is an honest best guess, not a confirmed repair: the crash would not
  reproduce under the chaos, soak, editor or resize harnesses, against a copy
  of the vault it happened on, under `G_DEBUG=fatal-warnings`.

### Added
- `ATLAS_DEBUG_EDITOR=1` records what the editor was doing before GTK stops
  the process: the reparse range, the caret's line, and each line's length in
  characters and bytes. A GLib error aborts, so there is no Go stack worth
  reading and the crash lands in cgo — a line from us immediately before GTK's
  own message is what turns "it keeps closing" into a note and a line number.
  Off unless the variable is set, because it runs on every keystroke.

## [0.5.8] - 2026-09-24

Acting on an outside review of the interface.

### Changed
- **The light scheme is legible.** Text was dimmed with opacity and alpha,
  which is not symmetric between the two schemes: dimming white on a dark
  background stays readable far longer than dimming black on a light one. Worse,
  it compounded, because a rule that set a recessive colour often had children
  that also set an opacity, and the two multiply. The footer's secondary text
  was the clearest case, landing around 2.3:1 against white, which fails
  WCAG AA. Dimming no longer compounds anywhere, and the floors are set for the
  light scheme rather than the dark one.
- **The formatting toolbar is visible without hovering it**, the panel dividers
  have enough weight to separate the three columns, and the selected row in the
  vault panel carries a solid accent bar rather than a wash of pale blue.
- **The footer says less.** Word count, reading time, checklist progress and
  save state. The character count moved to the word count's tooltip: a
  character count is something a form with a limit needs, not a note. The file
  path moved to the note title's tooltip, which is the thing it describes.
- **The install commands in the assistant's setup card are quieter** but still
  shown in full. What a `curl … | sh` actually runs is not something to fold
  away behind a button.

### Added
- **A checklist and a note now look different in the vault panel.** The app
  offers them as separate ways to start, so the panel draws them separately.
  Which one a note is comes from the index: the write path records it from the
  content it already holds, and the vault scan works it out only for files it
  has already decided have changed. The scan reads a note it could not
  otherwise open in exactly one case, a vault written by something other than
  the app, and it costs about 6 µs a note there. Launching is unchanged at
  92 ms on a 2,000-note vault, because the scan runs after the first frame.
- **Settings → Editor → "Show the formatting toolbar above notes"**, on by
  default. The toolbar is how someone who does not know the Markdown discovers
  what the editor understands; someone who does can have the room back.
- **The assistant panel folds away on a narrow window** (under 1,200px) and
  comes back when there is room, unless it was hidden deliberately. Hiding it
  yourself is a decision that survives resizing.

### Fixed
- **A locked note could be drawn as a checklist.** The vault scan cannot read
  one, so it records "could not tell" rather than guessing. On a fresh index
  there was no previous answer to keep, and that sentinel read back as "yes".
- **Upgrading would not have shown any checklist icons.** The scan only looks
  at files whose modification time changed, so every note already indexed would
  have kept the new column's default until it happened to be edited. Adding the
  column now clears the stored timestamps so the next scan fills it in.

## [0.5.7] - 2026-09-24

### Added
- **Password-protected notes and folders.** Right-click a note or folder in the
  vault panel and choose *Protect with Password*. One password covers
  everything protected; it is asked for once a session.

  It is encryption, not a flag. A protected note is stored as ciphertext under
  a `.md.enc` extension: unreadable by Atlas Notes without the password, and
  unreadable by `zstd -d`, a text editor, a backup tool or a sync client with
  or without it. The key is derived with Argon2id (64 MiB, t=3) and the bytes
  are sealed with XChaCha20-Poly1305, which authenticates — a tampered file
  fails to open rather than opening to garbage. The vault keeps a salt and a
  verifier beside the notes, so a vault copied to another machine still opens
  with its own password. The password itself is never stored, logged or put in
  a message.

  There is no recovery, and the dialog says so before it takes a password: a
  way back in for someone who forgot it is a way in for everyone else.

  A note's name, folder and dates stay visible, so it can be found in order to
  be unlocked; only the content is hidden, including the home screen's
  previews. Search matches names and never sees a protected body. Protecting a
  folder encrypts what is in it, and a note created in it afterwards is written
  encrypted from the start — never saved in the clear and encrypted after.
  Removing protection needs the password too, so it cannot be stripped off an
  unlocked machine.

- **Favourites.** Star a note or folder from the same menu. Favourites are a
  preference rather than vault content, so they live in `config.json` and stay
  on the machine rather than following a synced vault.

- **Lock Notes Now** (**Ctrl+Shift+L**), which forgets the password for the
  session. Without it the only way to re-lock would be to quit.

- `internal/vaultlock`, the cryptography on its own: no GTK, no files, no
  notes. It turns a password into a key and seals and opens bytes.

### Fixed
- **A row could not change once drawn.** The vault panel diffs new rows against
  the ones on screen and skips those that compare equal, and the comparison
  looked at the name, folder and modification time — so a change to anything
  else about a row updated the cache and changed nothing visible. Locking or
  starring something would have done exactly that.
- **The panel filled itself before it was told how.** `NewTree` populated its
  cache in the constructor, before the caller could say which notes are
  favourites, so the first thing drawn always said "none of them". It now fills
  itself when asked, after its callbacks are set.

### Verified
The property that matters is tested directly rather than asserted: after
locking, the note's text is not present anywhere under the vault, in any file.
The same check covers saving a locked note, creating one in a locked folder,
and deleting one. Every single-byte change to a sealed note fails to open, 200
seals under one key produce 200 distinct nonces, and two vaults with the same
password derive different keys. Tests, `go vet`, `staticcheck`, `deadcode` and
`gofmt` are clean; the race detector passes; the stability battery is 7/7; and
2,000 randomized operations against a vault containing locked notes and a
locked folder, with GLib criticals fatal, produced no warnings.

Typing, opening and startup are unchanged. An earlier reading suggested typing
had slowed by 2.5x; it had not — the benchmark's vault grows as the benchmark
types into it, and the comparison was against a smaller document. Measured
against a frozen one, keystroke latency is 0.058 ms before and 0.060 ms after.

## [0.5.6] - 2026-09-24

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
