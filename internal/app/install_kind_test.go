package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug these cover is the one the updater had before it could tell installs
// apart: on Arch it would rebuild into ~/.local and leave a second copy that
// shadows the one pacman owns and that nothing updates again.

func TestUpdateCommandPerManager(t *testing.T) {
	for _, c := range []struct {
		name, manager, pkg, helper, want string
	}{
		{"pacman with an AUR helper", "pacman", "atlas-notes", "paru", "paru -Syu atlas-notes"},
		{"apt", "apt", "atlas-notes", "", "sudo apt update && sudo apt install --only-upgrade atlas-notes"},
		{"dnf", "dnf", "atlas-notes", "", "sudo dnf upgrade atlas-notes"},
		{"zypper", "zypper", "atlas-notes", "", "sudo zypper update atlas-notes"},
		{"yum", "yum", "atlas-notes", "", "sudo yum update atlas-notes"},
	} {
		if got := updateCommand(c.manager, c.pkg, c.helper); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Atlas Notes is built from its own PKGBUILD, so without the AUR
// `pacman -Syu atlas-notes` answers "target not found".
func TestPacmanWithoutAHelperDoesNotSuggestAnImpossibleCommand(t *testing.T) {
	got := updateCommand("pacman", "atlas-notes", "")
	if strings.Contains(got, "pacman -Syu") {
		t.Errorf("got %q, which pacman cannot do for a package with no repository", got)
	}
	for _, want := range []string{"makepkg", "git clone"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to mention %q", got, want)
		}
	}
}

func TestNoCommandSuggestsMakeInstall(t *testing.T) {
	for _, mgr := range []string{"pacman", "apt", "dnf", "zypper", "yum", "unknown-mgr"} {
		for _, helper := range []string{"", "paru", "yay"} {
			if got := updateCommand(mgr, "atlas-notes", helper); strings.Contains(got, "make install") {
				t.Errorf("%s (helper %q) was told to run make install: %q", mgr, helper, got)
			}
		}
	}
}

// The command can be pasted into a terminal, so a name a shell would read as
// syntax never reaches it.
func TestUpdateCommandRejectsAnUnsafePackageName(t *testing.T) {
	for _, bad := range []string{"atlas; rm -rf ~", "atlas$(id)", "atlas`id`", "atlas && curl evil.example", "atlas\nrm -rf /", "atlas|tee /tmp/x", ""} {
		if got := updateCommand("dnf", bad, ""); got != "sudo dnf upgrade atlas-notes" {
			t.Errorf("package name %q produced %q; it should have fallen back to the known name", bad, got)
		}
	}
	for _, good := range []string{"atlas-notes", "atlas-notes-git", "gtk4", "go1.26", "a_b.c+d"} {
		if !safePackageName(good) {
			t.Errorf("safePackageName(%q) = false, want true", good)
		}
	}
}

// Each kind gets the right answer from the Update button: a source install
// carries on and rebuilds, the other two are told what to do.
func TestUpdateAdvicePerKind(t *testing.T) {
	if text, ok := updateAdvice(Install{Kind: FromSource, Source: "/x"}, ""); ok || text != "" {
		t.Errorf("a source install should update itself, got %q, %v", text, ok)
	}

	pkg := Install{Kind: FromPackage, Manager: "dnf", Package: "atlas-notes", Binary: "/usr/bin/atlas-notes"}
	text, ok := updateAdvice(pkg, "")
	if !ok || !strings.Contains(text, "sudo dnf upgrade atlas-notes") {
		t.Errorf("a packaged install should be handed the dnf command, got %q, %v", text, ok)
	}
	if !pkg.Managed() {
		t.Error("a packaged install is managed by definition")
	}

	text, ok = updateAdvice(Install{Kind: Standalone, Binary: "/opt/atlas-notes"}, "")
	if !ok || !strings.Contains(text, "install.sh") {
		t.Errorf("a standalone copy should be pointed at the installer, got %q, %v", text, ok)
	}
}

func TestWhere(t *testing.T) {
	for _, c := range []struct {
		in   Install
		want string
	}{
		{Install{Kind: FromSource, Source: "/s"}, "built from /s"},
		{Install{Kind: FromPackage, Manager: "pacman", Binary: "/usr/bin/atlas-notes"}, "installed by pacman: /usr/bin/atlas-notes"},
		{Install{Kind: Standalone, Binary: "/opt/atlas-notes"}, "on its own (no installer or package manager owns it)"},
		{Install{Kind: Flatpak}, "Flatpak (io.github.atlasnotes)"},
	} {
		if got := c.in.Where(); got != c.want {
			t.Errorf("Where() = %q, want %q", got, c.want)
		}
	}
}

func TestOwnerQueryParsing(t *testing.T) {
	names := map[string]func(string) string{}
	for _, q := range ownerQueries {
		names[q.tool] = q.name
	}
	for _, c := range []struct{ tool, out, want string }{
		{"pacman", "/usr/bin/atlas-notes is owned by atlas-notes 0.8.1-1", "atlas-notes"},
		{"pacman", "error: No package owns /x", ""},
		{"dpkg-query", "atlas-notes: /usr/bin/atlas-notes", "atlas-notes"},
		{"dpkg-query", "dpkg-query: no path found matching pattern /x", "dpkg-query"},
		{"rpm", "atlas-notes", "atlas-notes"},
		{"rpm", "", ""},
	} {
		if got := names[c.tool](c.out); got != c.want {
			t.Errorf("%s parsing %q = %q, want %q", c.tool, c.out, got, c.want)
		}
	}
}

// A checkout that was deleted or moved must not be believed: the path the
// binary was built with is never updated.
func TestLooksLikeCheckout(t *testing.T) {
	dir := t.TempDir()
	if looksLikeCheckout(dir) || looksLikeCheckout("") || looksLikeCheckout(filepath.Join(dir, "gone")) {
		t.Error("an empty or missing directory was taken for a checkout")
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !looksLikeCheckout(dir) {
		t.Error("a directory with .git was not taken for a checkout")
	}
}

// With no package owner and the updater's clone present, the copy is a source
// install; with neither it is standalone. ATLAS_DATA_HOME keeps this off the
// real data directory.
func TestDetectInstallFindsTheManagedClone(t *testing.T) {
	t.Setenv("ATLAS_DATA_HOME", t.TempDir())
	old := buildDir
	buildDir = ""
	t.Cleanup(func() { buildDir = old })

	if got := detectInstallReal(); got.Kind == FromSource {
		t.Fatalf("no clone exists, yet got %v", got)
	}
	if err := os.MkdirAll(filepath.Join(canonicalSourceDir(), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := detectInstallReal()
	if got.Kind != FromSource || got.Source != canonicalSourceDir() {
		t.Errorf("got %+v, want a source install at %s", got, canonicalSourceDir())
	}
}

// The update script must find a Go the installer downloaded, or updates fail on
// exactly the distros that needed one.
func TestUpdateScriptUsesThePrivateGo(t *testing.T) {
	t.Setenv("ATLAS_DATA_HOME", "/data")
	s := updateScript("main")
	if !strings.Contains(s, "/data/atlas-notes/go/bin") {
		t.Errorf("update script does not put the installer's Go on PATH:\n%s", s)
	}
	if !strings.Contains(s, "make -C") {
		t.Errorf("update script does not build:\n%s", s)
	}
}
