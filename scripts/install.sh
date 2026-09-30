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
#   install.sh --flatpak      install the Flatpak even where a native build
#                             would work
#   install.sh --branch NAME  track a branch other than the default
#   install.sh --help
#
# Environment toggles:
#   SKIP_DEPS=1     don't install packages (deps already present)
#   SKIP_OLLAMA=1   don't install Ollama or pull the default model
#   ATLAS_NOTES_BRANCH=<name>   same as --branch
#   ATLAS_NOTES_REPO=<url|path> clone from somewhere other than GitHub
#   ATLAS_NOTES_FLATPAK=1       same as --flatpak
#   ATLAS_NOTES_FLATPAK_BUNDLE=<url>  install this bundle, not the latest
#                               release's (file:// works, for testing a build)
#   PREFIX=<dir>    install root (default ~/.local)
#
# A native build needs GLib 2.88, GTK 4.22 and libadwaita 1.9 or newer (see
# MIN_* below). Where the distro offers older ones (Debian 13, Ubuntu 24.04,
# Linux Mint 22), the script installs the Flatpak from the latest release
# instead, which brings those libraries with it; --update and --uninstall then
# act on the Flatpak.
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

# Temporary directories are removed on every way out, including die, so a failed
# download never leaves a few hundred MB in /tmp. make_tmp sets TMP rather than
# printing it: a command substitution would record the directory in a subshell
# and the exit trap would never see it.
TMP_DIRS=()
TMP=""
make_tmp() {
	TMP="$(mktemp -d)"
	TMP_DIRS+=("$TMP")
}
cleanup_tmp() {
	local d
	for d in ${TMP_DIRS[@]+"${TMP_DIRS[@]}"}; do
		rm -rf "$d"
	done
}

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

# missing_pkgs prints the packages from PKGS that are not installed yet. Asking
# first means nothing is run as root, and no system upgrade is started, when the
# build dependencies are already there.
missing_pkgs() {
	local p out=""
	# PKGS is deliberately unquoted: it is a list of words.
	# shellcheck disable=SC2086
	case "$PM" in
		pacman) out="$(pacman -T $PKGS 2>/dev/null | tr '\n' ' ' || true)" ;;
		dnf|zypper)
			for p in $PKGS; do rpm -q "$p" >/dev/null 2>&1 || out="$out $p"; done ;;
		apt)
			for p in $PKGS; do
				[ "$(dpkg-query -W -f='${db:Status-Status}' "$p" 2>/dev/null || true)" = installed ] || out="$out $p"
			done ;;
	esac
	# shellcheck disable=SC2086
	echo $out
}

# FALLBACK_FLATPAK is set when installing the dependencies showed that this
# distro's libadwaita is too old, so there is no point installing the rest.
FALLBACK_FLATPAK=0

