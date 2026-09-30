package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"atlas-notes/internal/storage"
)

// repoURL is the source repository the updater fetches from.
const repoURL = "https://github.com/EternalCoder454/atlas-notes"

// releasesURL is where a packaged build is downloaded from, for the platforms
// that cannot rebuild themselves.
const releasesURL = repoURL + "/releases/latest"

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

// installUpdate fetches the branch from GitHub into the managed clone,
// reinstalls from it, and restarts the app in place. Progress is reported
// through onStatus, whose second argument says whether the work has finished
// (successfully or not) — the Settings page and the launch-time update dialog
// both drive this, and both need to say what is happening.
func (a *App) installUpdate(branch string, onStatus func(text string, done bool)) {
	// Where the app installs itself by rebuilding from source, it can finish
	// the job on its own. Where it arrives as a signed, packaged binary it
	// cannot: there is no compiler to assume, and an application cannot
	// overwrite the executable it is running from. There, the honest thing is
	// to open the page the download is on and say so.
	if !canSelfUpdate {
		if err := openDownloadPage(); err != nil {
			onStatus("Couldn't open the downloads page: "+err.Error()+
				"\n"+releasesURL, true)
			return
		}
		onStatus("Opened the downloads page in your browser.\n"+
			"Close Atlas Notes before installing the new version.", true)
		return
	}

	// A package manager owns a packaged copy, and a copy with no source has
	// nothing to rebuild; both are told what to do instead of being rebuilt
	// over. See install_kind.go.
	if text, handled := updateAdvice(detectInstall(), which(aurHelpers...)); handled {
		onStatus(text, true)
		return
	}

	onStatus("Downloading and building the new version…\nThis takes a minute or two.", false)

	go func() {
		out, err := exec.Command("bash", "-lc", updateScript(branch)).CombinedOutput()
		coreglib.IdleAdd(func() bool {
			if err != nil {
				onStatus("The update didn't finish:\n"+tail(string(out), 400), true)
				return false
			}
			onStatus("Updated. Restarting Atlas Notes…", true)
			a.flushDirty() // synchronous save before we replace the process

			exe, e := installedBinary()
			if e != nil {
				onStatus("Installed, but the app couldn't be restarted: "+e.Error()+
					"\nStart Atlas Notes again to finish.", true)
				return false
			}
			if e := restartInto(exe); e != nil {
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
	// install.sh keeps its own Go toolchain in the data dir when the distro's was
	// too old; without it on PATH this would fail on exactly those machines.
	return fmt.Sprintf(`set -e
if [ -x %[5]q/go ]; then export PATH=%[5]q:"$PATH"; fi
if [ -d %[6]q ]; then export GOCACHE=%[6]q; fi
if [ ! -d %[1]q/.git ]; then
  rm -rf %[1]q
  mkdir -p %[4]q
  git clone %[2]q %[1]q
fi
git -C %[1]q fetch --prune origin
git -C %[1]q checkout %[3]q
git -C %[1]q reset --hard origin/%[3]q
make -C %[1]q install`, src, repoURL, branch, parent, filepath.Join(storage.DataDir(), "go", "bin"), filepath.Join(storage.DataDir(), "cache"))
}

// buildInfo reports where this binary lives and was built, and where updates
// come from. It is the body of the collapsed section above.
func buildInfo() string {
	var parts []string
	if exe, err := installedBinary(); err == nil {
		parts = append(parts, "Installed at: "+exe)
		parts = append(parts, "Install: "+detectInstall().Where())
		if st, serr := os.Stat(exe); serr == nil {
			parts = append(parts, "This build: "+st.ModTime().Format("Jan 2, 2006 3:04 PM"))
		}
	}
	if buildDir != "" {
		parts = append(parts, "Built from: "+buildDir)
	}
	parts = append(parts, "Updates from: "+repoURL)
	if len(parts) == 0 {
		return "No build details are recorded in this binary."
	}
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
