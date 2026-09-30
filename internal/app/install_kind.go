package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// How this copy of Atlas Notes got onto the machine, and therefore what
// "update" means for it.
//
// The updater used to assume one answer: rebuild from the source checkout in the
// data directory. That is right for a copy installed by scripts/install.sh, and
// wrong for one that pacman, dnf, apt or zypper installed: the copy is not
// broken, and rebuilding into ~/.local would put a second one on PATH that
// shadows the packaged binary and is never updated by the package manager.
// Writing over /usr/bin ourselves would be worse, leaving the package database
// describing a file that is no longer there.
//
// So the three cases are told apart, and each gets the update that is correct
// for it.

// InstallKind is one of the three ways Atlas Notes can be installed.
type InstallKind int

const (
	// FromSource is a checkout that install.sh or `make install` built from.
	// Updating it means pulling and rebuilding, which installUpdate does.
	FromSource InstallKind = iota

	// FromPackage means a distribution package owns the binary. Updating is the
	// package manager's job.
	FromPackage

	// Standalone is a copy nothing owns and nothing recorded a source for: a
	// binary copied into place by hand. There is nothing to rebuild from.
	Standalone

	// Flatpak is a copy running inside a Flatpak sandbox. See install_flatpak.go.
	Flatpak
)

// Install describes this copy of Atlas Notes.
type Install struct {
	Kind InstallKind

	// Source is the checkout to pull and rebuild in, when there is one.
	Source string
	// Binary is the running executable with symlinks resolved, which is what the
	// package managers are asked about.
	Binary string

	// Manager is the command that owns updates when Kind is FromPackage:
	// "pacman", "apt", "dnf", "zypper". Package is the name it knows this
	// install by.
	Manager string
	Package string
}

// Managed says whether something other than Atlas Notes is responsible for
// updating it.
func (in Install) Managed() bool { return in.Kind == FromPackage }

// Where is a short phrase for the About section: how this copy got here, not
// just a path. A bare "/usr/bin/atlas-notes" does not say that pacman put it
// there and pacman will replace it.
func (in Install) Where() string {
	switch in.Kind {
	case Flatpak:
		return "Flatpak (" + flatpakAppID + ")"
	case FromSource:
		return "built from " + in.Source
	case FromPackage:
		return "installed by " + in.Manager + ": " + in.Binary
	default:
		// The path is already on the line above ("Installed at"); what this
		// line adds is that nothing owns the copy.
		return "on its own (no installer or package manager owns it)"
	}
}

// binaryName is what the package and the executable are both called.
const binaryName = "atlas-notes"

// installerURL is the one-line installer, for copies that have no source to
// update from.
const installerURL = "https://raw.githubusercontent.com/EternalCoder454/atlas-notes/main/scripts/install.sh"

// updateCommand is what the user should run to update a packaged install.
//
// For everything except Arch this is one line, because the package is in a
// repository the system already tracks. Arch is the awkward one: Atlas Notes is
// built from its own PKGBUILD, so `pacman -Syu` only knows about it if it came
// from the AUR. An AUR helper is used when one is installed, and otherwise the
// recipe that always works, fetching the newer PKGBUILD and building it, is
// spelled out rather than a command that would answer "target not found".
// aurHelper is the AUR wrapper that is available, or "".
func updateCommand(manager, pkg, aurHelper string) string {
	if !safePackageName(pkg) {
		// The name ends up in a command line the user may paste into a terminal,
		// so anything that is not a plain package name is replaced by the one we
		// already know. No real package manager produces such a name; this is
		// here so that none of them has to be trusted not to.
		pkg = binaryName
	}
	switch manager {
	case "pacman":
		if aurHelper != "" {
			return aurHelper + " -Syu " + pkg
		}
		return "git clone " + repoURL + ".git && cd atlas-notes/packaging && makepkg -si"
	case "apt":
		return "sudo apt update && sudo apt install --only-upgrade " + pkg
	case "dnf":
		return "sudo dnf upgrade " + pkg
	case "yum":
		return "sudo yum update " + pkg
	case "zypper":
		return "sudo zypper update " + pkg
	case "":
		return ""
	default:
		return manager + " " + pkg
	}
}

// UpdateCommand is updateCommand for this machine, or "" when Atlas Notes
// updates itself.
func (in Install) UpdateCommand() string {
	if in.Kind != FromPackage {
		return ""
	}
	return updateCommand(in.Manager, in.Package, which(aurHelpers...))
}

