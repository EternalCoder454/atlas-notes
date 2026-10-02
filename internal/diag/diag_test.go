package diag

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventsAreWrittenOnlyWhileOn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.jsonl")
	Event("before") // off: dropped silently
	if err := Start(p); err != nil {
		t.Fatal(err)
	}
	Event("note.open", "rel", "AtlasOS/Roadmap", "err", errors.New("boom"))
	Stop()
	Event("after")

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want diag.start and note.open, got %d lines:\n%s", len(lines), b)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	if e["ev"] != "note.open" || e["rel"] != "AtlasOS/Roadmap" || e["err"] != "boom" {
		t.Fatalf("event = %v", e)
	}
}

func TestLogStartsAgainPastItsSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.jsonl")
	if err := os.WriteFile(p, make([]byte, maxSize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Start(p); err != nil {
		t.Fatal(err)
	}
	Event("x")
	Stop()
	if info, err := os.Stat(p + ".1"); err != nil || info.Size() != maxSize {
		t.Fatalf("old log not moved aside: %v", err)
	}
	if info, err := os.Stat(p); err != nil || info.Size() == 0 || info.Size() > 4096 {
		t.Fatalf("new log: %v %v", info, err)
	}
}
