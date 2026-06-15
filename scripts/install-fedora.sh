#!/usr/bin/env bash
#
# Atlas Notes — Fedora installer.
#
# Installs build dependencies, fetches the source into the per-user data dir,
# builds, and installs the app + desktop entry into ~/.local. Re-running it
# updates an existing install. Safe to pipe from curl:
#
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install-fedora.sh | bash
#
# Environment toggles:
#   SKIP_DEPS=1     don't run `sudo dnf install` (deps already present)
#   SKIP_OLLAMA=1   don't install Ollama or pull the default model
#   ATLAS_NOTES_BRANCH=<name>   clone/track a branch other than the default
#
set -euo pipefail

REPO_URL="${ATLAS_NOTES_REPO:-https://github.com/EternalCoder454/atlas-notes}"
BRANCH="${ATLAS_NOTES_BRANCH:-}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
SRC_DIR="$DATA_HOME/atlas-notes/src"
MODEL="qwen2.5:3b"

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "Atlas Notes is a Linux desktop app."
if [ -r /etc/os-release ] && ! grep -qi 'ID\(_LIKE\)\?=.*\(fedora\|rhel\)' /etc/os-release; then
	warn "This script targets Fedora; on another distro install the GTK4/libadwaita devel packages yourself, then re-run with SKIP_DEPS=1."
fi

# 1. Build dependencies (need root; you'll be asked for your sudo password).
if [ "${SKIP_DEPS:-0}" != "1" ]; then
	say "Installing build dependencies (sudo)…"
	sudo dnf install -y golang gtk4-devel libadwaita-devel gcc pkgconf-pkg-config git make
fi

command -v git  >/dev/null 2>&1 || die "git is required."
command -v make >/dev/null 2>&1 || die "make is required."
command -v go   >/dev/null 2>&1 || die "Go is required (1.25+). Install 'golang' or get it from https://go.dev/dl/."

# Go 1.25+ is required by the modernc.org/sqlite dependency.
GOVER="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
if [ -n "$GOVER" ]; then
	if [ "$(printf '%s\n1.25.0\n' "$GOVER" | sort -V | head -1)" != "1.25.0" ]; then
		warn "Go $GOVER detected; Atlas Notes needs 1.25+. If the build fails, install a newer Go from https://go.dev/dl/."
	fi
fi

# 2. Fetch source into the canonical location the in-app updater also uses.
if [ -d "$SRC_DIR/.git" ]; then
	say "Updating source in $SRC_DIR…"
	git -C "$SRC_DIR" fetch --all --prune
	if [ -n "$BRANCH" ]; then git -C "$SRC_DIR" checkout "$BRANCH"; fi
	git -C "$SRC_DIR" pull --ff-only
else
	say "Cloning $REPO_URL → $SRC_DIR…"
	mkdir -p "$(dirname "$SRC_DIR")"
	if [ -n "$BRANCH" ]; then
		git clone --branch "$BRANCH" "$REPO_URL" "$SRC_DIR"
	else
		git clone "$REPO_URL" "$SRC_DIR"
	fi
fi

# 3. Build + install the binary, desktop entry, and icon into ~/.local.
say "Building and installing (first build compiles gotk4 — a few minutes)…"
make -C "$SRC_DIR" install

# 4. Optional: local AI via Ollama.
if [ "${SKIP_OLLAMA:-0}" != "1" ]; then
	if ! command -v ollama >/dev/null 2>&1; then
		say "Installing Ollama (local AI runtime)…"
		curl -fsSL https://ollama.com/install.sh | sh
	fi
	if command -v ollama >/dev/null 2>&1; then
		say "Pulling the default model '$MODEL' (~2 GB; skip with SKIP_OLLAMA=1)…"
		ollama pull "$MODEL" || warn "Could not pull $MODEL — set it up later; Atlas Notes runs fine without AI."
	fi
else
	say "Skipping Ollama. The AI panel will show offline until Ollama is running."
fi

# 5. PATH check.
case ":$PATH:" in
	*":$HOME/.local/bin:"*) ;;
	*) warn "$HOME/.local/bin is not on your PATH. Add it (e.g. in ~/.bashrc):"
	   printf '       export PATH="$HOME/.local/bin:$PATH"\n' ;;
esac

say "Done! Launch Atlas Notes from the Activities/Super menu, or run: atlas-notes"
say "Update any time from Settings → App → Update & Restart, or re-run this script."
