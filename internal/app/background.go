package app

import (
	"context"
	"log"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// The work that follows the first frame: scanning the vault for changes made
// outside the app, then reading whichever notes that turned up, to find the
// checklists and to fill the search index.
//
// It runs on its own goroutine. It used to run in an idle callback on the main
// thread, which was fine while all it did was set a flag per note, and was not
// once it had to read every note: on a first launch with 10,000 notes, the
// window stopped responding for 185 ms, and indexing their text as well takes
// nearly two seconds. Storage is safe to use from here; the store serialises
// its own database access, and the content pass holds its lock only for the
// moment it takes to write each batch. Only the interface is updated on the
// main thread, through IdleAdd.
//
// After the first pass the worker waits to be woken, which happens when
// something queues notes for reading: unlocking a note, for instance, whose
// text can now be indexed.
type background struct {
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// startBackground starts the worker. scan says whether the first pass should
// look at the vault on disk before reading anything.
func (a *App) startBackground(scan bool) {
	ctx, cancel := context.WithCancel(context.Background())
	a.bg = &background{wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	go a.runBackground(ctx, scan)
}

func (a *App) runBackground(ctx context.Context, scan bool) {
	defer close(a.bg.done)
	for first := true; ; first = false {
		changed, read := false, 0
		if first && scan {
			before, _ := a.store.CountNotes()
			if err := a.store.Reindex(); err != nil {
				log.Printf("atlas-notes: reindex: %v", err)
			} else {
				if err := a.store.EnsureWelcome(); err != nil {
					log.Printf("atlas-notes: welcome note: %v", err)
				}
				after, _ := a.store.CountNotes()
				changed = after != before
			}
		}
		if n, err := a.store.ResolveContent(ctx); err != nil {
			log.Printf("atlas-notes: reading notes: %v", err)
		} else if n > 0 {
			changed, read = true, n
		}
		if ctx.Err() != nil {
			return
		}

		wasFirst := first
		coreglib.IdleAdd(func() bool {
			if a.closing {
				return false
			}
			if changed && a.tree != nil {
				// Which notes are checklists may have changed, and so may what
				// a search in progress finds; ForceRefresh redraws the one and
				// asks the index again for the other.
				a.tree.ForceRefresh()
				a.refreshWelcome()
			}
			if wasFirst {
				a.backgroundCount = read
				a.backgroundDone = true
			}
			return false
		})

		select {
		case <-ctx.Done():
			return
		case <-a.bg.wake:
		}
	}
}

// wakeBackground asks the worker for another pass. It never blocks: if a pass
// is already owed, one more request changes nothing.
func (a *App) wakeBackground() {
	if a.bg == nil {
		return
	}
	select {
	case a.bg.wake <- struct{}{}:
	default:
	}
}

// stopBackground cancels the worker and waits for it, so the store is not
// closed underneath a pass. The pass stops between batches, which takes
// milliseconds; the wait is bounded anyway, because quitting must not hang on
// a disk that has stopped answering.
func (a *App) stopBackground() {
	if a.bg == nil {
		return
	}
	a.bg.cancel()
	select {
	case <-a.bg.done:
	case <-time.After(3 * time.Second):
		log.Printf("atlas-notes: the background pass did not stop in time")
	}
}