// updateAdvice is what the Update button says instead of building, when this
// copy is not one Atlas Notes may rebuild. ok is false for a source install,
// which carries on and updates itself.
func updateAdvice(in Install, aurHelper string) (text string, ok bool) {
	switch in.Kind {
	case Flatpak:
		return flatpakAdvice(), true
	case FromPackage:
		return "Atlas Notes was installed by " + in.Manager + ", so " + in.Manager +
			" updates it, not the app. Run this in a terminal:\n" +
			updateCommand(in.Manager, in.Package, aurHelper), true
	case Standalone:
		return "This copy of Atlas Notes was not installed by a package manager or by the " +
			"installer, so there is no source here to rebuild from. To replace it with one " +
			"that can update itself, run:\ncurl -fsSL " + installerURL + " | bash", true
	}
	return "", false
}

// safePackageName accepts the characters package names are made of. Everything
// a shell would read as syntax is rejected.
func safePackageName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '+':
		default:
			return false
		}
	}
	return true
}

// aurHelpers are the wrappers that can upgrade an AUR package, in the order
// they are preferred. Plain pacman cannot: an AUR package is built locally and
// is in no repository pacman syncs.
var aurHelpers = []string{"paru", "yay", "pikaur", "trizen", "aura"}

// detectInstall is a variable so tests can stand in for the machine.
var detectInstall = detectInstallReal

// looksLikeCheckout reports whether a path is still a source tree worth
// building in. The path baked into the binary at build time is never updated,
// so a checkout that has since been moved or deleted leaves one pointing at
// nothing.
func looksLikeCheckout(dir string) bool {
	if dir == "" {
		return false
	}
	for _, marker := range []string{".git", "Makefile"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func detectInstallReal() Install {
	var in Install

	// The binary is found first and kept whatever the answer is.
	if exe, err := installedBinary(); err == nil {
		in.Binary = exe
	}

	// Checked first: the sandbox has no package database to ask, and a binary
	// under /app is not something to rebuild.
	if inFlatpak() {
		in.Kind = Flatpak
		return in
	}

	// A package that owns the binary comes first. A packaged build can still
	// carry a path to the tree it was built in (makepkg leaves its src/
	// directory behind), and trusting that would send a packaged copy down the
	// rebuild path.
	if in.Binary != "" {
		if mgr, pkg, ok := packageOwner(in.Binary); ok {
			in.Kind, in.Manager, in.Package = FromPackage, mgr, pkg
			return in
		}
	}

	// The updater's own clone, then the one this binary was built from.
	for _, src := range []string{canonicalSourceDir(), buildDir} {
		if looksLikeCheckout(src) {
			in.Kind, in.Source = FromSource, src
			return in
		}
	}

	in.Kind = Standalone
	return in
}

// packageOwner asks whichever package managers are installed whether any of
// them owns a path. Each is a local database lookup, a few milliseconds, and
// never touches the network. The tool that answers and the command that updates
// are not always the same program (rpm answers for dnf and zypper alike), so
// they are found separately.
func packageOwner(path string) (manager, pkg string, ok bool) {
	for _, q := range ownerQueries {
		if which(q.tool) == "" {
			continue
		}
		out, err := exec.Command(q.tool, append(append([]string{}, q.args...), path)...).Output()
		if err != nil {
			continue // not owned by this one, or it could not say
		}
		name := q.name(strings.TrimSpace(string(out)))
		if name == "" {
			continue
		}
		return q.updater(), name, true
	}
	return "", "", false
}

var ownerQueries = []struct {
	tool string
	args []string
	// name pulls the package name out of what the tool printed.
	name func(string) string
	// updater is the command that performs updates on this system, which may
	// not be the tool that answered.
	updater func() string
}{
	{
		// "/usr/bin/atlas-notes is owned by atlas-notes 0.8.1-1"
		tool: "pacman", args: []string{"-Qo"},
		name: func(s string) string {
			_, after, found := strings.Cut(s, " is owned by ")
			if !found {
				return ""
			}
			return firstField(after)
		},
		updater: func() string { return "pacman" },
	},
	{
		// "atlas-notes: /usr/bin/atlas-notes"
		tool: "dpkg-query", args: []string{"-S"},
		name: func(s string) string {
			before, _, found := strings.Cut(s, ":")
			if !found {
				return ""
			}
			// A path can be listed by more than one package; the first line is
			// the one that owns this file.
			return firstField(before)
		},
		updater: func() string { return "apt" },
	},
	{
		// The name on its own, because that is what is asked for.
		tool: "rpm", args: []string{"-qf", "--queryformat", "%{NAME}"},
		name: firstField,
		// rpm is the database for several distributions with different front
		// ends, so the updater is whichever of them is installed.
		updater: func() string {
			for _, m := range []string{"dnf", "zypper", "yum"} {
				if which(m) != "" {
					return m
				}
			}
			return "rpm"
		},
	},
}

// firstField is the first whitespace-separated word, or "".
func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// which returns the first of names that is on PATH, or "".
func which(names ...string) string {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	return ""
}
