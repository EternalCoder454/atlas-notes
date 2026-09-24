package storage

import (
	"fmt"
	"sync"
	"testing"
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
