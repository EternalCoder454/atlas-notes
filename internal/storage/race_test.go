package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestConcurrentStoreAccess mirrors what the app does: the UI thread reads and
// lists notes while the autosave goroutine writes, and a reindex runs behind
// both. Run with -race, this catches unsynchronized access to the store, the
// zstd codecs and the SQLite connection.
func TestConcurrentStoreAccess(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 20; i++ {
		if err := s.WriteNote(fmt.Sprintf("Folder %d/Note %d", i%3, i), sampleNote(i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	stop := make(chan struct{})

	// Writers: the autosave path.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := s.WriteNote(fmt.Sprintf("Folder %d/Note %d", w, i), sampleNote(i)); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	// Readers: opening notes and refreshing the tree.
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if _, err := s.ListNotes(); err != nil {
					errs <- err
					return
				}
				if _, err := s.RecentNotes(4); err != nil {
					errs <- err
					return
				}
				s.ReadNote(fmt.Sprintf("Folder %d/Note %d", i%3, i))
				s.CountNotes()
			}
		}()
	}
	// The startup vault scan, running alongside everything else.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			if err := s.Reindex(); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(stop)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent access: %v", err)
	}
}

// TestConcurrentLockingIsSafe drives the password machinery from many
// goroutines at once: saving, reading, locking, unlocking, and dropping the
// key out from under all of it.
//
// It exists because the two mutexes are taken in both orders across the
// package. WriteNote holds writeMu and then asks for the key, which takes
// lockMu; LockNote asks for the key first and then takes writeMu. That is only
// safe because the key accessor releases lockMu before it returns, and nothing
// here holds both at once. Meaningful under -race, which is where it runs.
func TestConcurrentLockingIsSafe(t *testing.T) {
	s := testStore(t)
	if err := s.SetPassword("a password"); err != nil {
		t.Fatal(err)
	}
	const notes = 8
	for i := 0; i < notes; i++ {
		if err := s.WriteNote(fmt.Sprintf("Note %d", i),
			fmt.Sprintf("# Note %d\n\n- [ ] a task\n", i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers and readers, which need the key when a note happens to be locked.
	for i := 0; i < notes; i++ {
		rel := fmt.Sprintf("Note %d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				// Errors are expected and fine: the vault may be locked at this
				// instant. A panic, a race or a hang is not.
				_ = s.WriteNote(rel, fmt.Sprintf("# %s\n\nrevision %d\n\n- [ ] task\n", rel, n))
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = s.ReadNote(rel)
			}
		}()
	}

	// Lock and unlock individual notes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			rel := fmt.Sprintf("Note %d", n%notes)
			if n%2 == 0 {
				_ = s.LockNote(rel)
			} else {
				_ = s.UnlockNote(rel)
			}
		}
	}()

	// And take the key away, repeatedly, while all of that is happening.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			if n%2 == 0 {
				s.Lock()
			} else {
				_ = s.Unlock("a password")
			}
			_ = s.IsUnlocked()
			_ = s.HasPassword()
		}
	}()

	time.Sleep(700 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("locking deadlocked: the goroutines did not finish")
	}

	// Whatever state it ended in, every note has to still be readable with the
	// password, and none may have been left in both forms on disk.
	if err := s.Unlock("a password"); err != nil {
		t.Fatalf("the vault will not unlock after the churn: %v", err)
	}
	for i := 0; i < notes; i++ {
		rel := fmt.Sprintf("Note %d", i)
		if _, err := s.ReadNote(rel); err != nil {
			t.Errorf("%s is unreadable after the churn: %v", rel, err)
		}
		plain := filepath.Join(s.VaultPath, rel+noteExt)
		sealed := filepath.Join(s.VaultPath, rel+lockedExt)
		_, perr := os.Stat(plain)
		_, serr := os.Stat(sealed)
		if perr == nil && serr == nil {
			t.Errorf("%s exists both encrypted and in the clear", rel)
		}
		if perr != nil && serr != nil {
			t.Errorf("%s exists in neither form", rel)
		}
	}
}
