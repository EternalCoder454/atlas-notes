//go:build unix

package editor

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Opening a FIFO with no writer blocks, and the drop or paste that named it
// would wait on it for good, so it has to be refused before it is opened.
func TestReadImageFileRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe.png")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readImageFile(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as a picture")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readImageFile blocked on a FIFO")
	}
}
