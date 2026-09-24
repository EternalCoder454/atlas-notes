package app

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/editor"
)

// This file is the app's built-in measurement harness. It is inert unless
// ATLAS_BENCH / ATLAS_TRACE / ATLAS_SHOT are set in the environment, so it costs
// a single env lookup at startup in normal use.
//
//	ATLAS_TRACE=1        print startup phase timings to stderr, then run normally
//	ATLAS_BENCH=startup  print a JSON report once the first frame is on screen, quit
//	ATLAS_BENCH=idle=10  sit idle for 10s, report CPU/RSS/IO consumed, quit
//	ATLAS_BENCH=editor=N N type+reparse cycles on the open note, report latency, quit
//	ATLAS_BENCH=open=N   open notes round-robin N times, report latency
//	ATLAS_BENCH=soak=N   N edit/save/switch cycles with the main loop running,
//	                     reporting memory at checkpoints (leak hunting), quit
//	ATLAS_PPROF=file     write a Go heap profile when the run finishes

var (
	benchMode   = os.Getenv("ATLAS_BENCH")
	traceOn     = benchMode != "" || os.Getenv("ATLAS_TRACE") != ""
	execStart   = processStartTime()
	phaseMarks  []phaseMark
	benchReport = map[string]any{}
)

type phaseMark struct {
	Name string  `json:"name"`
	Ms   float64 `json:"ms"`
}

// mark records the elapsed time from process exec to this point in startup.
func mark(name string) {
	if !traceOn {
		return
	}
	phaseMarks = append(phaseMarks, phaseMark{name, msSince(execStart)})
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

// processStartTime derives the real exec time of this process from
// /proc/self/stat (field 22) and the boot time, so the trace includes dynamic
// linking and GTK library load — not just the Go runtime's view.
func processStartTime() time.Time {
	now := time.Now()
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return now
	}
	// Fields after the (comm) field, which may itself contain spaces.
	close := strings.LastIndex(string(stat), ")")
	if close < 0 {
		return now
	}
	fields := strings.Fields(string(stat)[close+1:])
	if len(fields) < 20 {
		return now
	}
	ticks, err := strconv.ParseFloat(fields[19], 64) // starttime, 22nd field overall
	if err != nil {
		return now
	}
	uptime, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return now
	}
	upSecs, err := strconv.ParseFloat(strings.Fields(string(uptime))[0], 64)
	if err != nil {
		return now
	}
	const hz = 100 // CONFIG_HZ on all mainstream x86-64 distro kernels
	return now.Add(-time.Duration((upSecs - ticks/hz) * float64(time.Second)))
}

// ---- resource sampling -------------------------------------------------

type sample struct {
	UserMs   float64 `json:"cpu_user_ms"`
	SysMs    float64 `json:"cpu_sys_ms"`
	RSSKB    int     `json:"rss_kb"`
	PeakKB   int     `json:"rss_peak_kb"`
	ReadKB   int     `json:"io_read_kb"`
	WriteKB  int     `json:"io_write_kb"`
	Syscr    int     `json:"io_syscall_read"`
	Syscw    int     `json:"io_syscall_write"`
	HeapKB   int     `json:"go_heap_kb"`
	GoSysKB  int     `json:"go_sys_kb"`
	NumGC    int     `json:"go_num_gc"`
	Routines int     `json:"goroutines"`
}

func takeSample() sample {
	var s sample
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.UserMs = float64(ru.Utime.Sec)*1000 + float64(ru.Utime.Usec)/1000
		s.SysMs = float64(ru.Stime.Sec)*1000 + float64(ru.Stime.Usec)/1000
	}
	for k, v := range procPairs("/proc/self/status") {
		switch k {
		case "VmRSS":
			s.RSSKB = v
		case "VmHWM":
			s.PeakKB = v
		}
	}
	for k, v := range procPairs("/proc/self/io") {
		switch k {
		case "read_bytes", "rchar":
			if k == "rchar" {
				s.ReadKB = v / 1024
			}
		case "wchar":
			s.WriteKB = v / 1024
		case "syscr":
			s.Syscr = v
		case "syscw":
			s.Syscw = v
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.HeapKB = int(m.HeapAlloc / 1024)
	s.GoSysKB = int(m.Sys / 1024)
	s.NumGC = int(m.NumGC)
	s.Routines = runtime.NumGoroutine()
	return s
}

// procPairs parses "Key: value kB"-style /proc files into a number map.
func procPairs(path string) map[string]int {
	out := map[string]int{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			out[strings.TrimSpace(k)] = n
		}
	}
	return out
}

func (s sample) sub(b sample) sample {
	return sample{
		UserMs: s.UserMs - b.UserMs, SysMs: s.SysMs - b.SysMs,
		RSSKB: s.RSSKB, PeakKB: s.PeakKB,
		ReadKB: s.ReadKB - b.ReadKB, WriteKB: s.WriteKB - b.WriteKB,
		Syscr: s.Syscr - b.Syscr, Syscw: s.Syscw - b.Syscw,
		HeapKB: s.HeapKB, GoSysKB: s.GoSysKB,
		NumGC: s.NumGC - b.NumGC, Routines: s.Routines,
	}
}