install_deps() {
	if [ "${SKIP_DEPS:-0}" = "1" ]; then
		return
	fi
	if [ -z "$PM" ]; then
		print_manual_deps
		warn "Continuing as if SKIP_DEPS=1 was set."
		return
	fi
	local missing
	missing="$(missing_pkgs)"
	if [ -z "$missing" ]; then
		say "The build dependencies are already installed."
		return
	fi
	local sudo_cmd=""
	if [ "$(id -u)" -ne 0 ]; then
		have sudo || die "Installing packages needs root and sudo isn't installed. As root, run: $(dep_command). Then re-run this with SKIP_DEPS=1."
		sudo_cmd="sudo"
		say "Installing build dependencies with $PM (sudo may ask for your password): $missing"
	else
		say "Installing build dependencies with $PM: $missing"
	fi
	# missing is deliberately unquoted: it is a list of words.
	# shellcheck disable=SC2086
	case "$PM" in
		dnf)    $sudo_cmd dnf install -y $missing ;;
		apt)    $sudo_cmd apt-get update
		        # The package lists were only just fetched, so this is the first
		        # trustworthy answer to "which libadwaita would I get?".
		        local off
		        off="$(offered_adw)"
		        if [ -n "$off" ] && ! version_ge "$off" "$MIN_ADW"; then
			        FALLBACK_FLATPAK=1
			        return
		        fi
		        $sudo_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y $missing ;;
		# Only what is missing, and never -y on a full upgrade: on Arch a partial
		# upgrade is unsupported, and upgrading the whole system is the user's call.
		pacman) $sudo_cmd pacman -S --needed --noconfirm $missing ||
		        die "pacman could not install $missing. Your package databases may be out of date. Run 'sudo pacman -Syu' yourself (it upgrades the whole system, so it is left to you), then run this again." ;;
		zypper) $sudo_cmd zypper --non-interactive refresh
		        $sudo_cmd zypper --non-interactive install $missing ;;
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
	# With pipefail a failing `go version` would abort the whole script here, so
	# an unusable go is treated like a missing one.
	{ (cd / && go version 2>/dev/null) | sed -n 's/^go version go1\.\([0-9]\{1,\}\).*/\1/p'; } || true
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
	make_tmp
	tmp="$TMP"
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
		die "The Go download does not match the checksum go.dev publishes ($sum). Not using it."
	fi
	rm -rf "${GO_DIR:?}.new"
	mkdir -p "$GO_DIR.new"
	tar -xzf "$tmp/$file" -C "$GO_DIR.new" --strip-components=1
	rm -rf "${GO_DIR:?}"
	mv "$GO_DIR.new" "$GO_DIR"
	export PATH="$GO_DIR/bin:$PATH"
	export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
	say "Go $(cd / && go version | cut -d' ' -f3) installed in $GO_DIR (only Atlas Notes uses it)."
}

# LIB_ERR says why check_libs returned 1: the libraries are there but too old.
# main decides what to do about it, because a too-old distro can use the Flatpak
# while a missing development package is something to install.
LIB_ERR=""

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
		LIB_ERR="$DISTRO ships GLib $glib, and Atlas Notes needs $MIN_GLIB or newer (with GTK $MIN_GTK and libadwaita $MIN_ADW; you have $gtk and $adw)."
		return 1
	fi
	if ! version_ge "$adw" "$MIN_ADW"; then
		LIB_ERR="$DISTRO ships libadwaita $adw, and Atlas Notes needs $MIN_ADW or newer (GTK $MIN_GTK or newer; you have $gtk)."
		return 1
	fi
	if ! version_ge "$gtk" "$MIN_GTK"; then
		LIB_ERR="$DISTRO ships GTK $gtk, and Atlas Notes needs $MIN_GTK or newer."
		return 1
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

# ---- flatpak --------------------------------------------------------------

FLATPAK_ID="io.github.atlasnotes"
FLATPAK_CHANGED=0
RELEASES_API="https://api.github.com/repos/EternalCoder454/atlas-notes/releases/latest"

# offered_adw prints the libadwaita version the package manager would install,
# or nothing when it cannot tell (package lists not fetched yet, say), in which
# case the native build is tried and check_libs has the last word.
offered_adw() {
	local v=""
	case "$PM" in
		apt)    v="$(apt-cache policy libadwaita-1-dev 2>/dev/null | awk '/Candidate:/ {print $2; exit}')" ;;
		dnf)    v="$(dnf -q repoquery --latest-limit=1 --qf '%{version}\n' libadwaita-devel 2>/dev/null | head -n1)" ;;
		pacman) v="$(pacman -Si libadwaita 2>/dev/null | awk -F': *' '/^Version/ {print $2; exit}')" ;;
		zypper) v="$(zypper --non-interactive info libadwaita-devel 2>/dev/null | awk -F': *' '/^Version/ {print $2; exit}')" ;;
	esac
	v="${v#*:}" # an epoch ("1:1.5.0") is not part of the version
	case "$v" in ""|"(none)") return ;; esac
	printf '%s' "$v"
}

# wants_flatpak decides, before anything is installed, whether this machine can
# build natively. Asking first matters: installing a page of development packages
# only to find they are too old would leave them behind for nothing.
wants_flatpak() {
	[ "${ATLAS_NOTES_FLATPAK:-0}" = "1" ] && return 0
	local have_adw offered
	have_adw="$(pkg-config --modversion libadwaita-1 2>/dev/null || true)"
	if [ -n "$have_adw" ]; then
		if version_ge "$have_adw" "$MIN_ADW"; then return 1; fi
		return 0
	fi
	offered="$(offered_adw)"
	[ -n "$offered" ] && ! version_ge "$offered" "$MIN_ADW"
}

