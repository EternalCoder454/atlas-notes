#!/usr/bin/env bash
# Measure Atlas Notes against a generated vault, reproducibly.
#
#   scripts/bench.sh                 10,000 notes, 7 runs of each measurement
#   scripts/bench.sh -n 30000 -r 5   a bigger vault, fewer runs
#   scripts/bench.sh --ab v0.5.8     also build v0.5.8 and compare startup
#   scripts/bench.sh --idle          include a 15 second idle measurement
#
# The vault comes from cmd/benchvault, which builds the same notes from the
# same seed every time, so a number measured today can be checked tomorrow.
# The changelog's figures were once measured against a vault made by hand and
# then lost, and could never be checked again; this is what replaces it.
#
# Four rules, each learned the hard way:
#
#   * Every window opens on a virtual display (Xvfb) this script starts and
#     stops, never on the desktop someone is using. It renders in software, so
#     its numbers are for comparing runs of this script with each other, not
#     for quoting as what the app does on real hardware.
#   * Every run is sandboxed through both ATLAS_DATA_HOME/ATLAS_CONFIG_HOME and
#     XDG_DATA_HOME/XDG_CONFIG_HOME. Builds before 0.6.0 only know the XDG pair,
#     and an A/B run that set only the ATLAS pair once sent an old build
#     straight to the real config and icon cache.
#   * A canary fingerprints the real config, index, icon stamp and vault before
#     and after, and the script fails if anything changed.
#   * The machine is printed with the numbers. The same code measured 130 ms
#     one week and 266 ms the next on the same laptop; without the machine
#     line, a number like that reads as a regression.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")/.."
repo=$PWD

notes=10000
runs=7
seed=20260927
ab_ref=""
idle=0
while [ $# -gt 0 ]; do
	case "$1" in
	-n) notes=$2; shift 2 ;;
	-r) runs=$2; shift 2 ;;
	--seed) seed=$2; shift 2 ;;
	--ab) ab_ref=$2; shift 2 ;;
	--idle) idle=1; shift ;;
	-h | --help) sed -n '2,9p' "$0"; exit 0 ;;
	*) echo "bench: unknown option $1" >&2; exit 2 ;;
	esac
done

