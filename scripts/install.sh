#!/usr/bin/env bash
#
# Atlas Notes installer for Linux: Fedora, Debian, Ubuntu, Linux Mint, Arch,
# openSUSE and anything else with GTK4 and libadwaita. Safe to pipe from curl:
#
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install.sh | bash
#
# It installs the build dependencies with the distro's package manager, fetches
# the source into the per-user data dir, builds, and installs the app, desktop
# entry and icon into ~/.local. Nothing else on the system is touched.
#
# Usage:
#   install.sh [install]      install, or update an existing install (default)
#   install.sh --update       pull and rebuild the existing checkout, like the
#                             in-app Update button; no dependency or Ollama step
#   install.sh --uninstall    remove the app, its source checkout, private
#                             build cache and Go; your notes and settings are
#                             kept. Go's shared build cache is left alone.
#   install.sh --purge        --uninstall, then optionally delete settings and
#                             the search index, and (only if you type a
#                             confirmation) your notes. Needs a terminal.
#   install.sh --branch NAME  track a branch other than the default
#   install.sh --help
#
# Environment toggles:
#   SKIP_DEPS=1     don't install packages (deps already present)
#   SKIP_OLLAMA=1   don't install Ollama or pull the default model
#   ATLAS_NOTES_BRANCH=<name>   same as --branch
#   ATLAS_NOTES_REPO=<url|path> clone from somewhere other than GitHub
#   PREFIX=<dir>    install root (default ~/.local)
#
# Needs GLib 2.88, GTK 4.22 and libadwaita 1.9 or newer (see MIN_* below); on an
# older distro it stops before building and says so.
#
# Everything lives inside main, which runs on the last line. When this script
# is piped into bash, bash reads it from the same stdin that the commands it
# starts (apt, sudo, curl) can also read; a half-read function is a syntax error
# up front, where a half-read script would have run the wrong half.

set -euo pipefail

# The oldest libraries the app builds against. They are set by gotk4 v0.4.1, not
# by the app's own code: its bindings are generated against GLib 2.88, GTK 4.22
# and libadwaita 1.9 and compile every function in them, so older headers fail
# with "could not determine what C.g_get_monotonic_time_ns refers to" (seen on
# Debian 13, GLib 2.84). Debian 13, Ubuntu 24.04 and Linux Mint 22 are too old.
MIN_GLIB="2.88"
MIN_ADW="1.9"
MIN_GTK="4.22"
# The oldest Go that can fetch the toolchain named in go.mod by itself.
MIN_GO_MINOR=21

REPO_URL="${ATLAS_NOTES_REPO:-https://github.com/EternalCoder454/atlas-notes}"
BRANCH="${ATLAS_NOTES_BRANCH:-}"
PREFIX="${PREFIX:-$HOME/.local}"
MODEL="qwen3.5:9b"

# These mirror internal/storage: ATLAS_DATA_HOME and ATLAS_CONFIG_HOME win, then
# the XDG variables, then the home directory.
if [ -n "${ATLAS_DATA_HOME:-}" ]; then
	DATA_DIR="$ATLAS_DATA_HOME/atlas-notes"
else
	DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/atlas-notes"
fi
if [ -n "${ATLAS_CONFIG_HOME:-}" ]; then
	CONFIG_DIR="$ATLAS_CONFIG_HOME/atlas-notes"
else
	CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/atlas-notes"