flatpak_installed() {
	have flatpak && flatpak info --user "$FLATPAK_ID" >/dev/null 2>&1
}

native_installed() {
	[ -e "$PREFIX/bin/atlas-notes" ] || [ -d "$SRC_DIR" ]
}

# Where a Flatpak keeps its notes and settings.
FP_ROOT="$HOME/.var/app/$FLATPAK_ID"
FP_DATA="$FP_ROOT/data/atlas-notes"
FP_CONFIG="$FP_ROOT/config/atlas-notes"

ensure_flatpak() {
	have flatpak && return
	[ -n "$PM" ] || die "Flatpak is not installed. Install it with your package manager (see https://flatpak.org/setup/), then run this again."
	local sudo_cmd=""
	if [ "$(id -u)" -ne 0 ]; then
		have sudo || die "Installing Flatpak needs root and sudo isn't installed. As root, install the flatpak package, then run this again."
		sudo_cmd="sudo"
	fi
	say "Installing Flatpak with $PM (sudo may ask for your password)..."
	case "$PM" in
		dnf)    $sudo_cmd dnf install -y flatpak ;;
		apt)    $sudo_cmd apt-get update
		        $sudo_cmd env DEBIAN_FRONTEND=noninteractive apt-get install -y flatpak ;;
		pacman) $sudo_cmd pacman -S --needed --noconfirm flatpak ;;
		zypper) $sudo_cmd zypper --non-interactive install flatpak ;;
	esac </dev/null
	have flatpak || die "Flatpak still isn't available after installing it."
}

GITHUB_URL="https://github.com/EternalCoder454/atlas-notes"
BUNDLE_URL=""

# find_bundle sets BUNDLE_URL to the .flatpak attached to the latest release. A
# network or rate-limit failure, a release with no Flatpak, and a release that is
# still being built are different problems with different fixes, so they are told
# apart.
find_bundle() {
	BUNDLE_URL="${ATLAS_NOTES_FLATPAK_BUNDLE:-}"
	[ -z "$BUNDLE_URL" ] || return 0
	make_tmp
	local json="$TMP/release.json" code tag guess
	code="$(curl -sS -o "$json" -w '%{http_code}' -H 'Accept: application/vnd.github+json' "$RELEASES_API")" ||
		die "Could not reach api.github.com to find the latest release. Check your network and try again in a while."
	case "$code" in
		200) ;;
		403|429) die "GitHub refused the request (HTTP $code), most likely its rate limit for your network address. Try again in an hour, or set ATLAS_NOTES_FLATPAK_BUNDLE to the .flatpak address from $GITHUB_URL/releases." ;;
		*) die "GitHub answered HTTP $code when asked for the latest release. Try again in a while." ;;
	esac
	BUNDLE_URL="$( { grep -o '"browser_download_url": *"[^"]*\.flatpak"' "$json" || true; } |
		head -n1 | sed 's/.*"\(https:[^"]*\)"$/\1/')"
	[ -z "$BUNDLE_URL" ] || return 0

	# No asset listed. The release may list its files late, so try the address the
	# file would have before giving up.
	tag="$(sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' "$json" | head -n1)"
	[ -n "$tag" ] || die "The latest release has no Flatpak attached. See $GITHUB_URL/releases, or build it: packaging/flatpak/README.md"
	guess="$GITHUB_URL/releases/latest/download/atlas-notes-$tag.flatpak"
	if curl -fsL -r 0-0 -o /dev/null "$guess"; then
		BUNDLE_URL="$guess"
		return 0
	fi
	die "Release $tag has no Flatpak yet. If it was published in the last few minutes it may still be building; try again in a while. Otherwise see $GITHUB_URL/releases."
}