cache=${XDG_CACHE_HOME:-$HOME/.cache}/atlas-notes-bench
work=$(mktemp -d "${TMPDIR:-/tmp}/atlas-bench.XXXXXX")
xvfb_pid=""
cleanup() {
	if [ -n "$xvfb_pid" ]; then
		kill "$xvfb_pid" 2>/dev/null || true
		wait "$xvfb_pid" 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

# The virtual display. Xvfb picks a free display number itself and writes it
# to the descriptor once it is ready, so there is no guessing and no race.
Xvfb -displayfd 3 -screen 0 1920x1080x24 -nolisten tcp 3>"$work/display" >/dev/null 2>&1 &
xvfb_pid=$!
for _ in $(seq 1 100); do
	[ -s "$work/display" ] && break
	sleep 0.05
done
if [ ! -s "$work/display" ]; then
	echo "bench: Xvfb did not start" >&2
	exit 1
fi
display=:$(tr -d '\n' <"$work/display")

# ---- the canary ----------------------------------------------------------

# canary prints a fingerprint of the real Atlas Notes state on this machine.
# It uses the real HOME, not anything this script sets.
canary() {
	local cfg=$HOME/.config/atlas-notes/config.json data=$HOME/.local/share/atlas-notes vault
	stat -c '%n %Y %s' "$cfg" "$data/index.db" "$data/icons/.stamp" 2>/dev/null || true
	vault=$(python3 -c 'import json,os,sys
try:
    v = json.load(open(sys.argv[1])).get("vault_path") or ""
except Exception:
    v = ""
print(os.path.expanduser(v) if v else sys.argv[2])' "$cfg" "$data/vault")
	find "$vault" -type f -printf '%p %T@\n' 2>/dev/null | sort | sha256sum
}
canary_before=$(canary)

# ---- the vault -------------------------------------------------------------

pristine=$cache/n$notes-s$seed
if [ ! -d "$pristine/atlas-notes/vault" ]; then
	echo "bench: generating $notes notes (seed $seed) into $pristine"
	rm -rf "$pristine"
	go run ./cmd/benchvault -out "$pristine" -n "$notes" -seed "$seed"
fi

echo "bench: building bin/atlas-notes"
if ! make -s build >/dev/null 2>"$work/build.log"; then
	cat "$work/build.log" >&2
	exit 1
fi

# sandboxed <data-home> <binary> [env...]: run one build against one copy.
sandboxed() {
	local home=$1 bin=$2
	shift 2
	local line
	line=$(env -u ATLAS_TRACE -u WAYLAND_DISPLAY DISPLAY="$display" GDK_BACKEND=x11 \
		ATLAS_DATA_HOME="$home" ATLAS_CONFIG_HOME="$home/cfg" \
		XDG_DATA_HOME="$home" XDG_CONFIG_HOME="$home/cfg" \
		ATLAS_NO_UPDATE_CHECK=1 "$@" timeout 600 "$bin" 2>/dev/null | grep '^{' | tail -1) || true
	# A run that crashes or hangs prints no report. That is worth saying out
	# loud, and not worth losing every other measurement over.
	if [ -z "$line" ]; then
		echo "bench: a run printed no report ($*)" >&2
		return 0
	fi
	echo "$line"
}

# snapshot <name> <binary>: a copy of the vault that <binary> has fully settled,
# with its index built and the after-first-frame work done and saved.
snapshot() {
	local dir=$work/snap-$1
	rm -rf "$dir"
	cp -a "$pristine" "$dir"
	sandboxed "$dir" "$2" ATLAS_BENCH=settle >/dev/null
	echo "$dir"
}

# measure <snapshot> <binary> <mode> <runs> <out.jsonl>: fresh copy every run.
measure() {
	local snap=$1 bin=$2 mode=$3 n=$4 out=$5
	: >"$out"
	for _ in $(seq 1 "$n"); do
		rm -rf "$work/run"
		cp -a "$snap" "$work/run"
		sandboxed "$work/run" "$bin" ATLAS_BENCH="$mode" >>"$out"
	done
}

bin=$repo/bin/atlas-notes
cold=$work/cold
cp -a "$pristine" "$cold"
rm -f "$cold"/atlas-notes/index.db*
warm=$(snapshot warm "$bin")

# The largest note, for typing: a long note is where editors fall over.
# Python rather than sort | head: under pipefail, head leaving early kills sort
# with SIGPIPE, and set -e then ends the script without saying why.
largest=$(python3 - "$warm/atlas-notes/vault" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
# Any format a note can be stored in: 0.8 writes plain .md by default, and
# older builds, measured with --ab, write .md.zst.
exts = (".md.zst", ".md.gz", ".md.xz", ".md")
notes = [f for f in root.rglob("*") if f.is_file() and f.name.endswith(exts)]
p = max(notes, key=lambda f: f.stat().st_size)
name = str(p.relative_to(root))
print(next(name[: -len(e)] for e in exts if name.endswith(e)))
PY
)
big=$work/big
cp -a "$warm" "$big"
python3 - "$big/cfg/atlas-notes/config.json" "$largest" <<'PY'
import json, sys
p, note = sys.argv[1], sys.argv[2]
d = json.load(open(p)); d["last_note"] = note; json.dump(d, open(p, "w"))
PY

echo "bench: measuring ($runs runs each)"
measure "$cold" "$bin" startup "$runs" "$work/startup-cold.jsonl"
measure "$warm" "$bin" startup "$runs" "$work/startup-warm.jsonl"
measure "$cold" "$bin" settle "$runs" "$work/settle.jsonl"
measure "$warm" "$bin" open=300 3 "$work/open.jsonl"
measure "$warm" "$bin" search=450 3 "$work/search.jsonl"
measure "$big" "$bin" editor=400 3 "$work/editor.jsonl"
if [ "$idle" = 1 ]; then
	measure "$warm" "$bin" idle=15 3 "$work/idle.jsonl"
fi

if [ -n "$ab_ref" ]; then
	echo "bench: building $ab_ref for comparison"
	git worktree add -q --detach "$work/ab-src" "$ab_ref"
	(cd "$work/ab-src" && go build -o "$work/ab-bin" . 2>/dev/null)
	git worktree remove --force "$work/ab-src"
	abwarm=$(snapshot ab "$work/ab-bin")
	# Interleaved, so anything drifting on the machine lands on both.
	: >"$work/ab-a.jsonl"
	: >"$work/ab-b.jsonl"
	for _ in $(seq 1 "$runs"); do
		measure "$abwarm" "$work/ab-bin" startup 1 "$work/one.jsonl"
		cat "$work/one.jsonl" >>"$work/ab-a.jsonl"
		measure "$warm" "$bin" startup 1 "$work/one.jsonl"
		cat "$work/one.jsonl" >>"$work/ab-b.jsonl"
	done
fi

if [ "$(canary)" != "$canary_before" ]; then
	echo "bench: CANARY TRIPPED: a run changed the real config, index, icons or vault" >&2
	exit 1
fi

# ---- the report ------------------------------------------------------------

gtk=$(pkg-config --modversion gtk4 2>/dev/null || echo "?")
adw=$(pkg-config --modversion libadwaita-1 2>/dev/null || echo "?")
cpu=$(sed -n '/^model name/{s/^model name\s*: //p;q}' /proc/cpuinfo)
python3 - "$work" "$notes" "$seed" "$runs" "$ab_ref" "$(git rev-parse --short HEAD)" \
	"$cpu" "$(uname -r)" "$gtk" "$adw" "Xvfb $display, software GL" "$largest" <<'PY'
import json, os, statistics as st, sys
work, notes, seed, runs, ab, head, cpu, kernel, gtk, adw, display, largest = sys.argv[1:]

def rows(name):
    p = os.path.join(work, name)
    if not os.path.exists(p):
        return []
    return [json.loads(l) for l in open(p) if l.startswith("{")]

def med(rs, f):
    v = [f(r) for r in rs]
    v = [x for x in v if x is not None]
    return st.median(v) if v else None

def lat(rs, key, q):
    return med(rs, lambda r: r.get(key, {}).get(q))

def fmt(v, unit="ms", nd=1):
    return "?" if v is None else f"{v:.{nd}f} {unit}"

print()
print(f"Atlas Notes {head}, {int(notes):,} notes (seed {seed}), median of {runs}")
print(f"{cpu}; kernel {kernel}; GTK {gtk}, libadwaita {adw}; {display}")
print()
print("| measurement | result |")
print("| --- | --- |")
cold, warm, settle = rows("startup-cold.jsonl"), rows("startup-warm.jsonl"), rows("settle.jsonl")
print(f"| first launch, no index | {fmt(med(cold, lambda r: r.get('startup_ms')))} |")
print(f"| launch | {fmt(med(warm, lambda r: r.get('startup_ms')))} |")
print(f"| memory at launch | {fmt(med(warm, lambda r: r['after']['rss_kb']/1024), 'MB', 0)} |")
print(f"| first launch, work after the first frame | {fmt(med(settle, lambda r: r.get('settle_ms')))} ({int(med(settle, lambda r: r.get('settle_notes_read')) or 0):,} notes read) |")
print(f"| longest the window stopped responding meanwhile | {fmt(med(settle, lambda r: r.get('settle_max_stall_ms')))} |")
o, s, e = rows("open.jsonl"), rows("search.jsonl"), rows("editor.jsonl")
print(f"| open a note (p50 / p95) | {fmt(lat(o,'open_ms','p50'),'',2)}/ {fmt(lat(o,'open_ms','p95'),'ms',2)} |")
print(f"| search, per keystroke (p50 / p95) | {fmt(lat(s,'search_ms','p50'),'',2)}/ {fmt(lat(s,'search_ms','p95'),'ms',2)} |")
if e:
    kb = e[0].get("editor_doc_bytes", 0) / 1024
    print(f"| typing in the largest note, {kb:.0f} KB (p50 / p95) | {fmt(lat(e,'editor_ms','p50'),'',3)}/ {fmt(lat(e,'editor_ms','p95'),'ms',3)} |")
idle = rows("idle.jsonl")
if idle:
    c = med(idle, lambda r: r["idle_delta"]["cpu_user_ms"] + r["idle_delta"]["cpu_sys_ms"])
    print(f"| CPU used idling 15 s, caret blink included | {fmt(c)} |")
a, b = rows("ab-a.jsonl"), rows("ab-b.jsonl")
if a and b:
    print()
    print(f"| launch, interleaved | {ab} | {head} |")
    print("| --- | --- | --- |")
    print(f"| startup | {fmt(med(a, lambda r: r.get('startup_ms')))} | {fmt(med(b, lambda r: r.get('startup_ms')))} |")
    print(f"| memory | {fmt(med(a, lambda r: r['after']['rss_kb']/1024), 'MB', 0)} | {fmt(med(b, lambda r: r['after']['rss_kb']/1024), 'MB', 0)} |")
print()
print(f"Typing was measured in \"{largest}\".")
PY
echo "bench: canary clean; nothing outside the sandbox was touched"
