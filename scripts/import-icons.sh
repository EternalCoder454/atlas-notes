#!/usr/bin/env bash
# Turn Material Symbols downloads into the symbolic icons Atlas Notes embeds.
#
# Google's exports are already close to what GTK wants — a single filled path,
# no strokes — but they need three things doing to them:
#
#   * Renaming. Icon lookup goes current theme -> parents -> hicolor last, and
#     an icon called "search-symbolic" would lose to the system theme's own and
#     never appear. Everything is prefixed "atlasnotes-" so it cannot collide.
#     The prefix used to be "atlas-", which kept clear of the system and not of
#     the other Atlas apps: Atlas Monitor installs atlas-menu-symbolic and four
#     more into the shared user theme, which is searched before this app's own
#     icons, so Atlas Notes was drawing Monitor's. The "-symbolic" suffix is
#     what tells GTK the icon may be recoloured.
#   * A black fill. The exports carry whatever colour the website previewed
#     with, usually a pale grey. GTK overrides it when recolouring, but anything
#     else that renders the file — a thumbnailer, a browser — would show pale
#     grey on white.
#   * A nominal 16px size, which is what these are drawn at in the toolbar. The
#     viewBox is left alone; it defines the coordinate system, not the size.
#
# One style for the whole set, the same as Atlas Monitor's so the two apps read
# as a pair: Material Symbols Outlined, weight 400, grade 0, optical size 20.
# Google's repository names that combination "<name>_20px.svg" in the
# "materialsymbolsoutlined" folder (the other weights, the filled and the
# rounded forms have longer names), and 20 is the smallest optical size, the one
# drawn for small sizes, which is what these are. Mixing in another weight or
# a filled form for one icon shows at once beside the rest, so a new icon is
# taken from that same file and nowhere else.
#
# Nothing here is borrowed from the desktop's icon theme: every icon the
# interface draws is in this list, because a theme's own arrows and crosses are
# another family at another weight, and differ from one desktop to the next.
#
# Run from the repository root:  scripts/import-icons.sh
#                                scripts/import-icons.sh --fetch
#
# Sources are read from assets/icons-src/<material name>.svg and left in place;
# the results are written to internal/app/icons/, which main.go embeds. With
# --fetch, a source that is not there yet is first downloaded from Google's
# repository, from the file described above.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")/.."
src_dir=assets/icons-src
out_dir=internal/app/icons
official=https://raw.githubusercontent.com/google/material-design-icons/master/symbols/web

fetch=0
if [ "${1:-}" = "--fetch" ]; then
    fetch=1
fi

# <Material Symbols name>:<installed name, without the atlasnotes-/-symbolic wrapper>
map=(
    # Formatting toolbar
    format_bold:bold
    format_italic:italic
    format_strikethrough:strikethrough
    code:code
    format_h1:heading1
    format_h2:heading2
    format_paragraph:paragraph
    format_list_bulleted:bullet-list
    check_box:task
    format_quote:quote
    horizontal_rule:divider

    # Window and navigation
    menu:menu
    home:home
    today:today
    settings:settings
    left_panel_open:panel-left
    right_panel_open:panel-right
    search:search
    info:info

    # Find bar
    keyboard_arrow_up:chevron-up
    keyboard_arrow_down:chevron-down
    find_replace:find-replace
    close:close

    # Vault panel
    folder:folder
    description:note
    checklist:checklist
    note_add:note-new
    add:add
    edit:edit
    delete:trash
    sort:sort
    history:recent

    # Locking and favourites
    lock:lock
    lock_open:lock-open
    star:star

    # Assistant
    chat:assistant
    quick_phrases:prompts
    arrow_upward:send
    content_copy:copy
)

fail=0
mkdir -p "$out_dir"
for pair in "${map[@]}"; do
    src="$src_dir/${pair%%:*}.svg"
    dst="$out_dir/atlasnotes-${pair##*:}-symbolic.svg"

    if [ ! -f "$src" ] && [ "$fetch" = 1 ]; then
        name="${pair%%:*}"
        if ! curl -fsS -o "$src" "$official/$name/materialsymbolsoutlined/${name}_20px.svg"; then
            rm -f "$src" # curl leaves an empty file behind when it fails
        fi
    fi

    if [ ! -f "$src" ]; then
        echo "  missing: $src"
        fail=1
        continue
    fi

    # A stroke would not recolour: GTK forces the fill on rect, circle and path,
    # so stroked artwork keeps its own colour and turns invisible in dark mode.
    # Refuse rather than ship something that looks fine until the theme changes.
    if grep -q 'stroke' "$src"; then
        echo "  REFUSED: $src contains a stroke — convert it to outlines first"
        fail=1
        continue
    fi
    # A fill="none" bounding box becomes a solid square once GTK forces the fill.
    if grep -q 'fill="none"' "$src"; then
        echo "  REFUSED: $src has a fill=\"none\" element — remove it first"
        fail=1
        continue
    fi

    python3 - "$src" "$dst" <<'PY'
import re, sys
src, dst = sys.argv[1], sys.argv[2]
svg = open(src).read().strip()
svg = re.sub(r'\s(width|height)="[^"]*"', '', svg, count=2)
svg = re.sub(r'\sfill="[^"]*"', '', svg, count=1)
svg = svg.replace('<svg ', '<svg width="16" height="16" fill="#000000" ', 1)
header = (
    '<?xml version="1.0" encoding="UTF-8"?>\n'
    '<!-- Material Symbols (Outlined, weight 400, grade 0, optical size 20),\n'
    '     Apache License 2.0 — see NOTICE. Filled paths only: GTK recolours a\n'
    '     symbolic icon by forcing the fill, so a stroke would not follow the\n'
    '     theme. Generated by scripts/import-icons.sh; edit the source file in\n'
    '     assets/icons-src rather than this. -->\n'
)
open(dst, 'w').write(header + svg + '\n')
PY
    echo "  $(basename "$src")  ->  $(basename "$dst")"
done

exit $fail
