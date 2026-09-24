package app

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// repoURL is the source repository the updater fetches from.
const repoURL = "https://github.com/EternalCoder454/atlas-notes"

// buildDir is the source directory this binary was built from, injected at build
// time via -ldflags "-X 'atlas-notes/internal/app.buildDir=<path>'". Shown for
// reference; the updater itself fetches into its own clone (see updateScript).
var buildDir string

// canonicalSourceDir is the updater-managed clone: <data dir>/src. Updates always
// run here so a developer's working checkout is never touched.
func canonicalSourceDir() string {
	return filepath.Join(storage.DataDir(), "src")
}

// channelBranch maps an update channel to its git branch.
func channelBranch(channel string) string {
	if channel == storage.ChannelBeta {
		return "beta"
	}
	return "main" // release
}

// channelFromIndex maps the channel dropdown's selection to a channel id.
func channelFromIndex(i uint) string {
	if i == 1 {
		return storage.ChannelBeta
	}
	return storage.ChannelRelease
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

// buildAppPage builds the settings "App" section: an update channel selector
// (Release = main, Beta = beta) and a one-click update + restart.
func (a *App) buildAppPage() gtk.Widgetter {
	box := sectionBox()

	box.Append(fieldLabel("Update Atlas Notes"))
	desc := gtk.NewLabel("Fetch the latest version from GitHub, rebuild, reinstall, and restart — automatically.")
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.AddCSSClass("dim-label")
	box.Append(desc)

	chanLabel := gtk.NewLabel("Update channel")
	chanLabel.SetXAlign(0)
	chanLabel.SetMarginTop(8)
	chanLabel.AddCSSClass("heading")
	box.Append(chanLabel)

	channel := gtk.NewDropDownFromStrings([]string{
		"Release — main branch (stable)",
		"Beta — beta branch (newest, may be unstable)",
	})
	channel.SetHAlign(gtk.AlignStart)
	if a.cfg.UpdateChannel == storage.ChannelBeta {
		channel.SetSelected(1)
	}
	channel.NotifyProperty("selected", func() {
		a.cfg.UpdateChannel = channelFromIndex(channel.Selected())
		if err := storage.SaveConfig(a.cfg); err != nil {
			log.Printf("atlas-notes: save update channel: %v", err)
		}
	})
	box.Append(channel)

	check := gtk.NewCheckButtonWithLabel("Check for updates when Atlas Notes starts")
	check.SetActive(a.cfg.CheckUpdates)
	check.SetTooltipText("Asks GitHub whether a newer version has been published. " +
		"Nothing about you or your notes is sent.")
	check.SetMarginTop(8)
	check.ConnectToggled(func() {
		a.cfg.CheckUpdates = check.Active()
		if err := storage.SaveConfig(a.cfg); err != nil {
			log.Printf("atlas-notes: save update setting: %v", err)
		}
	})
	box.Append(check)

	updateBtn := gtk.NewButtonWithLabel("Update & Restart")
	updateBtn.AddCSSClass("suggested-action")
	updateBtn.SetHAlign(gtk.AlignStart)
	updateBtn.SetMarginTop(8)
	box.Append(updateBtn)

	status := gtk.NewLabel("")
	status.SetXAlign(0)
	status.SetWrap(true)
	box.Append(status)

	updateBtn.ConnectClicked(func() {
		a.cfg.UpdateChannel = channelFromIndex(channel.Selected())
		if err := storage.SaveConfig(a.cfg); err != nil {
			log.Printf("atlas-notes: save update channel: %v", err)
		}
		updateBtn.SetSensitive(false)
		a.installUpdate(channelBranch(a.cfg.UpdateChannel), func(text string, done bool) {
			status.SetText(text)
			if done {
				updateBtn.SetSensitive(true)
			}
		})
	})

	info := gtk.NewLabel(buildInfo())
	info.SetXAlign(0)
	info.SetWrap(true)
	info.SetSelectable(true)
	info.AddCSSClass("dim-label")
	info.SetMarginTop(8)
	box.Append(info)

	return pageScroll(box)
}

// installUpdate fetches the branch from GitHub into the managed clone,
// reinstalls from it, and restarts the app in place. Progress is reported
// through onStatus, whose second argument says whether the work has finished
// (successfully or not) — the Settings page and the launch-time update dialog
// both drive this, and both need to say what is happening.
func (a *App) installUpdate(branch string, onStatus func(text string, done bool)) {
	onStatus("Downloading and building the new version…\nThis takes a minute or two.", false)

	go func() {
		out, err := exec.Command("bash", "-lc", updateScript(branch)).CombinedOutput()
		coreglib.IdleAdd(func() bool {
			if err != nil {
				onStatus("The update didn't finish:\n"+tail(string(out), 400), true)
				return false
			}
			onStatus("Updated — restarting Atlas Notes…", true)
			a.flushDirty() // synchronous save before we replace the process

			exe, e := installedBinary()
			if e != nil {
				onStatus("Installed, but the app couldn't be restarted: "+e.Error()+
					"\nStart Atlas Notes again to finish.", true)
				return false
			}
			// Replace this process with the freshly installed binary. On success
			// this never returns; the new process opens a fresh window.
			if e := syscall.Exec(exe, []string{exe}, os.Environ()); e != nil {
				onStatus("Installed, but the app couldn't be restarted: "+e.Error()+
					"\nStart Atlas Notes again to finish.", true)
			}
			return false
		})
	}()
}

// updateScript fetches branch from the repo into the updater's own clone (never a
// developer checkout) and installs it. A login shell (bash -lc) keeps go/make/git
// on PATH even when the app was launched from the GNOME app grid.
func updateScript(branch string) string {
	src := canonicalSourceDir()
	parent := filepath.Dir(src)
	return fmt.Sprintf(`set -e
if [ ! -d %[1]q/.git ]; then
  rm -rf %[1]q
  mkdir -p %[4]q
  git clone %[2]q %[1]q
fi
git -C %[1]q fetch --prune origin
git -C %[1]q checkout %[3]q
git -C %[1]q reset --hard origin/%[3]q
make -C %[1]q install`, src, repoURL, branch, parent)
}

// buildInfo reports the version, where this binary lives and was built, and where
// updates come from.
func buildInfo() string {
	parts := []string{"Atlas Notes v" + version}
	if exe, err := installedBinary(); err == nil {
		parts = append(parts, "Installed at: "+exe)
		if st, serr := os.Stat(exe); serr == nil {
			parts = append(parts, "This build: "+st.ModTime().Format("Jan 2, 2006 3:04 PM"))
		}
	}
	if buildDir != "" {
		parts = append(parts, "Built from: "+buildDir)
	}
	parts = append(parts, "Updates from: "+repoURL)
	return strings.Join(parts, "\n")
}

// tail returns the trailing n characters of s (trimmed), for error display.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