# flatpak_install installs the latest release's bundle, or replaces an older
# one with it. There is no hosted Flatpak repository, so updating a bundle
# means installing the newer bundle over it; the sandbox's data is kept.
flatpak_install() {
	# First, before Flathub is added or anything is downloaded: the bundle is
	# built for x86_64 only.
	[ "$(uname -m)" = "x86_64" ] ||
		die "The Flatpak is built for x86_64 only, and this machine is $(uname -m), so it can't be used here. Build from source on a distro with GLib $MIN_GLIB, GTK $MIN_GTK and libadwaita $MIN_ADW."
	have curl || die "curl is required."
	ensure_flatpak
	say "Adding Flathub, where the GNOME runtime the bundle runs on comes from..."
	flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo

	local version installed=""
	find_bundle
	version="$(basename "$BUNDLE_URL" .flatpak)"
	version="${version#atlas-notes-}"
	if flatpak_installed; then
		installed="$(flatpak info --user "$FLATPAK_ID" 2>/dev/null | awk -F': *' '/^ *Version/ {print $2; exit}')"
		if [ -n "$installed" ] && [ "$installed" = "$version" ]; then
			say "Atlas Notes $version is already installed and up to date."
			FLATPAK_CHANGED=0
			return
		fi
	fi

	make_tmp
	say "Downloading Atlas Notes $version (Flatpak)..."
	curl -fL --progress-bar -o "$TMP/atlas-notes.flatpak" "$BUNDLE_URL"
	say "Installing it (the first time also fetches the GNOME runtime, about 400 MB)..."
	flatpak install --user -y --noninteractive --reinstall --bundle "$TMP/atlas-notes.flatpak" </dev/null
	FLATPAK_CHANGED=1
}

flatpak_route() {
	flatpak_install
	say "Done! Launch Atlas Notes from your applications menu, or run: flatpak run $FLATPAK_ID"
	say "Update with: install.sh --update. Remove with: install.sh --uninstall (your notes are kept)."
}

# ---- removal --------------------------------------------------------------

# canon resolves symlinks and "..", so that a vault reached through a link is
# recognised as the same place.
canon() {
	local c
	c="$(readlink -f -- "$1" 2>/dev/null || true)"
	[ -n "$c" ] || c="$1"
	printf '%s' "${c%/}"
}

