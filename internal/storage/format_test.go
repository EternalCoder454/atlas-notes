package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/ulikunitz/xz"

	"atlas-notes/internal/vaultlock"
)

// storeIn is a store whose vault is set to write c.
func storeIn(t *testing.T, c Compression) *Store {
	t.Helper()
	s := testStore(t)
	if err := s.SetCompression(c); err != nil {
		t.Fatalf("SetCompression(%s): %v", c, err)
	}
	return s
}

// plant writes a note file in format c the way an older version, or another
// machine, would have left it, bypassing the store. A zero mtime leaves the
// time as it is.
func plant(t *testing.T, s *Store, c Compression, rel, text string, mtime time.Time) string {
	t.Helper()
	data, err := s.encode(c, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.VaultPath, filepath.FromSlash(rel)+c.ext())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// vaultFile is a path inside the vault.
func vaultFile(s *Store, name string) string {
	return filepath.Join(s.VaultPath, filepath.FromSlash(name))
}

func TestCompressionNames(t *testing.T) {
	want := []Compression{CompressionNone, CompressionZstd, CompressionGzip, CompressionXZ}
	if got := Compressions(); !slices.Equal(got, want) {
		t.Errorf("Compressions() = %v, want %v", got, want)
	}
	for _, c := range want {
		if got := ParseCompression(string(c)); got != c {
			t.Errorf("ParseCompression(%q) = %q", c, got)
		}
	}
	for _, unknown := range []string{"", "  ", "brotli", "ZSTD2", "md"} {
		if got := ParseCompression(unknown); got != CompressionNone {
			t.Errorf("ParseCompression(%q) = %q, want none", unknown, got)
		}
	}
	exts := map[Compression]string{
		CompressionNone: ".md", CompressionZstd: ".md.zst",
		CompressionGzip: ".md.gz", CompressionXZ: ".md.xz",
	}
	for c, ext := range exts {
		if c.ext() != ext {
			t.Errorf("%s extension = %q, want %q", c, c.ext(), ext)
		}
	}
}

func TestNormalizeRelStripsEveryNoteExtension(t *testing.T) {
	for _, ext := range []string{".md", ".md.zst", ".md.gz", ".md.xz", ".md.enc"} {
		if got := normalizeRel("Work/Todo" + ext); got != "Work/Todo" {
			t.Errorf("normalizeRel(%q) = %q, want Work/Todo", "Work/Todo"+ext, got)
		}
	}
	if got := normalizeRel("Work/Todo"); got != "Work/Todo" {
		t.Errorf("a name with no extension changed: %q", got)
	}
}

func TestRoundTripInEveryCompression(t *testing.T) {
	const content = "# Hello\n\nThis is a note with some **bold** text.\n\n- [ ] a task\n"
	magic := map[Compression][]byte{
		CompressionZstd: zstdMagic, CompressionGzip: gzipMagic, CompressionXZ: xzMagic,
	}
	for _, c := range Compressions() {
		t.Run(string(c), func(t *testing.T) {
			s := storeIn(t, c)
			if err := s.WriteNote("Work/Hello", content); err != nil {
				t.Fatalf("WriteNote: %v", err)
			}
			got, err := s.ReadNote("Work/Hello")
			if err != nil || got != content {
				t.Fatalf("ReadNote = %q, %v", got, err)
			}
			raw, err := os.ReadFile(vaultFile(s, "Work/Hello"+c.ext()))
			if err != nil {
				t.Fatalf("the note is not in a %s file: %v", c.ext(), err)
			}
			if c == CompressionNone {
				if string(raw) != content {
					t.Errorf("a plain note is not its own text: %q", raw)
				}
			} else if !bytes.HasPrefix(raw, magic[c]) {
				t.Errorf("file does not start with the %s magic bytes: % x", c, raw[:min(6, len(raw))])
			}
			if got := s.NoteFileName("Work/Hello"); got != "Work/Hello"+c.ext() {
				t.Errorf("NoteFileName = %q", got)
			}
			// Everything that reads a note finds it in this format too.
			if err := s.Reindex(); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ResolveContent(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(search(t, s, "bold"), []string{"Work/Hello"}) {
				t.Error("the note is not searchable")
			}
			if notes, _ := s.ListNotes(); len(notes) != 1 || !notes[0].HasTasks {
				t.Errorf("index after a scan: %+v", notes)
			}
		})
	}
}

func TestAPlainVaultReadsALegacyNoteAndSavingConvertsIt(t *testing.T) {
	s := testStore(t)
	if got := s.Compression(); got != CompressionNone {
		t.Fatalf("a new vault's compression = %q, want none", got)
	}
	plant(t, s, CompressionZstd, "Old", "# Old\n\nfrom before\n", time.Time{})

	if got, err := s.ReadNote("Old"); err != nil || got != "# Old\n\nfrom before\n" {
		t.Fatalf("ReadNote of a legacy note = %q, %v", got, err)
	}
	if got := s.NoteFileName("Old"); got != "Old.md.zst" {
		t.Errorf("NoteFileName = %q, want Old.md.zst", got)
	}

	if err := s.WriteNote("Old", "# Old\n\nedited\n"); err != nil {
		t.Fatal(err)
	}
	if exists(vaultFile(s, "Old.md.zst")) {
		t.Error("saving left the legacy file behind")
	}
	raw, err := os.ReadFile(vaultFile(s, "Old.md"))
	if err != nil || string(raw) != "# Old\n\nedited\n" {
		t.Errorf("Old.md = %q, %v", raw, err)
	}
	entries, _ := os.ReadDir(s.VaultPath)
	for _, e := range entries {
		switch e.Name() {
		case "Old.md", vaultFileName:
		default:
			t.Errorf("unexpected file after the save: %s", e.Name())
		}
	}
}

func TestLockAndUnlockUnderEveryCompression(t *testing.T) {
	all := Compressions()
	for i, c := range all {
		// A note from before the vault changed format, and one written since.
		prev := all[(i+len(all)-1)%len(all)]
		t.Run(string(c), func(t *testing.T) {
			s := lockedStore(t)
			if err := s.SetCompression(prev); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteNote("Old", "# Old\n\nwritten as "+string(prev)+"\n"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetCompression(c); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteNote("Current", "# Current\n\nsecret words\n"); err != nil {
				t.Fatal(err)
			}

			for _, name := range []string{"Old", "Current"} {
				want, err := s.ReadNote(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.LockNote(name); err != nil {
					t.Fatalf("LockNote(%s): %v", name, err)
				}
				for _, other := range all {
					if exists(vaultFile(s, name+other.ext())) {
						t.Errorf("%s is still there in the clear after locking", name+other.ext())
					}
				}
				if !exists(vaultFile(s, name+lockedExt)) {
					t.Fatalf("%s has no sealed file", name)
				}
				if got := s.NoteFileName(name); got != name+lockedExt {
					t.Errorf("NoteFileName of a locked note = %q", got)
				}
				// The payload stays zstd whatever the vault's format is, so that
				// older versions and the phone can open it.
				key, err := s.key()
				if err != nil {
					t.Fatal(err)
				}
				sealed, _ := os.ReadFile(vaultFile(s, name+lockedExt))
				payload, err := vaultlock.Open(key, sealed)
				if err != nil || !bytes.HasPrefix(payload, zstdMagic) {
					t.Errorf("the sealed payload is not zstd: % x, %v", payload[:min(4, len(payload))], err)
				}
				if got, err := s.ReadNote(name); err != nil || got != want {
					t.Errorf("ReadNote of the locked note = %q, %v", got, err)
				}

				if err := s.UnlockNote(name); err != nil {
					t.Fatalf("UnlockNote(%s): %v", name, err)
				}
				if exists(vaultFile(s, name+lockedExt)) {
					t.Errorf("%s still has a sealed file after unlocking", name)
				}
				for _, other := range all {
					if got, want := exists(vaultFile(s, name+other.ext())), other == c; got != want {
						t.Errorf("%s exists = %v after unlocking, want %v", name+other.ext(), got, want)
					}
				}
				if got := s.NoteFileName(name); got != name+c.ext() {
					t.Errorf("NoteFileName after unlocking = %q, want the vault's extension", got)
				}
				if got, err := s.ReadNote(name); err != nil || got != want {
					t.Errorf("ReadNote after unlocking = %q, %v", got, err)
				}
			}

			// Whatever format is inside a seal is read, so an older or newer
			// version's choice of payload never makes a note unreadable.
			key, _ := s.key()
			for _, pc := range all {
				name := "Sealed " + string(pc)
				text := "# " + name + "\n"
				payload, err := s.encode(pc, []byte(text))
				if err != nil {
					t.Fatal(err)
				}
				sealed, err := vaultlock.Seal(key, payload)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(vaultFile(s, name+lockedExt), sealed, 0o644); err != nil {
					t.Fatal(err)
				}
				if got, err := s.ReadNote(name); err != nil || got != text {
					t.Errorf("a %s payload read as %q, %v", pc, got, err)
				}
			}
		})
	}
}

func TestSavingALockedNoteRemovesEveryPlainCopy(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Private", "# Private\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	// Copies that a sync brought back, in two formats.
	plant(t, s, CompressionNone, "Private", "# Private\n", time.Time{})
	plant(t, s, CompressionGzip, "Private", "# Private\n", time.Time{})
	if err := s.WriteNote("Private", "# Private\n\nedited\n"); err != nil {
		t.Fatal(err)
	}
	for _, c := range Compressions() {
		if exists(vaultFile(s, "Private"+c.ext())) {
			t.Errorf("Private%s is still in the clear after a save of a locked note", c.ext())
		}
	}
}

// bomb is a few hundred kilobytes that expand to more than maxNoteBytes.
func bomb(t *testing.T, c Compression) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch c {
	case CompressionGzip:
		w = gzip.NewWriter(&buf)
	case CompressionXZ:
		xw, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w = xw
	default:
		t.Fatalf("no bomb for %s", c)
	}
	chunk := make([]byte, 1<<20)
	for written := 0; written <= maxNoteBytes; written += len(chunk) {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGzipAndXZBombsAreRefused(t *testing.T) {
	for _, c := range []Compression{CompressionGzip, CompressionXZ} {
		t.Run(string(c), func(t *testing.T) {
			s := testStore(t)
			data := bomb(t, c)
			t.Logf("bomb: %d bytes on disk, over %d MiB decompressed", len(data), maxNoteBytes>>20)
			if err := os.WriteFile(vaultFile(s, "Bomb"+c.ext()), data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadNote("Bomb")
			if err == nil {
				t.Fatalf("a note over the cap was decompressed in full (%d bytes read)", len(got))
			}
			if !errors.Is(err, errNoteTooLarge) {
				t.Errorf("refused for the wrong reason: %v", err)
			}

			// The same protection covers the payload of a locked note.
			if out, err := s.decodePayload(data); err == nil {
				t.Errorf("a %s payload over the cap was decoded (%d bytes)", c, len(out))
			}
		})
	}
}

func TestPlainNoteOverTheCapIsRefused(t *testing.T) {
	s := testStore(t)
	f, err := os.Create(vaultFile(s, "Huge.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file: it is large without taking the disk to prove it.
	if err := f.Truncate(maxNoteBytes + 1); err != nil {
		f.Close()
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	f.Close()
	if got, err := s.ReadNote("Huge"); !errors.Is(err, errNoteTooLarge) {
		t.Errorf("ReadNote of an oversized plain note = %d bytes, %v", len(got), err)
	}
}

func TestConvertVault(t *testing.T) {
	for _, target := range Compressions() {
		t.Run(string(target), func(t *testing.T) {
			s := storeIn(t, target)
			base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
			want := map[string]string{}
			mtimes := map[string]time.Time{}
			i := 0
			for _, from := range Compressions() {
				rel := "Work/Note " + string(from)
				if from == CompressionNone {
					rel = "Note " + string(from)
				}
				text := "# " + rel + "\n\nwords in " + string(from) + "\n"
				mtimes[rel] = base.Add(time.Duration(i) * time.Hour)
				plant(t, s, from, rel, text, mtimes[rel])
				want[rel] = text
				i++
			}
			// A copy Syncthing keeps of an old version is not the vault's.
			stale := plant(t, s, CompressionZstd, ".stversions/Old", "# Old\n", base)

			if err := s.Reindex(); err != nil {
				t.Fatal(err)
			}
			before, _ := s.RecentNotes(10)
			if len(before) != len(want) {
				t.Fatalf("the index holds %d notes, want %d", len(before), len(want))
			}

			var calls [][2]int
			n, err := s.ConvertVault(context.Background(), func(done, total int) {
				calls = append(calls, [2]int{done, total})
			})
			if err != nil {
				t.Fatalf("ConvertVault: %v", err)
			}
			if n != len(want)-1 {
				t.Errorf("converted %d notes, want %d (all but the one already in the format)", n, len(want)-1)
			}
			if len(calls) != n || calls[len(calls)-1] != [2]int{n, n} {
				t.Errorf("progress = %v", calls)
			}
			if s.NeedsConversion() {
				t.Error("NeedsConversion is still true afterwards")
			}
			if !exists(stale) {
				t.Error("a file in .stversions was converted or removed")
			}

			for rel, text := range want {
				for _, c := range Compressions() {
					if got, want := exists(vaultFile(s, rel+c.ext())), c == target; got != want {
						t.Errorf("%s exists = %v, want %v", rel+c.ext(), got, want)
					}
				}
				if got, err := s.ReadNote(rel); err != nil || got != text {
					t.Errorf("ReadNote(%s) = %q, %v", rel, got, err)
				}
				info, err := os.Stat(vaultFile(s, rel+target.ext()))
				if err != nil {
					t.Fatal(err)
				}
				if !info.ModTime().Equal(mtimes[rel]) {
					t.Errorf("%s modification time = %v, want %v", rel, info.ModTime(), mtimes[rel])
				}
			}

			// Converting is not editing: the index and the Recent order stay put.
			if err := s.Reindex(); err != nil {
				t.Fatal(err)
			}
			after, _ := s.RecentNotes(10)
			if len(after) != len(before) {
				t.Fatalf("the index holds %d notes afterwards, want %d", len(after), len(before))
			}
			for i := range before {
				if before[i].Path != after[i].Path || !before[i].ModifiedAt.Equal(after[i].ModifiedAt) {
					t.Errorf("Recent[%d] changed: %+v -> %+v", i, before[i], after[i])
				}
			}

			// A second run has nothing left to do.
			calls = nil
			if n, err := s.ConvertVault(context.Background(), func(int, int) { t.Error("progress on an empty run") }); err != nil || n != 0 {
				t.Errorf("second ConvertVault = %d, %v; want 0", n, err)
			}
		})
	}
}

func TestConvertVaultSkipsLockedNotes(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Secret", "# Secret\n\nhidden\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	sealedBefore, err := os.ReadFile(vaultFile(s, "Secret"+lockedExt))
	if err != nil {
		t.Fatal(err)
	}
	plant(t, s, CompressionGzip, "Open", "# Open\n", time.Time{})
	// Converting needs no password, and must not be able to read a locked note.
	s.Lock()

	n, err := s.ConvertVault(context.Background(), nil)
	if err != nil || n != 1 {
		t.Fatalf("ConvertVault = %d, %v; want 1", n, err)
	}
	sealedAfter, _ := os.ReadFile(vaultFile(s, "Secret"+lockedExt))
	if !bytes.Equal(sealedBefore, sealedAfter) {
		t.Error("the locked note was rewritten")
	}
	for _, c := range Compressions() {
		if exists(vaultFile(s, "Secret"+c.ext())) {
			t.Errorf("a plain copy of the locked note appeared: Secret%s", c.ext())
		}
	}
	if !exists(vaultFile(s, "Open.md")) {
		t.Error("the unlocked note was not converted")
	}
	if s.NeedsConversion() {
		t.Error("a locked note counts as needing conversion")
	}
}

func TestConvertVaultStopsWhenCancelled(t *testing.T) {
	s := testStore(t)
	plant(t, s, CompressionZstd, "One", "# One\n", time.Time{})
	plant(t, s, CompressionZstd, "Two", "# Two\n", time.Time{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := s.ConvertVault(ctx, nil); err != nil || n != 0 {
		t.Errorf("ConvertVault on a cancelled context = %d, %v; want 0 and no error", n, err)
	}
	if !s.NeedsConversion() {
		t.Error("nothing was left to convert after being cancelled")
	}
	if n, err := s.ConvertVault(context.Background(), nil); err != nil || n != 2 {
		t.Errorf("the run after it = %d, %v; want 2", n, err)
	}
}

func TestConvertVaultNeverWritesOverANoteAlreadyInTheFormat(t *testing.T) {
	s := testStore(t)
	// A different note under each name: the copy in the vault's format may be
	// the newer one, so it must survive.
	plant(t, s, CompressionZstd, "Diverged", "# older\n", time.Time{})
	newer := plant(t, s, CompressionNone, "Diverged", "# newer\n", time.Time{})
	// The same note in both: a conversion that stopped before removing the old
	// file. That one is finished, not left.
	plant(t, s, CompressionZstd, "Same", "# same\n", time.Time{})
	plant(t, s, CompressionNone, "Same", "# same\n", time.Time{})

	n, err := s.ConvertVault(context.Background(), nil)
	if err != nil {
		t.Fatalf("ConvertVault: %v", err)
	}
	if n != 1 {
		t.Errorf("converted %d, want 1 (the finished one)", n)
	}
	if got, _ := os.ReadFile(newer); string(got) != "# newer\n" {
		t.Errorf("the note in the vault's format was overwritten: %q", got)
	}
	if !exists(vaultFile(s, "Diverged.md.zst")) {
		t.Error("the differing legacy copy was deleted")
	}
	if exists(vaultFile(s, "Same.md.zst")) {
		t.Error("the finished conversion left the old file")
	}
}

func TestReindexSeesEveryExtensionAndIgnoresHiddenFolders(t *testing.T) {
	s := testStore(t)
	for _, c := range Compressions() {
		plant(t, s, c, "Work/In "+string(c), "# x\n", time.Time{})
	}
	if err := os.WriteFile(vaultFile(s, "Work/Locked"+lockedExt), []byte("sealed"), 0o644); err != nil {
		t.Fatal(err)
	}
	// What Syncthing, git and a trash folder keep in the vault, none of it notes.
	plant(t, s, CompressionNone, ".stversions/Work/In none~20260101", "# old\n", time.Time{})
	plant(t, s, CompressionNone, ".stversions/Old", "# old\n", time.Time{})
	plant(t, s, CompressionZstd, ".git/objects/x", "# x\n", time.Time{})
	plant(t, s, CompressionGzip, ".trash/Gone", "# gone\n", time.Time{})
	plant(t, s, CompressionNone, "Work/.hidden/Inside", "# inside\n", time.Time{})

	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range notes {
		got = append(got, n.Path)
	}
	slices.Sort(got)
	want := []string{"Work/In gzip", "Work/In none", "Work/In xz", "Work/In zstd", "Work/Locked"}
	if !slices.Equal(got, want) {
		t.Errorf("indexed %q, want %q", got, want)
	}
	for _, n := range notes {
		if n.Path == "Work/Locked" && !n.Locked {
			t.Error("the .md.enc file is not indexed as locked")
		}
	}

	folders, err := s.ListFolders()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(folders, []string{"Work"}) {
		t.Errorf("ListFolders = %q, want just Work", folders)
	}
}

func TestHiddenFoldersDoNotNeedConversion(t *testing.T) {
	s := testStore(t)
	plant(t, s, CompressionZstd, ".stversions/Old", "# old\n", time.Time{})
	if s.NeedsConversion() {
		t.Error("an old version kept by Syncthing counts as a note to convert")
	}
	plant(t, s, CompressionZstd, "Real", "# real\n", time.Time{})
	if !s.NeedsConversion() {
		t.Error("a legacy note does not count as needing conversion")
	}
}

func TestRenameAndDeleteOnANonDefaultExtension(t *testing.T) {
	s := testStore(t)
	plant(t, s, CompressionZstd, "Old", "# Old\n", time.Time{})
	plant(t, s, CompressionXZ, "Both", "# Both\n", time.Time{})
	plant(t, s, CompressionNone, "Both", "# Both\n", time.Time{})
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameNote("Old", "Moved/New"); err != nil {
		t.Fatalf("RenameNote: %v", err)
	}
	if exists(vaultFile(s, "Old.md.zst")) || !exists(vaultFile(s, "Moved/New.md.zst")) {
		t.Error("the file did not move with its own extension")
	}
	if got, err := s.ReadNote("Moved/New"); err != nil || got != "# Old\n" {
		t.Errorf("ReadNote after the rename = %q, %v", got, err)
	}
	var indexed int
	s.db.QueryRow(`SELECT count(*) FROM notes WHERE path = 'Moved/New' AND folder = 'Moved'`).Scan(&indexed)
	if indexed != 1 {
		t.Error("the index does not have the renamed note")
	}

	// A note that exists in two formats is renamed whole.
	if err := s.RenameNote("Both", "Renamed"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Renamed.md", "Renamed.md.xz"} {
		if !exists(vaultFile(s, name)) {
			t.Errorf("%s is missing after the rename", name)
		}
	}
	if exists(vaultFile(s, "Both.md")) || exists(vaultFile(s, "Both.md.xz")) {
		t.Error("a form of the note was left under the old name")
	}

	if err := s.DeleteNotePermanently("Moved/New"); err != nil {
		t.Fatal(err)
	}
	if exists(vaultFile(s, "Moved/New.md.zst")) {
		t.Error("the file is still there after a delete")
	}
	if err := s.DeleteNotePermanently("Renamed"); err != nil {
		t.Fatal(err)
	}
	if exists(vaultFile(s, "Renamed.md")) || exists(vaultFile(s, "Renamed.md.xz")) {
		t.Error("a form of the note survived a delete")
	}
	var left int
	s.db.QueryRow(`SELECT count(*) FROM notes`).Scan(&left)
	if left != 0 {
		t.Errorf("%d rows left in the index", left)
	}
}

func TestUniqueNameSeesEveryForm(t *testing.T) {
	s := testStore(t)
	plant(t, s, CompressionGzip, "Taken", "# Taken\n", time.Time{})
	if err := os.WriteFile(vaultFile(s, "Sealed"+lockedExt), []byte("sealed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.UniqueName("", "Taken"); got != "Taken 2" {
		t.Errorf("UniqueName with a .md.gz note = %q, want Taken 2", got)
	}
	if got := s.UniqueName("", "Sealed"); got != "Sealed 2" {
		t.Errorf("UniqueName with a locked note = %q, want Sealed 2", got)
	}
	if got := s.UniqueName("Work", "Free"); got != "Work/Free" {
		t.Errorf("UniqueName for a free name = %q", got)
	}
}

func TestCompressionSettingSurvivesReopening(t *testing.T) {
	dir := t.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")
	open := func() *Store {
		t.Helper()
		s, err := Open(vault, db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	s := open()
	if s.Compression() != CompressionNone {
		t.Errorf("a new vault is %q, want none", s.Compression())
	}
	if err := s.SetCompression("brotli"); err == nil {
		t.Error("SetCompression accepted a format that does not exist")
	}
	if err := s.SetCompression(CompressionXZ); err != nil {
		t.Fatal(err)
	}
	s.Close()

	raw, err := os.ReadFile(filepath.Join(vault, ".atlas-vault.json"))
	if err != nil || string(raw) != `{"compression":"xz"}` {
		t.Errorf("settings file = %q, %v", raw, err)
	}
	s = open()
	if s.Compression() != CompressionXZ {
		t.Errorf("after reopening the vault is %q, want xz", s.Compression())
	}
	if err := s.WriteNote("Kept", "# Kept\n"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !exists(filepath.Join(vault, "Kept.md.xz")) {
		t.Error("a save after reopening did not use the setting")
	}

	// A settings file that is damaged, or from something newer, is not a reason
	// to be unable to open the vault.
	for _, content := range []string{"not json", `{"compression":"brotli"}`, ``} {
		if err := os.WriteFile(filepath.Join(vault, ".atlas-vault.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		s = open()
		if s.Compression() != CompressionNone {
			t.Errorf("settings %q gave %q, want none", content, s.Compression())
		}
		s.Close()
	}
	os.Remove(filepath.Join(vault, ".atlas-vault.json"))
	s = open()
	defer s.Close()
	if s.Compression() != CompressionNone {
		t.Errorf("no settings file gave %q, want none", s.Compression())
	}
}