fi
SRC_DIR="$DATA_DIR/src"
GO_DIR="$DATA_DIR/go"
# Builds use a cache of their own, so uninstalling can remove it without touching
# the Go build cache that the user's other projects share.
CACHE_DIR="$DATA_DIR/cache"

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() {
	# Piped from curl, $0 is bash, not a file with a header to print.
	if ! { [ -r "$0" ] && grep -q '^# Usage:' "$0"; }; then
		echo "Usage: install.sh [install|--update|--uninstall|--purge] [--branch NAME]"
		return
	fi
	sed -n '2,/^# Everything lives/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

# ---- distro ---------------------------------------------------------------

DISTRO="unknown Linux"
PM=""
PKGS=""

# detect_distro reads ID and ID_LIKE, so derivatives (Mint, Pop!_OS, Manjaro,
# Nobara) are handled by whatever they are built on without listing each one.
detect_distro() {
	local id="" like="" name=""
	if [ -r /etc/os-release ]; then
		# Sourced in a subshell: os-release is shell syntax, and its variables
		# (VERSION, NAME, ...) must not leak into this script's own.
		# shellcheck source=/dev/null
		{ read -r id; read -r like; read -r name; } < <(
			. /etc/os-release
			printf '%s\n%s\n%s\n' "${ID:-}" "${ID_LIKE:-}" "${PRETTY_NAME:-${NAME:-}}")
	fi
	[ -n "$name" ] && DISTRO="$name"
	local word
	for word in $id $like; do
		case "$word" in
			fedora|rhel|centos|rocky|almalinux|nobara) PM=dnf ;;
			debian|ubuntu|linuxmint|pop|elementary|raspbian|zorin|kali) PM=apt ;;
			arch|manjaro|endeavouros|cachyos|garuda) PM=pacman ;;
			opensuse*|suse|sles|sled) PM=zypper ;;
		esac
		[ -n "$PM" ] && break
	done
	case "$PM" in
		dnf)    PKGS="golang gtk4-devel libadwaita-devel gobject-introspection-devel gcc pkgconf-pkg-config git make tar" ;;
		# Go comes from go.dev (see ensure_go), not from the distro: Debian and
		# Ubuntu ship one too old for this project's go.mod.
		apt)    PKGS="libgtk-4-dev libadwaita-1-dev libgirepository1.0-dev gcc pkg-config git make curl ca-certificates tar gzip" ;;
		pacman) PKGS="go gtk4 libadwaita gobject-introspection gcc pkgconf git make" ;;
		zypper) PKGS="go gtk4-devel libadwaita-devel gobject-introspection-devel gcc pkg-config git make curl tar gzip" ;;
	esac
}

# print_manual_deps is for distros this script has no package manager for: the
# exact things the build needs, so the user can install them and carry on.
print_manual_deps() {
	cat >&2 <<EOF
This script doesn't know the package manager of $DISTRO. Install these with
yours, then run it again with SKIP_DEPS=1:

  - GTK $MIN_GTK or newer, with its development files (headers and pkg-config file)
  - libadwaita $MIN_ADW or newer, with its development files
  - gobject-introspection, with its development files (the Go bindings ask for it)
  - a C compiler (gcc or clang)
  - pkg-config (or pkgconf)
  - git and make
  - Go $(printf '1.%s' "$MIN_GO_MINOR") or newer (optional: this script fetches it from go.dev if missing)

For reference: Fedora calls them gtk4-devel, libadwaita-devel and
gobject-introspection-devel, Debian and Ubuntu libgtk-4-dev, libadwaita-1-dev
and libgirepository1.0-dev, Arch gtk4, libadwaita and gobject-introspection.
EOF
}

install_deps() {
	if [ "${SKIP_DEPS:-0}" = "1" ]; then
		return
	fi
	if [ -z "$PM" ]; then
		print_manual_deps
		warn "Continuing as if SKIP_DEPS=1 was set."
		return
	fi
	local sudo_cmd=""
	if [ "$(id -u)" -ne 0 ]; then
		have sudo || die "Installing packages needs root and sudo isn't installed. As root, run: $(dep_command). Then re-run this with SKIP_DEPS=1."
		sudo_cmd="sudo"
		say "Installing build dependencies with $PM (sudo may ask for your password)..."
	else
		say "Installing build dependencies with $PM..."
	fi
	# PKGS is deliberately unquoted: it is a list of words.
	# shellcheck disable=SC2086
	case "$PM" in
		dnf)    $sudo_cmd dnf install -y $PKGS ;;
		apt)    $sudo_cmd apt-get update
		        $sudo_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y $PKGS ;;
		pacman) $sudo_cmd pacman -Syu --needed --noconfirm $PKGS ;;
		zypper) $sudo_cmd zypper --non-interactive refresh
		        $sudo_cmd zypper --non-interactive install $PKGS ;;
	esac </dev/null
}

dep_command() {
	case "$PM" in
		dnf)    echo "dnf install -y $PKGS" ;;
		apt)    echo "apt-get update && apt-get install -y $PKGS" ;;
		pacman) echo "pacman -S --needed $PKGS" ;;
		zypper) echo "zypper install $PKGS" ;;
	esac
}