// ---- bench driver ------------------------------------------------------

// benchAfterFirstFrame installs a frame-clock callback that fires once the
// window has actually painted, which is where every measurement starts.
func (a *App) benchAfterFirstFrame(fn func()) {
	var id uint
	id = a.win.AddTickCallback(func(w gtk.Widgetter, _ gdk.FrameClocker) bool {
		a.win.RemoveTickCallback(id)
		fn()
		return false
	})
}

// runBench dispatches the ATLAS_BENCH mode once the window is painted.
func (a *App) runBench() {
	if benchMode == "" {
		return
	}
	kind, arg, _ := strings.Cut(benchMode, "=")
	n, _ := strconv.Atoi(arg)
	a.benchAfterFirstFrame(func() {
		mark("first-frame")
		switch kind {
		case "startup":
			a.emitReport()
		case "idle":
			if n <= 0 {
				n = 10
			}
			base := takeSample()
			go func() {
				time.Sleep(time.Duration(n) * time.Second)
				coreglib.IdleAdd(func() bool {
					benchReport["idle_seconds"] = n
					benchReport["idle_delta"] = takeSample().sub(base)
					a.emitReport()
					return false
				})
			}()
		case "editor":
			if n <= 0 {
				n = 200
			}
			a.benchEditor(n)
			a.emitReport()
		case "open":
			if n <= 0 {
				n = 50
			}
			a.benchOpen(n)
			a.emitReport()
		case "anchors":
			if n <= 0 {
				n = 5000
			}
			a.benchAnchors(n)
			a.emitReport()
		case "widgets":
			if n <= 0 {
				n = 20000
			}
			benchWidgets(n)
			a.emitReport()
		case "soak":
			if n <= 0 {
				n = 500
			}
			a.benchSoak(n)
		default:
			a.emitReport()
		}
	})
}

// benchEditor measures the real per-keystroke cost: insert a character (which
// runs the change handler) then the reparse the debounce timer would run,
// including the word-count/AI-state refresh hanging off OnReparsed.
func (a *App) benchEditor(n int) {
	if a.editor == nil {
		return
	}
	durs := make([]float64, 0, n)
	word := []string{"alpha ", "beta ", "gamma ", "delta "}
	for i := 0; i < n; i++ {
		t := time.Now()
		a.editor.InsertAtCursor(word[i%len(word)])
		a.editor.Reparse()
		durs = append(durs, float64(time.Since(t).Microseconds())/1000)
	}
	benchReport["editor_cycles"] = n
	benchReport["editor_ms"] = latency(durs)
	benchReport["editor_doc_bytes"] = len(a.editor.Content())
}

// benchOpen measures note switching: the flush + read + decompress + set +
// re-render path that runs when a row in the tree is clicked.
func (a *App) benchOpen(n int) {
	if a.store == nil {
		return
	}
	notes, err := a.store.ListNotes()
	if err != nil || len(notes) == 0 {
		return
	}
	durs := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		t := time.Now()
		a.openNote(notes[i%len(notes)].Path)
		durs = append(durs, float64(time.Since(t).Microseconds())/1000)
	}
	benchReport["open_count"] = n
	benchReport["open_notes_in_vault"] = len(notes)
	benchReport["open_ms"] = latency(durs)
}

// benchSoak exercises the paths a long editing session takes — opening notes,
// typing, rendering, saving, adding and removing tasks — and samples memory as
// it goes. It runs from idle callbacks rather than in one blocking loop, so the
// main loop keeps turning between cycles: timers fire, GTK disposes widgets,
// and anything that accumulates has the chance to show up.
func (a *App) benchSoak(total int) {
	if a.store == nil || a.editor == nil {
		a.emitReport()
		return
	}
	notes, err := a.store.ListNotes()
	if err != nil || len(notes) == 0 {
		a.emitReport()
		return
	}

	const batch = 20 // cycles per idle callback
	var (
		done       int
		checkpoint []map[string]any
	)
	sampleNow := func() {
		runtime.GC()
		s := takeSample()
		checkpoint = append(checkpoint, map[string]any{
			"cycle": done, "rss_kb": s.RSSKB, "heap_kb": s.HeapKB,
			"go_sys_kb": s.GoSysKB, "goroutines": s.Routines, "gc": s.NumGC,
		})
	}

	// ATLAS_SOAK_OPS selects which operations the cycle performs, so a leak can
	// be bisected down to one of them (default: all).
	ops := os.Getenv("ATLAS_SOAK_OPS")
	if ops == "" {
		ops = "open,type,task,save,tree,welcome"
	}
	does := func(op string) bool { return strings.Contains(ops, op) }
	doOpen, doType, doTask := does("open"), does("type"), does("task")
	doSave, doTree, doWelcome := does("save"), does("tree"), does("welcome")
	benchReport["soak_ops"] = ops

	words := []string{"alpha ", "beta ", "gamma ", "- [ ] task\n"}
	var step func() bool
	step = func() bool {
		for i := 0; i < batch && done < total; i, done = i+1, done+1 {
			if doOpen {
				a.openNote(notes[done%len(notes)].Path)
			}
			if doType {
				a.editor.InsertAtCursor(words[done%len(words)])
				a.editor.Reparse()
			}
			if doTask && done%3 == 0 {
				a.editor.ToggleTask() // create/remove a checkbox widget
			}
			if doSave && done%5 == 0 {
				a.saveCurrent()
			}
			if doTree && done%7 == 0 && a.tree != nil {
				a.tree.Refresh()
			}
			if doWelcome && done%11 == 0 {
				a.showWelcome() // build/tear down the home screen
			}
		}
		if done%(total/10+1) < batch {
			sampleNow()
		}
		if done >= total {
			a.flushDirty()
			sampleNow()
			benchReport["items_created"] = editor.ItemsCreated
			benchReport["soak_cycles"] = total
			benchReport["soak_checkpoints"] = checkpoint
			a.emitReport()
			return false
		}
		return true // keep going on the next idle turn
	}
	sampleNow()
	coreglib.IdleAdd(step)
}