# vault_of is where the notes are: vault_path in config.json, or the default.
vault_of() { # data dir, config dir
	local v=""
	if [ -r "$2/config.json" ]; then
		v="$(sed -n 's/.*"vault_path": *"\([^"]*\)".*/\1/p' "$2/config.json" | head -n1)"
	fi
	printf '%s' "${v:-$1/vault}"
}

# PROFS lists each place this user has Atlas Notes data, as label|data|config:
# the native install's, and the Flatpak's under ~/.var/app. VAULTS holds the
# resolved vault of each. Both are set by collect_profiles.
PROFS=()
VAULTS=()
collect_profiles() {
	PROFS=()
	VAULTS=()
	if [ -d "$DATA_DIR" ] || [ -d "$CONFIG_DIR" ]; then
		PROFS+=("native|$DATA_DIR|$CONFIG_DIR")
	fi
	if [ -d "$FP_ROOT" ]; then
		PROFS+=("Flatpak|$FP_DATA|$FP_CONFIG")
	fi
	local p label data config
	for p in ${PROFS[@]+"${PROFS[@]}"}; do
		IFS='|' read -r label data config <<<"$p"
		VAULTS+=("$(canon "$(vault_of "$data" "$config")")")
	done
}

# overlaps_vault is true when a path is a vault, holds one, or is inside one.
# Removing any of those would delete notes, and the vault can be set to any
# folder (even the data dir itself), so this is checked for every removal
# rather than trusting where the vault usually is.
overlaps_vault() {
	local t v
	t="$(canon "$1")"
	for v in ${VAULTS[@]+"${VAULTS[@]}"}; do
		[ "$t" = "$v" ] && return 0
		case "$v/" in "${t%/}"/*) return 0 ;; esac
		case "$t/" in "${v%/}"/*) return 0 ;; esac
	done
	return 1
}

# REMOVED collects what remove_path actually deleted, so the summary never
# claims more than was done.
REMOVED=()
remove_path() {
	local t="$1"
	[ -e "$t" ] || [ -L "$t" ] || return 0
	if overlaps_vault "$t"; then
		warn "Keeping $t: your notes are in it, or it is inside them."
		return 0
	fi
	# Some caches are read-only by design.
	[ -d "$t" ] && [ ! -L "$t" ] && chmod -R u+w "$t" 2>/dev/null || true
	rm -rf -- "$t"
	REMOVED+=("$t")
}

print_removed() {
	local t
	if [ "${#REMOVED[@]}" -gt 0 ]; then
		say "Removed:"
		for t in "${REMOVED[@]}"; do printf '       %s\n' "$t"; done
	fi
}

print_kept() {
	local p label data config
	[ "${#PROFS[@]}" -gt 0 ] || return 0
	say "Your notes and settings were left alone:"
	for p in "${PROFS[@]}"; do
		IFS='|' read -r label data config <<<"$p"
		printf '     %s:\n       settings:    %s\n       notes:       %s\n       history, search index: %s\n' \
			"$label" "$config" "$(vault_of "$data" "$config")" "$data"
	done
	printf '     Delete them yourself, or run: install.sh --purge\n'
}

# uninstall_app removes what the installer put there, and nothing else.
uninstall_app() {
	local apps="$PREFIX/share/applications" icons="$PREFIX/share/icons/hicolor" f
	REMOVED=()
	for f in "$PREFIX/bin/atlas-notes" "$apps/io.github.atlasnotes.desktop" \
		"$apps/atlas-notes.desktop" "$icons/scalable/apps/atlas-notes.svg"; do
		remove_path "$f"
	done
	if [ "${#REMOVED[@]}" -gt 0 ]; then
		update-desktop-database "$apps" 2>/dev/null || true
		gtk-update-icon-cache -f -t "$icons" 2>/dev/null || true
	fi
	remove_path "$SRC_DIR"
	remove_path "$CACHE_DIR"
	remove_path "$GO_DIR"
	# Only if nothing is left in it: the vault lives here by default.
	rmdir "$DATA_DIR" 2>/dev/null || true
	print_removed
}

remove_flatpak() {
	say "Removing the Atlas Notes Flatpak..."
	flatpak uninstall --user -y --noninteractive "$FLATPAK_ID" </dev/null
}

confirm() {
	# $1 prompt, $2 the exact answer that counts.
	local answer
	printf '%s ' "$1" >&2
	read -r answer || return 1
	[ "$answer" = "$2" ]
}

# purge_profile offers to delete one profile's settings, then its notes.
purge_profile() { # label, data dir, config dir
	local label="$1" data="$2" config="$3" vault t
	vault="$(vault_of "$data" "$config")"

	printf '\n' >&2
	say "$label settings and search index:"
	printf '       %s\n       %s\n' "$config" "$data/index.db" >&2
	if confirm "Delete them? Type 'yes' to continue, anything else keeps them:" yes; then
		REMOVED=()
		remove_path "$config"
		for t in "$data/index.db" "$data/index.db-wal" "$data/index.db-shm" "$data/index.db-journal" "$data/icons"; do
			remove_path "$t"
		done
		print_removed
		[ "${#REMOVED[@]}" -gt 0 ] || say "There was nothing to remove."
	else
		say "Kept settings and the search index."
	fi

	printf '\n' >&2
	say "$label notes:"
	printf '       vault:   %s\n       history: %s\n' "$vault" "$data/history" >&2
	# The vault is only ever deleted where this installer knows it belongs: the
	# default place inside Atlas Notes' own folder. Any other folder might be
	# Documents, or a synced folder shared with other things.
	local delete_vault=0
	if [ "$(canon "$vault")" = "$(canon "$data/vault")" ]; then
		delete_vault=1
	else
		say "Your vault is outside Atlas Notes' own folder, so this script will not delete it. It was kept."
	fi
	if [ ! -e "$data/history" ] && { [ "$delete_vault" = 0 ] || [ ! -e "$vault" ]; }; then
		say "There is no history to delete."
		return
	fi
	if confirm "To permanently delete $([ "$delete_vault" = 1 ] && echo "your notes and their history" || echo "their history"), type exactly: delete my notes" "delete my notes"; then
		REMOVED=()
		[ "$delete_vault" = 0 ] || remove_path "$vault"
		remove_path "$data/history"
		rmdir "$data" 2>/dev/null || true
		print_removed
		[ "${#REMOVED[@]}" -gt 0 ] || say "Nothing was deleted."
	else
		say "Kept your notes and their history."
	fi
}

purge() {
	# A prompt nobody is reading must never turn into a deletion. Piped from
	# curl, stdin is the script itself and there is no one to ask.
	if [ ! -t 0 ] || [ ! -t 2 ]; then
		die "--purge asks for confirmation, so it needs a terminal. Download the script and run it directly: bash install.sh --purge"
	fi
	[ -n "$DATA_DIR" ] && [ "$DATA_DIR" != "/" ] && [ "$DATA_DIR" != "$HOME" ] || die "Refusing to purge: bad data directory '$DATA_DIR'."

	collect_profiles
	if flatpak_installed; then
		remove_flatpak
	fi
	uninstall_app

	local p label data config
	if [ "${#PROFS[@]}" -eq 0 ]; then
		say "No Atlas Notes settings or notes were found."
		return
	fi
	for p in "${PROFS[@]}"; do
		IFS='|' read -r label data config <<<"$p"
		purge_profile "$label" "$data" "$config"
	done
}

# ---- main -----------------------------------------------------------------

main() {
	local action=install
	trap cleanup_tmp EXIT
	while [ $# -gt 0 ]; do
		case "$1" in
			install)      action=install ;;
			--update|update) action=update ;;
			--uninstall|uninstall) action=uninstall ;;
			--purge|purge) action=purge ;;
			--flatpak)    ATLAS_NOTES_FLATPAK=1 ;;
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
			collect_profiles
			local had_flatpak=0
			if flatpak_installed; then
				remove_flatpak
				had_flatpak=1
			fi
			uninstall_app
			if [ "${#REMOVED[@]}" -gt 0 ] || [ "$had_flatpak" = 1 ]; then
				print_kept
				say "Atlas Notes is removed."
			else
				say "Nothing to remove: no Atlas Notes files found under $PREFIX or $DATA_DIR, and no Flatpak."
				say "If you installed it from a package, use the package manager instead."
				print_kept
			fi
			return ;;
		purge)
			purge
			return ;;
	esac

	detect_distro
	say "Detected $DISTRO${PM:+ (package manager: $PM)}."

	# A Flatpak install stays one: updating it means the newer bundle.
	if [ "$action" = update ] && flatpak_installed && ! native_installed; then
		flatpak_install
		[ "$FLATPAK_CHANGED" = 1 ] && say "Updated. Restart Atlas Notes to use the new version."
		return
	fi
	if [ "$action" = install ] && wants_flatpak; then
		if [ "${ATLAS_NOTES_FLATPAK:-0}" != "1" ]; then
			say "$DISTRO offers libadwaita older than $MIN_ADW, which Atlas Notes needs to build. Installing the Flatpak instead, which brings its own."
		fi
		flatpak_route
		return
	fi

	if [ "$action" = update ]; then
		# Dependencies were installed the first time, and the updater has no
		# business asking for a password.
		SKIP_DEPS=1
		SKIP_OLLAMA=1
	fi
	install_deps
	if [ "$FALLBACK_FLATPAK" = 1 ]; then
		say "$DISTRO offers libadwaita older than $MIN_ADW, which Atlas Notes needs to build. Installing the Flatpak instead, which brings its own."
		flatpak_route
		return
	fi

	have git  || die "git is required."
	have make || die "make is required."
	have gcc || have cc || have clang || die "A C compiler is required (gcc or clang)."
	if ! check_libs; then
		# Too old to build, before anything was compiled. A fresh install has
		# the Flatpak to fall back on; an update of a native build does not.
		if [ "$action" = install ]; then
			warn "$LIB_ERR"
			say "Installing the Flatpak instead, which brings its own libraries."
			flatpak_route
			return
		fi
		die "$LIB_ERR Run the installer with --flatpak to switch to the Flatpak."
	fi
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

main "$@"
