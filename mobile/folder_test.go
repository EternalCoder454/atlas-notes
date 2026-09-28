package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"atlas-notes/internal/storage"
)

// desktop writes notes into a folder the way the desktop app would, with a
// store and an index of its own, as if the folder had arrived by sync.
func desktop(t *testing.T, dir string) *storage.Store {
	t.Helper()
	s, err := storage.Open(dir, filepath.Join(t.TempDir(), "desktop-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func listed(t *testing.T) []string {
	t.Helper()
	raw, err := ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	var rows []noteRow
	json.Unmarshal([]byte(raw), &rows)
	var out []string
	for _, r := range rows {
		out = append(out, r.Path)
	}
	slices.Sort(out)
	return out
}

func TestASharedFolderIsUsedAndRemembered(t *testing.T) {
	data := t.TempDir()
	Close()
	if err := Open(data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	if got := listed(t); !slices.Equal(got, []string{"Getting Started"}) {
		t.Fatalf("private vault: %v", got)
	}

	shared := t.TempDir()
	d := desktop(t, shared)
	d.WriteNote("From the laptop", "# From the laptop\n")

	if err := SetVaultPath(shared); err != nil {
		t.Fatal(err)
	}
	if got := listed(t); !slices.Equal(got, []string{"From the laptop"}) {
		t.Errorf("shared folder: %v", got)
	}
	if _, err := os.Stat(filepath.Join(shared, "Getting Started.md.zst")); err == nil {
		t.Error("the guide note was written into the shared folder, where it would sync everywhere")
	}

	// A restart keeps the folder.
	Close()
	if err := Open(data); err != nil {
		t.Fatal(err)
	}
	if got := listed(t); !slices.Equal(got, []string{"From the laptop"}) {
		t.Errorf("after a restart: %v", got)
	}
	raw, _ := VaultPath()
	var where struct {
		Path    string `json:"path"`
		Private bool   `json:"private"`
	}
	json.Unmarshal([]byte(raw), &where)
	if where.Private || where.Path != shared {
		t.Errorf("VaultPath = %+v, want the shared folder", where)
	}

	// Back to the app's own, where its notes are as they were.
	if err := SetVaultPath(""); err != nil {
		t.Fatal(err)
	}
	if got := listed(t); !slices.Equal(got, []string{"Getting Started"}) {
		t.Errorf("back to the private vault: %v", got)
	}
}

func TestSetVaultPathRefusesFoldersItCannotUse(t *testing.T) {
	newVault(t)
	file := filepath.Join(t.TempDir(), "a file")
	os.WriteFile(file, nil, 0o644)
	readOnly := t.TempDir()
	os.Chmod(readOnly, 0o555)
	t.Cleanup(func() { os.Chmod(readOnly, 0o755) })

	for name, dir := range map[string]string{
		"missing":   filepath.Join(t.TempDir(), "not there"),
		"a file":    file,
		"read-only": readOnly,
	} {
		if err := SetVaultPath(dir); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// And the vault it had is still the one in use.
	if got := listed(t); !slices.Equal(got, []string{"Getting Started"}) {
		t.Errorf("after refusing: %v", got)
	}
}

// The password file is inside the vault, so it syncs with the notes, and the
// password set on the desktop opens them on the phone.
func TestLockedNotesFromTheDesktopOpenWithTheSamePassword(t *testing.T) {
	newVault(t)
	shared := t.TempDir()
	d := desktop(t, shared)
	d.WriteNote("Diary", "# Diary\n\ndear diary\n")
	if err := d.SetPassword("the same on every device"); err != nil {
		t.Fatal(err)
	}
	if err := d.LockNote("Diary"); err != nil {
		t.Fatal(err)
	}

	if err := SetVaultPath(shared); err != nil {
		t.Fatal(err)
	}
	if !HasPassword() {
		t.Fatal("the phone does not see the desktop's password")
	}
	if _, err := ReadNote("Diary"); err == nil || err.Error() != ErrLockedMessage {
		t.Fatalf("a locked note read without the password: %v", err)
	}
	if err := Unlock("the same on every device"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadNote("Diary"); err != nil || got != "# Diary\n\ndear diary\n" {
		t.Errorf("after unlocking: %q, %v", got, err)
	}
}

// Settle is how the phone sees what a sync app changed: new notes appear, and
// their text is searchable.
func TestSettlePicksUpWhatSyncChanged(t *testing.T) {
	newVault(t)
	shared := t.TempDir()
	desktop(t, shared).WriteNote("First", "# First\n")
	if err := SetVaultPath(shared); err != nil {
		t.Fatal(err)
	}

	desktop(t, shared).WriteNote("Arrived later", "# Arrived later\n\nsynchronicity\n")
	if _, err := Settle(); err != nil {
		t.Fatal(err)
	}
	if got := listed(t); !slices.Contains(got, "Arrived later") {
		t.Errorf("a synced note did not appear: %v", got)
	}
	raw, err := SearchNotes("synchron")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	json.Unmarshal([]byte(raw), &hits)
	if !slices.Equal(hits, []string{"Arrived later"}) {
		t.Errorf("search: %v", hits)
	}
}

// Switching folders while a content pass runs stops the pass and switches;
// neither waits on the other forever.
func TestSwitchingWhileSettlingDoesNotHang(t *testing.T) {
	newVault(t)
	shared := t.TempDir()
	d := desktop(t, shared)
	for i := 0; i < 600; i++ {
		d.WriteNote(filepath.Join("Many", time.Duration(i).String()), "# n\n\nsome words to read\n")
	}
	if err := SetVaultPath(shared); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)
	go func() { _, err := Settle(); done <- err }()
	go func() { done <- SetVaultPath("") }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("switching folders and settling deadlocked")
		}
	}
	if got := listed(t); !slices.Equal(got, []string{"Getting Started"}) {
		t.Errorf("after the switch: %v", got)
	}
}
