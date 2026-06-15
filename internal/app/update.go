package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// repoURL is the canonical source repository. The in-app updater pulls from it,
// and clones it on first update when no local source checkout is found.
const repoURL = "https://github.com/EternalCoder454/atlas-notes"

// buildDir is the source directory this binary was built from, injected at build
// time via:
//
//	-ldflags "-X 'atlas-notes/internal/app.buildDir=<path>'"
//
// It is only one of the locations the updater probes; see sourceDir.
var buildDir string

// sourceDir auto-detects the source tree to rebuild from, trying in order:
//   - the ATLAS_NOTES_SRC environment override,
//   - the canonical clone under the data dir (where the installer puts it), and
//   - the build-time directory embedded in this binary.
//
// It returns "" when none exist, in which case the updater clones a fresh copy.
func sourceDir() string {
	for _, dir := range []string{
		strings.TrimSpace(os.Getenv("ATLAS_NOTES_SRC")),
		canonicalSourceDir(),
		buildDir,
	} {
		if dir != "" && isDir(dir) {
			return dir
		}
	}
	return ""
}

// canonicalSourceDir is where the installer clones the repo and where the updater
// keeps it for `git pull` updates: <data dir>/src.
func canonicalSourceDir() string {
	return filepath.Join(storage.DataDir(), "src")
}

// installedBinary resolves the path of the running binary (following symlinks) so
// the updater can re-exec the freshly installed copy in place.
func installedBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		return resolved, nil
	}
	return exe, nil
}

// buildAppPage builds the settings "App" section: a one-click update (pull or
// clone the latest source, rebuild, reinstall) and restart.
func (a *App) buildAppPage() gtk.Widgetter {
	box := sectionBox()

	box.Append(fieldLabel("Update Atlas Notes"))
	desc := gtk.NewLabel("Fetch the latest version, rebuild, reinstall, and restart — automatically.")
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.AddCSSClass("dim-label")
	box.Append(desc)

	updateBtn := gtk.NewButtonWithLabel("Update & Restart")
	updateBtn.AddCSSClass("suggested-action")
	updateBtn.SetHAlign(gtk.AlignStart)
	box.Append(updateBtn)

	status := gtk.NewLabel("")
	status.SetXAlign(0)
	status.SetWrap(true)
	box.Append(status)

	updateBtn.ConnectClicked(func() { a.runUpdate(updateBtn, status) })

	info := gtk.NewLabel(buildInfo())
	info.SetXAlign(0)
	info.SetWrap(true)
	info.SetSelectable(true)
	info.AddCSSClass("dim-label")
	info.SetMarginTop(8)
	box.Append(info)

	return pageScroll(box)
}

// runUpdate fetches the latest source, reinstalls from it, then restarts the app
// in place.
func (a *App) runUpdate(btn *gtk.Button, status *gtk.Label) {
	btn.SetSensitive(false)
	src := sourceDir()
	if src == "" {
		status.SetText("No source found — cloning " + repoURL + ", building, installing…")
	} else {
		status.SetText("Updating from " + src + " — building, installing…")
	}

	go func() {
		out, err := exec.Command("bash", "-lc", updateScript(src)).CombinedOutput()
		coreglib.IdleAdd(func() bool {
			if err != nil {
				btn.SetSensitive(true)
				status.SetText("Update failed:\n" + tail(string(out), 600))
				return false
			}
			status.SetText("Updated — restarting…")
			a.saveCurrent()

			exe, e := installedBinary()
			if e != nil {
				btn.SetSensitive(true)
				status.SetText("Installed, but couldn't find the binary to restart: " + e.Error())
				return false
			}
			// Replace this process with the freshly installed binary. On success
			// this never returns; the new process opens a fresh window.
			if e := syscall.Exec(exe, []string{exe}, os.Environ()); e != nil {
				btn.SetSensitive(true)
				status.SetText("Installed, but restart failed: " + e.Error() + "\nRelaunch Atlas Notes manually.")
			}
			return false
		})
	}()
}

// updateScript builds the shell pipeline for an update. A git checkout pulls then
// reinstalls; a plain source dir just reinstalls; no source at all is cloned from
// the repo first. A login shell (bash -lc) keeps go/make/git on PATH even when
// the app was launched from the GNOME app grid.
func updateScript(src string) string {
	switch {
	case src == "":
		dst := canonicalSourceDir()
		return fmt.Sprintf(`set -e; mkdir -p %q; git clone %q %q; make -C %q install`,
			filepath.Dir(dst), repoURL, dst, dst)
	case isGitRepo(src):
		return fmt.Sprintf(`set -e; git -C %q pull --ff-only; make -C %q install`, src, src)
	default:
		return fmt.Sprintf(`set -e; make -C %q install`, src)
	}
}

// buildInfo reports where this binary lives, when it was built, and which source
// the updater will use — so the install/update path is always visible.
func buildInfo() string {
	var parts []string
	if exe, err := installedBinary(); err == nil {
		parts = append(parts, "Installed at: "+exe)
		if st, serr := os.Stat(exe); serr == nil {
			parts = append(parts, "This build: "+st.ModTime().Format("Jan 2, 2006 3:04 PM"))
		}
	}
	if src := sourceDir(); src != "" {
		kind := "source"
		if isGitRepo(src) {
			kind = "git checkout"
		}
		parts = append(parts, fmt.Sprintf("Source (%s): %s", kind, src))
	} else {
		parts = append(parts, "Source: none found — Update will clone "+repoURL)
	}
	return strings.Join(parts, "\n")
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// isGitRepo reports whether dir holds a git checkout (a .git directory).
func isGitRepo(dir string) bool {
	return isDir(filepath.Join(dir, ".git"))
}

// tail returns the trailing n characters of s (trimmed), for error display.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