# ---- toolchain ------------------------------------------------------------

# version_ge A B: true when dotted version A >= B.
version_ge() {
	[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]
}

# go_minor prints N for a Go 1.N toolchain, or nothing if go is missing or not 1.x.
go_minor() {
	have go || return 0
	# From a neutral directory: inside a module, `go version` can be answered by
	# a toolchain go.mod asks for, rather than the one that is installed.
	(cd / && go version 2>/dev/null) | sed -n 's/^go version go1\.\([0-9]\{1,\}\).*/\1/p'
}

# ensure_go makes sure a Go that can build this is first on PATH. An installed
# Go 1.21 or newer is enough: it fetches the exact toolchain go.mod names
# (GOTOOLCHAIN=auto). Anything older, or nothing, gets the official release from
# go.dev, checked against the SHA-256 that go.dev publishes, in a private
# directory so no system package is touched.
ensure_go() {
	# A Go we installed earlier wins over an old one from the distro.
	if [ -x "$GO_DIR/bin/go" ]; then
		export PATH="$GO_DIR/bin:$PATH"
	fi
	local minor
	minor="$(go_minor)"
	if [ -n "$minor" ] && [ "$minor" -ge "$MIN_GO_MINOR" ]; then
		# Some distros (Fedora) pin GOTOOLCHAIN=local, which would stop a Go older
		# than go.mod asks for from fetching the right one.
		export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
		return
	fi
	if [ -n "$minor" ]; then
		say "Go 1.$minor is too old to fetch the toolchain this project needs; getting Go from go.dev."
	else
		say "Go isn't installed; getting it from go.dev."
	fi
	have curl || die "curl is needed to download Go. Install it (or Go itself, from https://go.dev/dl/) and re-run."
	have tar || die "tar is needed to unpack Go."
	have sha256sum || die "sha256sum is needed to verify the Go download."

	local arch file sum tmp
	case "$(uname -m)" in
		x86_64|amd64)  arch=amd64 ;;
		aarch64|arm64) arch=arm64 ;;
		armv7l|armv6l) arch=armv6l ;;
		i?86)          arch=386 ;;
		riscv64)       arch=riscv64 ;;
		*) die "No official Go build for $(uname -m). Install Go 1.$MIN_GO_MINOR or newer from your distro and re-run." ;;
	esac
	tmp="$(mktemp -d)"
	# The JSON lists the newest stable releases first, and each archive entry has
	# its "filename" line before its "sha256" line.
	curl -fsSL 'https://go.dev/dl/?mode=json' -o "$tmp/dl.json" || die "Could not read the Go download list from go.dev."
	file="$(sed -n "s/.*\"filename\": *\"\\(go[0-9.]*\\.linux-$arch\\.tar\\.gz\\)\".*/\\1/p" "$tmp/dl.json" | head -n1)"
	[ -n "$file" ] || die "go.dev lists no Linux $arch build of Go."
	sum="$(awk -v f="$file" '
		$0 ~ "\"filename\": *\"" f "\"" { hit = 1; next }
		hit && /"sha256"/ { gsub(/.*"sha256": *"|".*/, ""); print; exit }' "$tmp/dl.json")"
	[ -n "$sum" ] || die "go.dev published no checksum for $file."
	say "Downloading $file..."
	curl -fSL "https://go.dev/dl/$file" -o "$tmp/$file"
	if [ "$(sha256sum "$tmp/$file" | cut -d' ' -f1)" != "$sum" ]; then
		rm -rf "$tmp"
		die "The Go download does not match the checksum go.dev publishes ($sum). Not using it."
	fi
	rm -rf "${GO_DIR:?}.new"
	mkdir -p "$GO_DIR.new"
	tar -xzf "$tmp/$file" -C "$GO_DIR.new" --strip-components=1
	rm -rf "$tmp" "${GO_DIR:?}"
	mv "$GO_DIR.new" "$GO_DIR"
	export PATH="$GO_DIR/bin:$PATH"
	export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
	say "Go $(cd / && go version | cut -d' ' -f3) installed in $GO_DIR (only Atlas Notes uses it)."
}

