package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sandbox points the data and config directories at fresh temporary ones.
func sandbox(t *testing.T) {
	t.Helper()
	t.Setenv("ATLAS_DATA_HOME", t.TempDir())
	t.Setenv("ATLAS_CONFIG_HOME", t.TempDir())
}

func savedVaultPath(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		VaultPath string `json:"vault_path"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw.VaultPath
}

// The default vault is written as "", so a data directory that is copied or
// restored somewhere else still finds its own vault.
func TestDefaultVaultIsNotPinnedToAnAbsolutePath(t *testing.T) {
	sandbox(t)
	cfg := DefaultConfig()
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := savedVaultPath(t); got != "" {
		t.Fatalf("the default vault was written as %q, want \"\"", got)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VaultPath != DefaultVaultPath() {
		t.Fatalf("loaded vault path %q, want the default %q", loaded.VaultPath, DefaultVaultPath())
	}
}

// Configs written by earlier versions hold the default as an absolute path.
// They keep working, and the next save rewrites them the portable way.
func TestAbsoluteDefaultVaultMigratesOnSave(t *testing.T) {
	sandbox(t)
	cfg := DefaultConfig()
	cfg.VaultPath = DefaultVaultPath() + string(filepath.Separator) // as an older version might spell it
	data, _ := json.Marshal(cfg)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(loaded.VaultPath) != DefaultVaultPath() {
		t.Fatalf("loaded %q, want the default", loaded.VaultPath)
	}
	if err := SaveConfig(loaded); err != nil {
		t.Fatal(err)
	}
	if got := savedVaultPath(t); got != "" {
		t.Fatalf("after saving, the vault is still %q", got)
	}
}

// A vault the user chose is theirs, and is written exactly as given.
func TestChosenVaultIsKept(t *testing.T) {
	sandbox(t)
	mine := filepath.Join(t.TempDir(), "Synced", "Notes")
	cfg := DefaultConfig()
	cfg.VaultPath = mine
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := savedVaultPath(t); got != mine {
		t.Fatalf("a chosen vault was written as %q, want %q", got, mine)
	}
}
