package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"atlas-notes/internal/storage"
)

// Where the notes live, and keeping them in step with the other devices.
//
// A phone gets notes from a computer the way any two devices share files: a
// sync app such as Syncthing keeps one folder the same on both. Atlas Notes
// only has to be willing to use that folder. The vault is plain files, one per
// note, so a sync app needs to know nothing about it, and the password file
// that protects locked notes is inside the vault too, so it travels with them
// and the same password opens them on the phone.
//
// The index is not in the folder. It stays in the app's private storage,
// because it is a cache the app rebuilds, and a database in a synced folder
// is a database two devices write to at once.

// VaultPath describes where the notes are, as JSON: the folder, and whether
// it is the app's own private one.
func VaultPath() (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	return toJSON(struct {
		Path    string `json:"path"`
		Private bool   `json:"private"`
	}{s.VaultPath, isPrivate(s.VaultPath)})
}

// SetVaultPath moves the app onto another folder of notes: a shared one, or
// back to the app's own with "". The notes in the folder it leaves are not
// touched, copied or moved; switching back shows them again.
//
// The folder has to exist and be writable. If it is not, or it cannot be
// opened, nothing changes and the app goes on using the vault it had.
func SetVaultPath(path string) error {
	path = strings.TrimSpace(path)
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if err := checkWritableDir(abs); err != nil {
			return err
		}
		path = abs
	}

	// Stop a content pass on the old vault, and hold the lock that keeps a
	// new one from starting until the switch is done.
	stopSettle()
	defer settleMu.Unlock()

	mu.Lock()
	defer mu.Unlock()

	cfg, err := storage.LoadConfig()
	if err != nil {
		cfg = storage.DefaultConfig()
	}
	if path == "" {
		cfg.VaultPath = storage.DefaultVaultPath()
	} else {
		cfg.VaultPath = path
	}

	next, err := openVault(cfg.VaultPath)
	if err != nil {
		return err // the old store is still open and still in use
	}
	if err := storage.SaveConfig(cfg); err != nil {
		next.Close()
		return fmt.Errorf("remember the folder: %w", err)
	}
	if store != nil {
		store.Close()
	}
	store = next
	return nil
}

// Settle brings the index up to date with the folder: it looks for notes
// added, changed or removed by anything else, a sync app above all, and then
// reads the ones that need it, for the checklists and for search. It returns
// how many notes it read.
//
// It can take a few seconds on a large vault the first time, so it is called
// off the main thread, after the list is already on screen. Only one runs at a
// time, and switching folders stops it.
func Settle() (int, error) {
	settleMu.Lock()
	defer settleMu.Unlock()

	mu.Lock()
	s := store
	if s == nil {
		mu.Unlock()
		return 0, errors.New("the vault is not open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	settleCancel = cancel
	mu.Unlock()
	defer func() {
		mu.Lock()
		settleCancel = nil
		mu.Unlock()
		cancel()
	}()

	if err := s.Reindex(); err != nil {
		return 0, err
	}
	// Notes still in another format than the vault's are rewritten before they
	// are read, so the pass below reads each one once. A note that will not
	// convert is not a reason to leave search unbuilt, so it is only logged.
	if s.NeedsConversion() {
		if _, err := s.ConvertVault(ctx, nil); err != nil {
			log.Printf("atlas-notes: converting notes to the vault's format: %v", err)
		}
	}
	return s.ResolveContent(ctx)
}

// stopSettle asks a running content pass to stop, waits for it, and returns
// holding settleMu so that no other starts. The caller must unlock it.
func stopSettle() {
	mu.Lock()
	if settleCancel != nil {
		settleCancel()
	}
	mu.Unlock()
	settleMu.Lock()
}

// SearchNotes returns, as a JSON array, the notes whose text contains every
// word of query. It is the same search the desktop app runs, and like it,
// never returns a locked note.
func SearchNotes(query string) (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	paths, err := s.SearchContent(query, 500)
	if err != nil {
		return "", err
	}
	if paths == nil {
		paths = []string{}
	}
	return toJSON(paths)
}

// indexPath is where the index for a vault lives. The app's own vault keeps
// the index it has always had, so nothing existing moves. A shared folder gets
// one of its own, named for its path: sharing one index between folders would
// leave the last folder's notes in it after a switch, and rebuilding it on
// every switch would make switching back slow.
func indexPath(vault string) string {
	if isPrivate(vault) {
		return "" // the storage default
	}
	sum := sha256.Sum256([]byte(filepath.Clean(vault)))
	return filepath.Join(storage.DataDir(), "index-"+hex.EncodeToString(sum[:8])+".db")
}

// isPrivate reports whether a vault path is the app's own private folder.
func isPrivate(path string) bool {
	return path == "" || filepath.Clean(path) == filepath.Clean(storage.DefaultVaultPath())
}

// checkWritableDir makes sure a folder exists and that the app can write to
// it, by writing to it. Asking the filesystem for permissions answers a
// different question on Android, where access to shared storage is granted
// to the app separately from what the folder's mode bits say.
func checkWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("that folder could not be found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	probe, err := os.CreateTemp(dir, ".atlas-notes-probe-*")
	if err != nil {
		return fmt.Errorf("Atlas Notes cannot write to that folder: %w", err)
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}