# check_libs says plainly whether GTK and libadwaita are here and new enough,
# rather than leaving the answer to a wall of C compiler errors.
check_libs() {
	have pkg-config || have pkgconf || die "pkg-config is missing. Install pkg-config (pkgconf), or re-run without SKIP_DEPS."
	local pc=pkg-config
	have pkg-config || pc=pkgconf
	local gtk adw
	gtk="$($pc --modversion gtk4 2>/dev/null || true)"
	adw="$($pc --modversion libadwaita-1 2>/dev/null || true)"
	[ -n "$gtk" ] || die "GTK 4 development files not found. $(dep_hint gtk)"
	[ -n "$adw" ] || die "libadwaita development files not found. $(dep_hint adw)"
	$pc --exists gobject-introspection-1.0 2>/dev/null || die "gobject-introspection development files not found. $(dep_hint gi)"
	local glib
	glib="$($pc --modversion glib-2.0 2>/dev/null || true)"
	say "Found GLib ${glib:-?}, GTK $gtk and libadwaita $adw."
	if [ -n "$glib" ] && ! version_ge "$glib" "$MIN_GLIB"; then
		die "$DISTRO ships GLib $glib, and Atlas Notes needs $MIN_GLIB or newer (with GTK $MIN_GTK and libadwaita $MIN_ADW; you have $gtk and $adw). Use a newer release of the distro. A Flatpak for older distros is coming."
	fi
	if ! version_ge "$adw" "$MIN_ADW"; then
		die "$DISTRO ships libadwaita $adw, and Atlas Notes needs $MIN_ADW or newer (GTK $MIN_GTK or newer; you have $gtk). Use a newer release of the distro. A Flatpak for older distros is coming."
	fi
	if ! version_ge "$gtk" "$MIN_GTK"; then
		die "$DISTRO ships GTK $gtk, and Atlas Notes needs $MIN_GTK or newer."
	fi
}

dep_hint() {
	case "$PM:$1" in
		dnf:gtk)     echo "Install gtk4-devel." ;;
		dnf:adw)     echo "Install libadwaita-devel." ;;
		dnf:gi)      echo "Install gobject-introspection-devel." ;;
		apt:gi)      echo "Install libgirepository1.0-dev." ;;
		pacman:gi)   echo "Install gobject-introspection." ;;
		zypper:gi)   echo "Install gobject-introspection-devel." ;;
		*:gi)        echo "Install the gobject-introspection development package." ;;
		apt:gtk)     echo "Install libgtk-4-dev." ;;
		apt:adw)     echo "Install libadwaita-1-dev." ;;
		pacman:gtk)  echo "Install gtk4." ;;
		pacman:adw)  echo "Install libadwaita." ;;
		zypper:gtk)  echo "Install gtk4-devel." ;;
		zypper:adw)  echo "Install libadwaita-devel." ;;
		*:gtk)       echo "Install the GTK 4 development package." ;;
		*)           echo "Install the libadwaita development package." ;;
	esac
}

# ---- source ---------------------------------------------------------------

# fetch_source puts the source in the one place the in-app updater also uses.
fetch_source() {
	if [ -d "$SRC_DIR/.git" ]; then
		say "Updating source in $SRC_DIR..."
		git -C "$SRC_DIR" fetch --prune origin
		if [ -z "$BRANCH" ]; then
			BRANCH="$(git -C "$SRC_DIR" rev-parse --abbrev-ref HEAD)"
			[ "$BRANCH" != "HEAD" ] || BRANCH="main"
		fi
		git -C "$SRC_DIR" checkout "$BRANCH"
		# Fast-forward when possible. The checkout is this script's own, never
		# somewhere to keep work, so if it has diverged it is reset to match
		# origin, as the in-app updater does.
		if ! git -C "$SRC_DIR" merge --ff-only "origin/$BRANCH"; then
			warn "The checkout had diverged from origin/$BRANCH; resetting it to match."
			git -C "$SRC_DIR" reset --hard "origin/$BRANCH"
		fi
	else
		say "Cloning $REPO_URL into $SRC_DIR..."
		mkdir -p "$(dirname "$SRC_DIR")"
		rm -rf "${SRC_DIR:?}"
		if [ -n "$BRANCH" ]; then
			git clone --branch "$BRANCH" "$REPO_URL" "$SRC_DIR"
		else
			git clone "$REPO_URL" "$SRC_DIR"
		fi
	fi
}

