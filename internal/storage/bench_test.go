package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleNote is a realistic note: prose, a few headings, and a checklist.
func sampleNote(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Note %d\n\n", i)
	for p := 0; p < 8; p++ {
		b.WriteString("Atlas Notes keeps every note as a compressed markdown file on disk, ")
		b.WriteString("indexed for fast browsing and searched locally.\n\n")
	}
	b.WriteString("## Tasks\n\n")
	for k := 0; k < 12; k++ {
		fmt.Fprintf(&b, "- [ ] Task %d for note %d <!-- priority:high order:%d -->\n", k, i, k+1)
	}
	return b.String()
}

func benchStore(tb testing.TB, notes int) *Store {
	tb.Helper()
	dir := tb.TempDir()
	s, err := Open(filepath.Join(dir, "vault"), filepath.Join(dir, "index.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	for i := 0; i < notes; i++ {
		rel := fmt.Sprintf("Folder %02d/Note %04d", i%10, i)
		if err := s.WriteNote(rel, sampleNote(i)); err != nil {
			tb.Fatal(err)
		}
	}
	return s
}

func BenchmarkWriteNote(b *testing.B) {
	s := benchStore(b, 0)
	content := sampleNote(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.WriteNote("Bench Note", content); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadNote(b *testing.B) {
	s := benchStore(b, 0)
	if err := s.WriteNote("Bench Note", sampleNote(1)); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ReadNote("Bench Note"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReindex is the startup path: it runs over a vault of n notes.
func BenchmarkReindex(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("notes=%d", n), func(b *testing.B) {
			s := benchStore(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Reindex(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkListNotes(b *testing.B) {
	s := benchStore(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListNotes(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFolders(b *testing.B) {
	s := benchStore(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListFolders(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenStore measures the cold-open cost paid on every launch.
func BenchmarkOpenStore(b *testing.B) {
	dir := b.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")
	s, err := Open(vault, db)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		s.WriteNote(fmt.Sprintf("Note %04d", i), sampleNote(i))
	}
	s.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := Open(vault, db)
		if err != nil {
			b.Fatal(err)
		}
		s.Close()
	}
	b.StopTimer()
	os.RemoveAll(dir)
}
