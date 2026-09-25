# Atlas Notes

**A fast, local-first notes & checklist app for Linux — with an optional local AI
assistant.** Your notes are plain, compressed Markdown files in a folder on *your*
machine. Nothing is uploaded, nothing is tracked, and the AI runs locally too.
The one time Atlas Notes reaches the network on its own is to ask whether a
newer version has been published, which you can turn off.

Built with GTK4 + libadwaita, so it looks and feels native on GNOME (and follows
your light/dark theme). Tuned on Fedora; other distros work once the GTK4 devel
packages are installed.

<!-- Add a screenshot at docs/screenshot.png and uncomment:
![Atlas Notes](docs/screenshot.png)
-->

## Why Atlas Notes

- **Local-first & private.** Every note lives under your home directory as a
  zstd-compressed Markdown file. No account, no cloud, no telemetry. It works
  fully offline — the only request it ever makes is the update check described
  under [Updating](#updating), which sends nothing about you or your notes and
  can be switched off.
- **WYSIWYG Markdown.** Headings, **bold**, *italic*, `code`, ~~strikethrough~~,
  bullets, quotes and dividers render as you type; the syntax markers hide except
  on the line you're editing. A formatting toolbar and shortcuts (Ctrl+B, Ctrl+I,
  Ctrl+1…) do the same without typing the markers.
- **Real checklists.** `- [ ]` lines become live checkboxes with priority colors
  and per-item due dates (set from a right-click menu, shown as a badge on the
  item). The header tracks how many are done.
- **Find anything.** Search the whole vault by name from the side panel
  (**Ctrl+K**); every command has a keyboard shortcut, listed under **Ctrl+?**.
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
- **Three-panel layout** — vault · editor · AI assistant — each panel
  drag-resizable and collapsible from the header bar, with a home screen that
  puts new notes, search and your recent work one click away.

## Install on Fedora

One command installs the build dependencies, fetches the source, builds, and adds
Atlas Notes to your app grid (it will ask for your `sudo` password for the
dependencies):

```bash
curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install-fedora.sh | bash
```

Prefer to read it first? Clone and run it locally:

```bash
git clone https://github.com/EternalCoder454/atlas-notes.git
bash atlas-notes/scripts/install-fedora.sh
```

The script:

1. `sudo dnf install`s `golang gtk4-devel libadwaita-devel gcc pkgconf-pkg-config git make`.
2. Clones the source into `~/.local/share/atlas-notes/src` (the same place the
   in-app updater uses).
3. Runs `make install` → installs the binary, `.desktop` entry, and icon under
   `~/.local`.
4. Installs Ollama and pulls the default model `qwen3.5:9b` (Q4_K_M, ~5.5 GB).

Then launch **Atlas Notes** from the Activities/Super menu, or run `atlas-notes`.

Useful toggles: `SKIP_OLLAMA=1` (don't touch Ollama), `SKIP_DEPS=1` (deps already
installed), `ATLAS_NOTES_BRANCH=<name>`. Re-running the script updates an existing
install.

> Make sure `~/.local/bin` is on your `PATH` — the script warns if it isn't.
>
> *Other distros are coming. For now, install the GTK4/libadwaita devel packages
> with your package manager, then run the script with `SKIP_DEPS=1`, or [build from
> source](#building-from-source).*

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
the password, and it cannot be read by anything else either — `zstd -d`, a text
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
| `~/.local/share/atlas-notes/vault/` | your notes, one `<name>.md.zst` per note |
| `~/.local/share/atlas-notes/index.db` | SQLite index (notes + checklist items) |
| `~/.local/share/atlas-notes/src/` | source checkout used by the in-app updater |
| `~/.config/atlas-notes/config.json` | settings: vault path, model, prompts, window size |

Each note's **filename is its title** — the title field above the editor renames
the file, and that name is what shows in the folder tree. Notes are
zstd-compressed and written atomically (temp file + rename), and every `.md.zst`
is self-contained: checklist metadata is stored inline as an HTML comment as well
as in the index, e.g.

```markdown
- [ ] Buy groceries <!-- priority:high due:2026-07-01 order:1 -->
```

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
- **Script:** re-run the Fedora installer — it pulls and reinstalls.
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
├── assets/                   # style.css, app icon, icons-src/ (icon sources)
├── scripts/import-icons.sh   # Material Symbols -> the icons the app embeds
├── packaging/                # .desktop entry
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
    ├── editor/   # GtkTextView WYSIWYG, checklist rows and their menu
    ├── storage/  # vault I/O, zstd, atomic writes, SQLite index, config
    ├── checklist/# pure checklist model (parse / serialize / sort)
    ├── update/   # is there a newer version? (no GTK, no install logic)
    ├── vaultlock/# Argon2id + XChaCha20-Poly1305 (no GTK, no files)
    ├── ai/       # Ollama HTTP client
    └── ui/       # vault panel (tree*.go) + assistant panel (sidebar*.go)
```

## Roadmap

- Checklist **drag-reorder** and an inline due-date label (priorities/due dates
  are set from the right-click menu today; the AI **Sort Priorities** action
  reorders).
- A **right-click context menu** in the folder tree (rename/delete/move are on the
  bottom toolbar for now).
- Packaging for **more distributions** (Flatpak / other package managers).

## License

[MIT](LICENSE) © EternalHell