build_and_install() {
	say "Building and installing (the first build compiles gotk4, which takes a few minutes)..."
	make -C "$SRC_DIR" install PREFIX="$PREFIX"
	local bin="$PREFIX/bin/atlas-notes"
	[ -x "$bin" ] || die "The build finished but $bin is missing."
	# Ask the dynamic linker what it can't resolve, rather than finding out at launch.
	if have ldd; then
		local missing
		missing="$(ldd "$bin" 2>/dev/null | awk '/not found/ {print $1}' | sort -u)"
		if [ -n "$missing" ]; then
			warn "The installed binary needs libraries this system can't find:"
			printf '  %s\n' "${missing//$'\n'/ }" >&2
		fi
	fi
}

install_ollama() {
	if [ "${SKIP_OLLAMA:-0}" = "1" ]; then
		say "Skipping Ollama. The AI panel will show offline until Ollama is running."
		return
	fi
	if ! have ollama; then
		say "Installing Ollama (local AI runtime)..."
		curl -fsSL https://ollama.com/install.sh | sh || warn "Could not install Ollama. Atlas Notes runs fine without AI."
	fi
	if have ollama; then
		say "Pulling the default model '$MODEL' (about 5.5 GB; skip with SKIP_OLLAMA=1)..."
		ollama pull "$MODEL" || warn "Could not pull $MODEL. Set it up later; Atlas Notes runs fine without AI."
	fi
}

path_hint() {
	case ":$PATH:" in
		*":$PREFIX/bin:"*) ;;
		*) warn "$PREFIX/bin is not on your PATH. Add it (for example in ~/.bashrc):"
		   echo "       export PATH=\"$PREFIX/bin:\$PATH\"" ;;
	esac
}

# ---- removal --------------------------------------------------------------

