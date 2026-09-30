#!/usr/bin/env bash
#
# Atlas Notes Fedora installer. Kept so the command in older READMEs and blog
# posts keeps working; the real installer is scripts/install.sh, which handles
# Fedora and every other distro the same way, with the same environment
# variables (SKIP_DEPS, SKIP_OLLAMA, ATLAS_NOTES_BRANCH):
#
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install-fedora.sh | bash
#
set -euo pipefail

# Run from a clone: the installer sits next to this file. Piped from curl there
# is no file, so fetch the current one. BASH_SOURCE is empty in that case.
here=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
	here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi
if [ -n "$here" ] && [ -f "$here/install.sh" ]; then
	exec bash "$here/install.sh" "$@"
fi

url="https://raw.githubusercontent.com/EternalCoder454/atlas-notes/${ATLAS_NOTES_INSTALLER_REF:-main}/scripts/install.sh"
script="$(curl -fsSL "$url")" || { echo "error: could not download $url" >&2; exit 1; }
exec bash -c "$script" install.sh "$@"
