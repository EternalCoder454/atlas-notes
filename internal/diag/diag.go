// Package diag keeps a diagnostic log: what was clicked, which note and page
// were on screen, what the app was told and what it did about it. It is for
// tracking down a bug that only shows up in someone's own use of the app, such
// as a click that does nothing.
//
// It is off unless turned on (Settings, About, or ATLAS_DIAG=1), it is written
// only to a file in the data directory, and nothing in it is ever sent
// anywhere. It holds note names and the labels of what was clicked; never what
// is typed into a note.
package diag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// maxSize is the size past which the log is moved aside to a ".1" file and
// started again, so it never holds more than about twice this.
const maxSize = 2 << 20

// queueLen is how many events may wait for the writer. Past that, events are
// dropped rather than making the main thread wait on the disk.
const queueLen = 512

var (
	on      atomic.Bool
	mu      sync.Mutex // guards queue and done across Start and Stop
	queue   chan []byte
	done    chan struct{}
	dropped atomic.Int64
)

// Enabled reports whether events are being recorded. Callers that would do
// work to describe an event check it first.
func Enabled() bool { return on.Load() }

// Start begins recording to path. Calling it while recording does nothing.
func Start(path string) error {
	started, err := start(path)
	if started {
		Event("diag.start", "pid", os.Getpid())
	}
	return err
}

// start opens the log and starts the writer, reporting whether it did.
func start(path string) (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	if queue != nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, err
	}
	queue, done = make(chan []byte, queueLen), make(chan struct{})
	go write(path, f, queue, done)
	on.Store(true)
	return true, nil
}

// Stop finishes writing what is queued and closes the log.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if queue == nil {
		return
	}
	on.Store(false)
	close(queue)
	<-done
	queue, done = nil, nil
}

// Event records one event: a name such as "note.open", then pairs of a key and
// a value. It never blocks.
func Event(name string, kv ...any) {
	if !on.Load() {
		return
	}
	e := map[string]any{"t": time.Now().Format("15:04:05.000"), "ev": name}
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			k = fmt.Sprint(kv[i])
		}
		v := kv[i+1]
		if err, ok := v.(error); ok {
			v = err.Error()
		}
		e[k] = v
	}
	if n := dropped.Swap(0); n > 0 {
		e["dropped_before"] = n
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	// Under the lock, so Stop cannot close the queue between the check and
	// the send. The send itself never waits.
	mu.Lock()
	defer mu.Unlock()
	if queue == nil {
		return
	}
	select {
	case queue <- append(line, '\n'):
	default:
		dropped.Add(1)
	}
}

// write is the goroutine that owns the file.
func write(path string, f *os.File, q <-chan []byte, done chan<- struct{}) {
	defer close(done)
	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	for line := range q {
		if size+int64(len(line)) > maxSize {
			f.Close()
			_ = os.Rename(path, path+".1")
			nf, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				for range q { // drain so Stop does not hang
				}
				return
			}
			f, size = nf, 0
		}
		n, err := f.Write(line)
		if err != nil { // a full disk, most likely: stop rather than fail line by line
			f.Close()
			for range q {
			}
			return
		}
		size += int64(n)
	}
	f.Close()
}