# vault_path is where the notes are: vault_path in config.json, or the default.
vault_path() {
	local v=""
	if [ -r "$CONFIG_DIR/config.json" ]; then
		v="$(sed -n 's/.*"vault_path": *"\([^"]*\)".*/\1/p' "$CONFIG_DIR/config.json" | head -n1)"
	fi
	printf '%s' "${v:-$DATA_DIR/vault}"
}

print_kept() {
	say "Your notes and settings were left alone:"
	printf '       settings:    %s\n' "$CONFIG_DIR"
	printf '       notes:       %s\n' "$(vault_path)"
	printf '       history, search index: %s\n' "$DATA_DIR"
	printf '     Delete them yourself, or run: install.sh --purge\n'
}

uninstall_app() {
	say "Removing Atlas Notes from $PREFIX..."
	local apps="$PREFIX/share/applications" icons="$PREFIX/share/icons/hicolor"
	rm -f "$PREFIX/bin/atlas-notes" "$apps/io.github.atlasnotes.desktop" \
		"$apps/atlas-notes.desktop" "$icons/scalable/apps/atlas-notes.svg"
	update-desktop-database "$apps" 2>/dev/null || true
	gtk-update-icon-cache -f -t "$icons" 2>/dev/null || true

	if [ -d "$SRC_DIR" ]; then
		say "Removing the source checkout $SRC_DIR..."
		# Go's module cache is read-only by design; this one is not, but be safe.
		chmod -R u+w "$SRC_DIR" 2>/dev/null || true
		rm -rf "${SRC_DIR:?}"
	fi
	if [ -d "$CACHE_DIR" ]; then
		say "Removing the build cache $CACHE_DIR..."
		chmod -R u+w "$CACHE_DIR" 2>/dev/null || true
		rm -rf "${CACHE_DIR:?}"
	fi
	if [ -d "$GO_DIR" ]; then
		say "Removing the Go toolchain this installer downloaded ($GO_DIR)..."
		chmod -R u+w "$GO_DIR" 2>/dev/null || true
		rm -rf "${GO_DIR:?}"
	fi
	# Only if nothing is left in it: the vault lives here by default.
	rmdir "$DATA_DIR" 2>/dev/null || true
}

confirm() {
	# $1 prompt, $2 the exact answer that counts.
	local answer
	printf '%s ' "$1" >&2
	read -r answer || return 1
	[ "$answer" = "$2" ]
}

purge() {
	# A prompt nobody is reading must never turn into a deletion. Piped from
	# curl, stdin is the script itself and there is no one to ask.
	if [ ! -t 0 ] || [ ! -t 2 ]; then
		die "--purge asks for confirmation, so it needs a terminal. Download the script and run it directly: bash install.sh --purge"
	fi
	[ -n "$DATA_DIR" ] && [ "$DATA_DIR" != "/" ] && [ "$DATA_DIR" != "$HOME" ] || die "Refusing to purge: bad data directory '$DATA_DIR'."

	local vault
	vault="$(vault_path)"
	uninstall_app

	printf '\n' >&2
	say "Settings and search index:"
	printf '       %s\n       %s\n' "$CONFIG_DIR" "$DATA_DIR/index.db" >&2
	if confirm "Delete them? Type 'yes' to continue, anything else keeps them:" yes; then
		rm -rf "${CONFIG_DIR:?}"
		rm -f "$DATA_DIR/index.db" "$DATA_DIR/index.db-wal" "$DATA_DIR/index.db-shm" "$DATA_DIR/index.db-journal"
		rm -rf "${DATA_DIR:?}/icons"
		say "Settings and the search index are gone."
	else
		say "Kept settings and the search index."
	fi

	printf '\n' >&2
	say "Your notes:"
	printf '       vault:   %s\n       history: %s\n' "$vault" "$DATA_DIR/history" >&2
	if [ "$vault" != "$DATA_DIR/vault" ]; then
		say "Your vault is outside Atlas Notes' own folder, so this script will not touch it. Delete it yourself if you want it gone."
		vault=""
	fi
	if confirm "To permanently delete your notes and their history, type exactly: delete my notes" "delete my notes"; then
		[ -z "$vault" ] || rm -rf "${vault:?}"
		rm -rf "${DATA_DIR:?}/history"
		rmdir "$DATA_DIR" 2>/dev/null || true
		say "Notes and history deleted."
	else
		say "Kept your notes and their history."
	fi
}

# ---- main -----------------------------------------------------------------

main() {
	local action=install
	while [ $# -gt 0 ]; do
		case "$1" in
			install)      action=install ;;
			--update|update) action=update ;;
			--uninstall|uninstall) action=uninstall ;;
			--purge|purge) action=purge ;;
			--branch)     [ $# -ge 2 ] || die "--branch needs a name."; BRANCH="$2"; shift ;;
			--branch=*)   BRANCH="${1#--branch=}" ;;
			-h|--help)    usage; exit 0 ;;
			*)            die "Unknown option: $1 (try --help)" ;;
		esac
		shift
	done

	[ "$(uname -s)" = "Linux" ] || die "This installer is for Linux."
	[ -n "${HOME:-}" ] || die "HOME is not set."

	case "$action" in
		uninstall)
			ensure_go_quiet
			uninstall_app
			print_kept
			say "Atlas Notes is removed. If you installed it from a package, use the package manager instead."
			return ;;
		purge)
			ensure_go_quiet
			purge
			return ;;
	esac

	detect_distro
	say "Detected $DISTRO${PM:+ (package manager: $PM)}."
	if [ "$action" = update ]; then
		# Dependencies were installed the first time, and the updater has no
		# business asking for a password.
		SKIP_DEPS=1
		SKIP_OLLAMA=1
	fi
	install_deps

	have git  || die "git is required."
	have make || die "make is required."
	have gcc || have cc || have clang || die "A C compiler is required (gcc or clang)."
	check_libs
	ensure_go
	mkdir -p "$CACHE_DIR"
	export GOCACHE="$CACHE_DIR"
	fetch_source
	build_and_install
	install_ollama
	path_hint

	if [ "$action" = update ]; then
		say "Updated. Restart Atlas Notes to use the new version."
	else
		say "Done! Launch Atlas Notes from the Activities or Super menu, or run: atlas-notes"
		say "Update any time from Settings, App, Update & Restart, or with: install.sh --update"
		say "Remove it with: install.sh --uninstall (your notes are kept)."
	fi
}

# ensure_go_quiet only puts an earlier private Go on PATH, so that removing the
# cache works; it never downloads anything.
ensure_go_quiet() {
	if [ -x "$GO_DIR/bin/go" ]; then
		export PATH="$GO_DIR/bin:$PATH"
	fi
}

main "$@"
