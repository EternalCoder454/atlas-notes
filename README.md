# Atlas Notes

**A fast, local-first notes & checklist app for Linux — with an optional local AI
assistant.** Your notes are plain, compressed Markdown files in a folder on *your*
machine. Nothing is uploaded, nothing is tracked, and the AI runs locally too.

Built with GTK4 + libadwaita, so it looks and feels native on GNOME (and follows
your light/dark theme). Tuned on Fedora; other distros work once the GTK4 devel
packages are installed.

<!-- Add a screenshot at docs/screenshot.png and uncomment:
![Atlas Notes](docs/screenshot.png)
-->

## Why Atlas Notes

- **Local-first & private.** Every note lives under your home directory as a
  zstd-compressed Markdown file. No account, no cloud, no telemetry. It works
  fully offline.
- **WYSIWYG Markdown.** Headings, **bold**, *italic*, and `code` render as you
  type; the syntax markers hide except on the line you're editing.
- **Real checklists.** `- [ ]` lines become live checkboxes with priority colors
  and per-item due dates (set from a right-click menu).
- **Local AI that never blocks the UI.** Summarize, Clean & Format, re-sort a
  checklist by priority, or ask a free-form question — all via a local
  [Ollama](https://ollama.com) model, each call on a background thread. The AI is
  entirely optional; the app is great without it.
- **Snappy & stable.** Instant **Ctrl + S** plus background autosave, an indexed
  vault (embedded SQLite) for fast browsing, and atomic writes so a note is never
  half-saved.
- **Three-panel layout** — folder tree · editor · AI assistant — each panel
  drag-resizable and collapsible from the header bar.

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
4. Installs Ollama and pulls the default model `qwen2.5:3b` (~2 GB).

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
ollama pull qwen2.5:3b
```

**Make it your own.** Open **Settings** (the gear icon, top-right) → *Model &
Prompt* to switch models or edit the system prompt, and *Prompt Shortcuts* to add,
edit, or remove the AI buttons. In a prompt, `{content}` is replaced with the note
text and `{items}` with the checklist (for Sort actions). Each action has a mode:

- **Show result** — display the answer in the panel.
- **Replace note** — overwrite the note with the result (Clean & Format).
- **Sort checklist** — reorder/re-prioritize the checklist (Sort Priorities).

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

- **In-app:** Settings → **App** → **Update & Restart**. It auto-detects your
  source checkout (or clones one if missing), runs `git pull`, rebuilds,
  reinstalls, and relaunches. The same page shows where the binary and source
  live.
- **Script:** re-run the Fedora installer — it pulls and reinstalls.
- **Manual:** `git -C ~/.local/share/atlas-notes/src pull && make -C ~/.local/share/atlas-notes/src install`.

## Configuration

`~/.config/atlas-notes/config.json` is plain JSON, written with sensible defaults
on first run:

| Key | Meaning |
| --- | ------- |
| `vault_path` | directory holding your notes |
| `model` | Ollama model name (default `qwen2.5:3b`) |
| `system_prompt` | system prompt sent with every AI call |
| `actions` | the AI buttons: `[{ "name", "prompt", "mode" }]` (`mode` ∈ `show`/`replace`/`sort`) |
| `enable_tree_summaries` | show a 1-sentence AI summary when hovering a note |
| `last_note` | note reopened on launch |
| `window_width` / `window_height` | remembered window size |

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

Run the (GTK-free) test suite with `go test ./...`.

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
├── assets/                   # style.css + app icon
├── packaging/                # .desktop entry
├── scripts/install-fedora.sh # one-command Fedora install/update
└── internal/
    ├── app/      # window, three-panel layout, autosave, Ctrl+S, in-app updater
    ├── editor/   # GtkTextView WYSIWYG + checklist rendering
    ├── storage/  # vault I/O, zstd, atomic writes, SQLite index, config
    ├── checklist/# pure checklist model (parse / serialize / sort)
    ├── ai/       # Ollama HTTP client
    └── ui/       # folder tree panel + AI sidebar
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
