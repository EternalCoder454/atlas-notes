# Atlas Notes

**A fast, local-first notes & checklist app for Linux — with an optional local AI
assistant.** Your notes are plain Markdown files in a folder on *your*
machine. Nothing is uploaded, nothing is tracked, and the AI runs locally too.
The one time Atlas Notes reaches the network on its own is to ask whether a
newer version has been published, which you can turn off.

Built with GTK4 + libadwaita, so it looks and feels native on GNOME (and follows
your light/dark theme). Installs with one command on Fedora, Debian,
Ubuntu, Arch and openSUSE (see [Install](#install)).

<!-- Add a screenshot at docs/screenshot.png and uncomment:
![Atlas Notes](docs/screenshot.png)
-->

## Why Atlas Notes

- **Local-first & private.** Every note lives under your home directory as a
  plain Markdown file that any other app can open (or, if you prefer, a
  compressed one: see [Note files](#note-files)). No account, no cloud, no telemetry. It works
  fully offline — the only request it ever makes is the update check described
  under [Updating](#updating), which sends nothing about you or your notes and
  can be switched off.
- **Live preview Markdown.** Headings, **bold**, *italic*, `code`,
  ~~strikethrough~~, links, bullets, numbered lists, quotes, code blocks, tables
  and dividers render as you type, and a symbol shows only while the caret is in
  what it marks: the `**` of the word you are editing, the brackets of that one
  link. Enter continues a list. A formatting toolbar and shortcuts (Ctrl+B,
  Ctrl+I, Ctrl+1…) do the same without typing the markers, and the file stays
  plain Markdown.
- **Real checklists.** `- [ ]` lines become live checkboxes with priority colors
  and per-item due dates (set from a right-click menu, shown as a badge on the
  item). The header tracks how many are done.
- **Find anything.** Search the whole vault by name and by what notes say from
  the side panel (**Ctrl+K**), and find or replace inside the open note
  (**Ctrl+F**, **Ctrl+R**). Every command has a keyboard shortcut, listed under
  **Ctrl+?**.
- **Links and tags.** Write `[[Another note]]` to link to it (suggestions appear
  as you type, clicking opens it, and renaming a note updates the links to it),
  and `#tag` anywhere to tag a note; clicking a tag lists every note that has
  it. Under each note, *Linked from* lists the notes that link to it.
- **Pictures.** Paste or drop an image into a note, or use *Insert Image*
  (**Ctrl+Shift+I**). Images are stored in the vault's `attachments` folder,
  sized for a screen and stripped of location data, and a protected note's
  images are encrypted with it.
- **Due dates that remind you.** The home screen lists what is overdue, due
  today and due this week, and a desktop notification (or, on the phone, a
  morning one) says when something is due.
- **Daily notes, templates and history.** **Ctrl+D** opens today's note
  (`Daily/2026-09-29`); any note in a `Templates` folder can start a new one;
  and *Version History* (**Ctrl+Shift+H**) keeps earlier versions of each note
  on your machine, to look back at or restore.
  Notes and checklists carry different icons, and you can star either to mark
  it a favourite.
- **Password-protect what matters.** Right-click a note or folder → *Protect
  with Password*. It is encrypted on disk, not merely hidden: see
  [Password protection](#password-protection).
- **Local AI that never blocks the UI.** Summarize, Clean & Format, re-sort a
  checklist by priority, or ask a free-form question — all via a local
  [Ollama](https://ollama.com) model, each call on a background thread. The AI is
  entirely optional; the app is great without it.
- **Snappy & stable.** Instant **Ctrl + S** plus background autosave, an indexed
  vault (embedded SQLite) for fast browsing, and atomic writes so a note is never
  half-saved.
- **A Windows 11 look.** The vault, the note and the assistant, each panel
  drag-resizable and collapsible, in one frame with the note on a page of its
  own; Home and Settings in the vault panel; optional window transparency; and
  a home screen that puts new notes, what is due and your recent work one click
  away.

## Your notes on your phone

Atlas Notes does not run a sync service. A sync app does that job: keep one
folder the same on your computer and your phone with something like
[Syncthing](https://syncthing.net), then point both at it. On the computer, that
folder is your vault (`vault_path` in `config.json`); on the phone, tap the folder
button above the list and choose it. Locked notes open with the same password on
both, because the password file travels inside the vault.

## Exporting

Any note exports as a Word document, OpenDocument text, Markdown, a web page or
plain text: **Export…** in the main menu (Ctrl+Shift+E), a note's right-click menu,
or the share button in a note on the phone.

## Platforms

| | Interface | How it is built |
| --- | --- | --- |
| **Linux** | GTK4 + libadwaita | `make install`, or the Fedora script below |
| **Windows** | GTK4 + libadwaita | A `.zip` on the release page: unpack it anywhere and run `atlas-notes.exe` |
| **Android** | Kotlin + Jetpack Compose, Material 3 | An `.apk` on the release page |
| macOS | GTK4 + libadwaita | Not built yet; the code compiles for it |

Everything below the interface is shared: the same vault format, the same
Markdown files, the same SQLite index, the same encryption. A vault
copied between machines opens on any of them.

**On Android the assistant is absent.** It needs a model running on the same
machine, which a phone does not have, and sending notes to someone else's
server is the one thing this app promises not to do. The phone build has no
network code in it at all.

**The Android build is signed with a fixed key.** Android refuses to replace an
app with one signed by a different key, so the key is what makes one build an
update to the last rather than a different app wearing its name. It lives in
the repository's secrets, not in the repository: this one is public, and a
signing key in it would let anyone build something Android treats as an upgrade
to Atlas Notes. A build without the secrets still works and still installs; it
just cannot update a signed one, so a fork or a local build is not broken.

If you installed an unsigned build, Android will refuse the signed one with
*"App not installed as package conflicts with an existing package"*. Uninstall
it once and the signed builds update each other from then on.

**Windows updates itself differently.** The Linux build installs by fetching
the source and rebuilding, so it can update in place. The Windows build arrives
as a packaged binary, and Windows will not let a running executable be
replaced, so *Update & Restart* opens the downloads page instead.

### Signing the Android build

Four repository secrets, the same names and the same key as the other Atlas
apps, so one certificate covers them:

| Secret | What it is |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | the keystore, base64-encoded |
| `ANDROID_KEYSTORE_PASSWORD` | its password |
| `ANDROID_KEY_PASSWORD` | the key's password, if it differs |
| `ANDROID_KEY_ALIAS` | which key, if the store holds more than one |

Only the first two are required. Set them from the keystore itself, so the
value never appears in a shell history or a terminal:

```bash
base64 -w0 path/to/release-signing.jks | gh secret set ANDROID_KEYSTORE_BASE64
gh secret set ANDROID_KEYSTORE_PASSWORD   # prompts, does not echo
```

Every release prints the signing certificate's SHA-256 digest in its build log.
It has to be the same string every time: that is the check that this release
can update the last one, shown rather than asserted.

### Where notes live

| | Vault | Settings |
| --- | --- | --- |
| Linux | `~/.local/share/atlas-notes/` | `~/.config/atlas-notes/` |
| Windows | `%LOCALAPPDATA%\Atlas Notes\` | `%APPDATA%\Atlas Notes\` |
| macOS | `~/Library/Application Support/Atlas Notes/` | same |
| Android | the app's private directory | same |

`ATLAS_DATA_HOME` and `ATLAS_CONFIG_HOME` override both anywhere.

## Install

One command works on Fedora, Debian, Ubuntu, Linux Mint, Arch Linux, openSUSE
and any other distro that has GTK 4 and libadwaita. It installs the build
dependencies (asking for your `sudo` password), fetches the source, builds, and
adds Atlas Notes to your app grid:

```bash
curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install.sh | bash
```

Prefer to read it first? Clone and run it locally:

```bash
git clone https://github.com/EternalCoder454/atlas-notes.git
bash atlas-notes/scripts/install.sh
```

The older `scripts/install-fedora.sh` command still works; it runs the same
installer.

The installer:

1. Reads `/etc/os-release` and installs the GTK 4 and libadwaita development
   packages, a C compiler, `pkg-config`, `git` and `make` with `dnf`, `apt`,
   `pacman` or `zypper`. On any other distro it prints exactly what is needed and
   carries on as if you had set `SKIP_DEPS=1`. It uses `sudo` only when you are
   not already root.
2. Checks for Go. If there is none, or it is older than 1.21, it downloads the
   official Go from go.dev (checking its SHA-256) into
   `~/.local/share/atlas-notes/go`. A distro Go from 1.21 on is fine: it fetches
   the exact toolchain the project names by itself.
3. Checks that GTK and libadwaita are new enough and says so plainly if not.
4. Clones the source into `~/.local/share/atlas-notes/src` (the same place the
   in-app updater uses) and runs `make install`, which puts the binary, the
   `.desktop` entry and the icon under `~/.local`.
5. Installs Ollama and pulls the default model `qwen3.5:9b` (Q4_K_M, about
   5.5 GB). Skip this with `SKIP_OLLAMA=1`.

Then launch **Atlas Notes** from the Activities or Super menu, or run
`atlas-notes`. Make sure `~/.local/bin` is on your `PATH`; the installer warns
if it isn't.

The first build compiles all of `gotk4` and takes several minutes and a few GB
of RAM. Later builds are cached.

Options: `--branch NAME` (or `ATLAS_NOTES_BRANCH`) tracks a branch other than the
default, `SKIP_DEPS=1` skips the package step, `SKIP_OLLAMA=1` skips Ollama, and
`ATLAS_NOTES_REPO` clones from somewhere other than GitHub. `install.sh --help`
lists them all.

### What you need

Atlas Notes needs **GLib 2.88, GTK 4.22 and libadwaita 1.9 or newer**. The
GTK bindings it uses (gotk4 0.4.1) are generated against those releases and will
not compile against older headers. That means Fedora 44 or newer, Arch Linux
and openSUSE Tumbleweed today, and the distros that follow GNOME 50. **Debian 13
(GLib 2.84), Ubuntu 24.04 and Linux Mint 22 (libadwaita 1.5) are too old**; the
installer stops before building and says which version it found. On those, build
inside a container or toolbox with a newer distro.

### Per distro

- **Fedora, RHEL and relatives:** the one-liner uses `dnf`. To build an RPM
  yourself (or for COPR), use `packaging/atlas-notes.spec`:
  `rpmbuild -ba packaging/atlas-notes.spec`. Enable network access for the Go
  module download on COPR.
- **Debian, Ubuntu, Linux Mint:** the one-liner uses `apt` and downloads Go from
  go.dev, but only releases with GLib 2.88 or newer can build it (see above).
- **Arch Linux and Manjaro:** the one-liner uses `pacman`. To get a proper
  package that `pacman` owns, build the PKGBUILD:
  `git clone https://github.com/EternalCoder454/atlas-notes.git && cd atlas-notes/packaging && makepkg -si`.
- **openSUSE:** the one-liner uses `zypper`.
- **Anything else:** install GTK 4 and libadwaita with their development files, a
  C compiler, `pkg-config`, `git` and `make`, then run the installer with
  `SKIP_DEPS=1`.

### Updating

- **In the app:** Settings, **App**, **Update & Restart**. For a copy built by the
  installer it pulls, rebuilds, reinstalls and relaunches. For a copy a package
  manager installed (the PKGBUILD or the RPM), it does not write over
  `/usr/bin`; it shows the `pacman`, `dnf`, `apt` or `zypper` command to run
  instead.
- **Script:** `bash ~/.local/share/atlas-notes/src/scripts/install.sh --update`, or
  re-run the one-liner.
- **Package manager:** `sudo dnf upgrade atlas-notes`, `paru -Syu atlas-notes`,
  or rebuild from the newer PKGBUILD.

See [Updating](#updating) for the launch-time check and the channels.

### Removing

```bash
bash ~/.local/share/atlas-notes/src/scripts/install.sh --uninstall
```

This removes the binary, the desktop entry, the icon, the source checkout, the
Go build cache and the Go toolchain the installer downloaded (if it did). For a
package, use the package manager: `sudo pacman -R atlas-notes`,
`sudo dnf remove atlas-notes`.

**Your notes and settings stay.** Uninstalling never touches them:

- settings: `~/.config/atlas-notes`
- notes (the vault): `~/.local/share/atlas-notes/vault`, or wherever
  `vault_path` in `config.json` points
- history and the search index: `~/.local/share/atlas-notes`

To remove those as well, run `install.sh --purge` in a terminal. It asks before
deleting settings and the search index, and will not delete your notes or their
history unless you type `delete my notes` when asked. It refuses to run when
piped, and never deletes a vault that lives outside Atlas Notes' own folder.
Ollama and its models are not installed by the package and are not removed.

## Using the AI assistant

The right-hand panel talks to a local Ollama server at `http://localhost:11434`.
The status dot is **green** when Ollama is reachable and **red** when it isn't —
the rest of the app works regardless.

| Action | What it does |
| ------ | ------------ |
| **Summarize Note** | A short summary of the current note. |
| **Clean & Format** | Fixes grammar and tidies the Markdown, replacing the note's content. |
| **Sort Priorities** | Re-orders the note's checklist by urgency and assigns priorities. |
| **Ask about this note** | Free-form Q&A — the model only sees the current note. |

Set up Ollama (the installer does this for you):

```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama pull qwen3.5:9b
```

**Make it your own.** Open **Settings** (the gear icon, top-right) → *Model &
Prompt* to switch models or edit the system prompt, and *Prompt Shortcuts* to add,
edit, or remove the AI buttons. In a prompt, `{content}` is replaced with the note
text and `{items}` with the checklist (for Sort actions). Each action has a mode:

- **Show result** — display the answer in the panel.
- **Replace note** — overwrite the note with the result (Clean & Format).
- **Sort checklist** — reorder/re-prioritize the checklist (Sort Priorities).

## Password protection

Right-click a note or folder in the vault panel and choose **Protect with
Password**. The first time, you are asked to set a password for the vault; one
password covers everything you protect.

**It is encryption, not a setting.** A protected note is stored as ciphertext
under a `.md.enc` extension. Its content cannot be read by Atlas Notes without
the password, and it cannot be read by anything else either: a text
editor, a backup tool or a sync client all see random bytes. The key is derived
from your password with Argon2id and held in memory only while the app is
unlocked; the vault stores a salt and a verifier, never the password.

**There is no recovery.** If you forget the password, the notes it protects
cannot be opened again, by this app or any other. That is what makes the
protection real, and the dialog says so before it takes a password.

What stays visible: a protected note's **name, folder and dates**, so you can
find it in order to unlock it. Search matches names — it never sees the body of
a protected note. What is hidden is the content, including the previews on the
home screen.

- **Protecting a folder** encrypts every note in it, and notes you add to it
  later are encrypted from the moment they are written — never saved in the
  clear first.
- **Unlocking** asks for the password once and lasts until you quit, or until
  you choose **Lock Notes Now** (**Ctrl+Shift+L**).
- **Removing protection** needs the password too, so nobody can strip it off an
  unlocked machine.
- **Changing it** is Settings → **App** → *Change Password*, which re-encrypts
  every protected note.

The assistant never sees a note you have not unlocked — it only ever reads what
is open in the editor.

## Where your notes live

Atlas Notes follows the XDG base directories (override with `XDG_DATA_HOME` /
`XDG_CONFIG_HOME`):

| Path | Contents |
| ---- | -------- |
| `~/.local/share/atlas-notes/vault/` | your notes, one `<name>.md` per note, and `attachments/` for pictures |
| `~/.local/share/atlas-notes/index.db` | SQLite index (notes, links, tags, due items, search) |
| `~/.local/share/atlas-notes/history/` | earlier versions of notes, kept on this machine only |
| `~/.local/share/atlas-notes/src/` | source checkout used by the in-app updater |
| `~/.config/atlas-notes/config.json` | settings: vault path, model, prompts, window size |

Each note's **filename is its title** — the title field above the editor renames
the file, and that name is what shows in the folder tree. Notes are written
atomically (temp file + rename), and every note file is self-contained: checklist metadata is stored inline as an HTML comment as well
as in the index, e.g.

```markdown
- [ ] Buy groceries <!-- priority:high due:2026-07-01 order:1 -->
```

### Note files

A vault stores its notes as plain Markdown (`.md`) unless you choose otherwise
in **Settings → General → Note files**: Zstandard (`.md.zst`), Gzip (`.md.gz`)
or XZ (`.md.xz`) take less space, but only apps that decompress them can read
the notes. The choice is saved in the vault (`.atlas-vault.json`), so every
device that syncs it writes the same way, and changing it converts every note.
Vaults from before 0.8 were all `.md.zst`; the first launch of 0.8 converts
them to plain Markdown, keeping each note's modification time. Protected notes
stay encrypted (`.md.enc`) whichever you choose.

Want your notes in Documents, a synced folder, etc.? Set `"vault_path"` in
`config.json` to any directory. On first launch the vault is seeded with a
**Welcome** note that walks through the basics.

## Updating

**On launch.** A moment after the window opens, Atlas Notes checks whether a
newer version has been published on your update channel. If there is one, it
says so — **Update Found — v0.5.5** — lists what changed in a few plain lines,
and offers **Update Now** or **Update Later**. If you are up to date, offline,
or GitHub is unreachable, nothing appears at all; the check never delays the
window or interrupts what you are typing.

The check is a single anonymous read of
[`WHATSNEW.md`](WHATSNEW.md) from this repository. No identifier, no version
ping, nothing about your machine or your notes is sent. Turn it off with
Settings → **App** → **Check for updates when Atlas Notes starts** (on by
default), or set `"check_updates": false` in `config.json`.

The release notes it shows are written for people using the app. The technical
history is in [CHANGELOG.md](CHANGELOG.md).

Other ways to update:

- **In-app:** Settings → **App** → **Update & Restart**. It auto-detects your
  source checkout (or clones one if missing), runs `git pull`, rebuilds,
  reinstalls, and relaunches. The same page shows where the binary and source
  live.
- **Script:** `install.sh --update`, or re-run the [installer](#install). It pulls
  and reinstalls.
- **Package manager:** if you installed from the PKGBUILD or RPM, update through
  `pacman`, `dnf` and friends. The Update button shows the command.
- **Manual:** `git -C ~/.local/share/atlas-notes/src pull && make -C ~/.local/share/atlas-notes/src install`.

**Channels.** Settings → **App** → *Update channel* picks which branch updates
follow: **Release** (`main`, stable) or **Beta** (`beta`, newest). The launch
check follows the same channel.

## Configuration

`~/.config/atlas-notes/config.json` is plain JSON, written with sensible defaults
on first run:

| Key | Meaning |
| --- | ------- |
| `vault_path` | directory holding your notes |
| `model` | Ollama model name (default `qwen3.5:9b`, the Q4_K_M build) |
| `system_prompt` | system prompt sent with every AI call |
| `actions` | the AI buttons: `[{ "name", "prompt", "mode" }]` (`mode` ∈ `show`/`replace`/`sort`) |
| `enable_tree_summaries` | show a 1-sentence AI summary when hovering a note |
| `update_channel` | `release` (the `main` branch) or `beta` |
| `check_updates` | look for a new version on launch (default `true`) |
| `show_format_bar` | show the formatting toolbar above notes (default `true`) |
| `favourite_notes` / `favourite_folders` | starred items; a preference, so they stay on this machine |
| `font_rendering` | `auto` (default), `crisp` (hinted — sharper at 1080p) or `smooth` (GTK default — for HiDPI) |
| `last_note` | note reopened on launch |
| `window_width` / `window_height` | remembered window size |
| `left_panel_width` / `right_panel_width` | remembered panel widths |

Most of this is editable from the in-app Settings dialog.

## Building for other platforms

Release artifacts are built by `.github/workflows/release.yml` when a tag is
pushed, and can be run from the Actions tab against any branch to check a build
before tagging.

**Windows** is built on a Windows runner under MSYS2, not cross-compiled. That
is the supported way to build GTK4 and libadwaita for Windows: MSYS2 packages
both, and Linux distributions do not package libadwaita for mingw at all, so
cross-compiling would mean assembling that sysroot by hand and keeping it
working. The job bundles the DLLs, the compiled schemas, the icon themes and
the pixbuf loaders alongside the executable, because a GTK application on
Windows does not run from its executable alone.

**Android** is a real Android app: Kotlin and Jetpack Compose, in
`packaging/android`. It is not a port of the desktop interface, because GTK does
not run on Android and a scaled-down version of three panes would be worse than
the list a phone is actually good at.

What it is not is a second implementation of Atlas Notes. The vault, the
Markdown files, the SQLite index, the checklist model and the encryption
are the same Go code the desktop runs, compiled for Android by `gomobile` into
an `.aar` the Kotlin app links against. Two implementations of a vault format
are two chances to disagree about it, and the one that disagrees about
encryption loses notes.

The bridge between them is `mobile/`, which is an ordinary Go package with no
build tags: it compiles and its tests run on every machine, so a change that
breaks the phone app fails on a laptop rather than on a phone.

Building it needs the Android SDK, the NDK and Gradle:

```bash
make apk
```

`make aar` builds just the Go bindings, which is the part that needs `gomobile`:

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init
```

## Building from source

Atlas Notes uses [`gotk4`](https://github.com/diamondburned/gotk4), which binds
GTK4/libadwaita via CGO, so you need the development headers and a C toolchain.

```bash
# Fedora
sudo dnf install -y golang gtk4-devel libadwaita-devel gcc pkgconf-pkg-config git make

git clone https://github.com/EternalCoder454/atlas-notes.git
cd atlas-notes
make install   # builds bin/atlas-notes and installs to ~/.local
atlas-notes
```

- **Go 1.25+** is required (by the `modernc.org/sqlite` dependency).
- The **first build compiles all of `gotk4`** — several minutes and a chunk of
  RAM. It's cached afterward, so later builds are quick.

Make targets:

| Target | Action |
| ------ | ------ |
| `make build` | `go build -o bin/atlas-notes .` (embeds the source path for the updater) |
| `make run` | build, then launch |
| `make install` | build, then install binary + desktop entry + icon under `~/.local` |
| `make uninstall` | remove the installed files |
| `make clean` | remove `bin/` |

Run the (GTK-free) test suite with `go test ./...`, and the microbenchmarks with
`go test -run XXX -bench . ./internal/...`.

### Measuring the app itself

The binary has a built-in harness for the things a unit test can't see. It is
inert unless one of these is set:

| Variable | What it does |
| -------- | ------------ |
| `ATLAS_TRACE=1` | print startup phase timings (config, index, window, first frame) |
| `ATLAS_BENCH=startup` | print a JSON report once the window is painted, then quit |
| `ATLAS_BENCH=idle=10` | sit idle 10s, report the CPU, memory and I/O consumed |
| `ATLAS_BENCH=editor=300` | 300 type-and-render cycles on the open note, report latency |
| `ATLAS_BENCH=open=100` | open notes round-robin, report per-note latency |
| `ATLAS_BENCH=search=200` | type in the vault's search field, report per-keystroke latency |
| `ATLAS_BENCH=soak=1500` | run an editing session against the live UI, reporting memory at checkpoints |
| `ATLAS_BENCH=chaos=2000` | drive the whole UI through randomized operations, for stability testing |
| `ATLAS_PPROF=heap.out` | write a Go heap profile at the end of the run |
| `ATLAS_CPUPROF=cpu.out` | record a CPU profile for the whole run |

Runs with any of these set are not single-instance, so they neither hand off to
nor disturb a copy of Atlas Notes you already have open.
| `ATLAS_DEBUG_FONTS=1` | print the text-rendering settings and each monitor's scale |

### Atlas Notes closes itself while you select or copy text

Fixed in 0.5.10. If it ever happens again, start it with `ATLAS_NO_HIDE=1`:
Markdown markers then show dimmed instead of hiding, which keeps the app out of
the GTK code path that caused it. `ATLAS_DEBUG_EDITOR=1` records what the
editor was doing just before, which is what a bug report needs.

### Text looks soft or uneven

GTK 4 renders glyphs unhinted, which suits a HiDPI screen but looks soft at 1x —
a 1080p monitor, typically. Atlas Notes picks per display (and re-picks when you
drag the window between screens); **Settings → Text rendering** forces *Crisp* or
*Smooth* if you disagree with its choice. `ATLAS_DEBUG_FONTS=1` shows what it
decided and why.

### Optional: GPU acceleration (AMD ROCm)

Ollama uses your GPU automatically where supported. Some AMD cards need an HSA
override — e.g. the RX 7900 XTX (RDNA 3, `gfx1100`):

```bash
mkdir -p ~/.config/environment.d
echo 'HSA_OVERRIDE_GFX_VERSION=11.0.0' > ~/.config/environment.d/ollama.conf
```

If Ollama runs as a system service, set the variable there instead
(`sudo systemctl edit ollama`, add `Environment="HSA_OVERRIDE_GFX_VERSION=11.0.0"`,
then `sudo systemctl restart ollama`). Confirm with `ollama ps` (it should show
`100% GPU`).

## Project structure

```
atlas-notes/
├── main.go                   # AdwApplication entry point; embeds style.css
├── mobile/                   # the Go core as Android calls it (gomobile)
├── .github/workflows/        # the Windows .exe and the Android .apk
├── assets/                   # style.css, app icon, icons-src/ (icon sources)
├── scripts/import-icons.sh   # Material Symbols -> the icons the app embeds
├── packaging/                # .desktop entry, and android/ (the phone app)
├── scripts/install-fedora.sh # one-command Fedora install/update
├── NOTICE                    # third-party attribution (Material Symbols)
├── WHATSNEW.md               # release notes the app shows you (plain language)
├── CHANGELOG.md              # the technical history
└── internal/
    ├── app/      # window, panels, actions, settings, the in-app updater
    │   ├── app.go          # application lifecycle
    │   ├── notes.go        # the open note: loading, saving, renaming
    │   ├── center.go       # editor page: title, toolbar, status bar
    │   ├── welcome.go      # the home screen
    │   ├── update.go       # the updater: channels, build, restart
    │   ├── updatecheck.go  # the launch-time check and its dialog
    │   ├── icons/          # every icon the app draws (Material Symbols)
    │   └── bench.go        # the measurement harness (see above)
    ├── editor/   # live preview GtkTextView: checklists, links, pictures, tables, find
    ├── storage/  # vault I/O, note formats, history, attachments, SQLite index, config
    ├── markup/   # what counts as a [[link]], a #tag or an ![image] (shared)
    ├── imagefit/ # pasted images: sized, stripped of metadata, compressed
    ├── export/   # Word, OpenDocument, Markdown, HTML and text exports
    ├── checklist/# pure checklist model (parse / serialize / sort)
    ├── update/   # is there a newer version? (no GTK, no install logic)
    ├── vaultlock/# Argon2id + XChaCha20-Poly1305 (no GTK, no files)
    ├── ai/       # Ollama HTTP client
    └── ui/       # vault panel (tree*.go) + assistant panel (sidebar*.go)

packaging/android/            # the phone app: Kotlin, Jetpack Compose
└── app/src/main/
    ├── java/io/github/atlasnotes/
    │   ├── MainActivity.kt   # the whole app: a list, and one note open
    │   ├── Vault.kt          # the Go bindings, as Kotlin sees them
    │   ├── VaultModel.kt     # what is on screen, and everything that changes it
    │   ├── Updater.kt        # download a release, hand it to the installer
    │   └── ui/               # Compose: theme, list, editor, password, update
    └── res/                  # the launcher icon, themes, the backup rules
```

## Roadmap

- Checklist **drag-reorder** (the AI **Sort Priorities** action reorders today).
- Pictures on the phone: the Android app keeps a note's image lines as text for
  now.
- Packaging for **more distributions** (Flatpak / other package managers).

## License

[MIT](LICENSE) © EternalHell