// benchAnchors measures what one embedded-checkbox position costs: a text
// child anchor created, used, and deleted again. It is the floor under the
// editor's memory behaviour, since a task line cannot be rendered without one.
func (a *App) benchAnchors(n int) {
	if a.editor == nil {
		return
	}
	before := takeSample()
	for i := 0; i < n; i++ {
		a.editor.SetContent("- [ ] task one\n- [ ] task two\n")
	}
	runtime.GC()
	after := takeSample()
	benchReport["anchor_rounds"] = n
	benchReport["anchor_bytes_each"] = float64(after.RSSKB-before.RSSKB) * 1024 / float64(n*2)
}

// benchWidgets measures the cost of creating and dropping GTK widgets that are
// never parented — i.e. object churn through the bindings alone, with no text
// view, anchors or signal handlers involved.
func benchWidgets(n int) {
	gcEvery, _ := strconv.Atoi(os.Getenv("ATLAS_WIDGET_GC"))
	before := takeSample()
	for i := 0; i < n; i++ {
		box := gtk.NewBox(gtk.OrientationHorizontal, 0)
		cb := gtk.NewCheckButton()
		box.Append(cb)
		_ = box
		if gcEvery > 0 && i%gcEvery == 0 {
			runtime.GC()
		}
	}
	runtime.GC()
	runtime.GC()
	after := takeSample()
	benchReport["widgets_created"] = n
	benchReport["widgets_rss_before_kb"] = before.RSSKB
	benchReport["widgets_rss_after_kb"] = after.RSSKB
	benchReport["widgets_bytes_each"] = float64(after.RSSKB-before.RSSKB) * 1024 / float64(n)
}

// latency reduces a set of durations to mean / p50 / p95 / max.
func latency(ms []float64) map[string]float64 {
	if len(ms) == 0 {
		return nil
	}
	sorted := append([]float64(nil), ms...)
	for i := 1; i < len(sorted); i++ { // insertion sort: small n, no import
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	pick := func(p float64) float64 {
		i := int(p * float64(len(sorted)-1))
		return sorted[i]
	}
	return map[string]float64{
		"mean": sum / float64(len(sorted)),
		"p50":  pick(0.50),
		"p95":  pick(0.95),
		"max":  sorted[len(sorted)-1],
	}
}

// writeHeapProfile dumps a Go heap profile (ATLAS_PPROF), so a soak run's
// memory growth can be attributed to the code that allocated it.
func writeHeapProfile() {
	path := os.Getenv("ATLAS_PPROF")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "atlas-notes: heap profile: %v\n", err)
		return
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintf(os.Stderr, "atlas-notes: heap profile: %v\n", err)
	}
}

// emitReport prints the JSON report on stdout and quits the app.
func (a *App) emitReport() {
	writeHeapProfile()
	benchReport["phases"] = phaseMarks
	if len(phaseMarks) > 0 {
		benchReport["startup_ms"] = phaseMarks[len(phaseMarks)-1].Ms
	}
	benchReport["after"] = takeSample()
	benchReport["version"] = version
	out, _ := json.Marshal(benchReport)
	fmt.Println(string(out))
	os.Stdout.Sync()
	a.adw.Quit()
}

// printTrace dumps the startup phase table to stderr for ATLAS_TRACE runs.
func printTrace() {
	if !traceOn || benchMode != "" {
		return
	}
	prev := 0.0
	fmt.Fprintln(os.Stderr, "atlas-notes startup trace (ms from exec):")
	for _, p := range phaseMarks {
		fmt.Fprintf(os.Stderr, "  %-16s %8.2f  (+%.2f)\n", p.Name, p.Ms, p.Ms-prev)
		prev = p.Ms
	}
}
