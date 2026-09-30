package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlatpakDetectedFromInfoFile(t *testing.T) {
	t.Setenv("ATLAS_DATA_HOME", t.TempDir())
	old := flatpakInfoPath
	t.Cleanup(func() { flatpakInfoPath = old })

	flatpakInfoPath = filepath.Join(t.TempDir(), "missing")
	if inFlatpak() || detectInstallReal().Kind == Flatpak {
		t.Fatal("no .flatpak-info, yet detected as a Flatpak")
	}

	info := filepath.Join(t.TempDir(), ".flatpak-info")
	if err := os.WriteFile(info, []byte("[Application]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flatpakInfoPath = info
	got := detectInstallReal()
	if got.Kind != Flatpak {
		t.Fatalf("got %+v, want a Flatpak install", got)
	}
	if got.Managed() || got.UpdateCommand() != "" {
		t.Error("a Flatpak is not package-manager managed")
	}
	if w := got.Where(); !strings.Contains(w, "Flatpak") {
		t.Errorf("Where() = %q, want it to say Flatpak", w)
	}
}

func TestFlatpakUpdateAdvice(t *testing.T) {
	text, ok := updateAdvice(Install{Kind: Flatpak}, "")
	if !ok || !strings.Contains(text, "flatpak install --user") || strings.ContainsAny(text, "—–") {
		t.Errorf("unexpected advice %q, %v", text, ok)
	}
}
