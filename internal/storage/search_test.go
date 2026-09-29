package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func search(t *testing.T, s *Store, q string) []string {
	t.Helper()
	got, err := s.SearchContent(q, 50)
	if err != nil {
		t.Fatalf("SearchContent(%q): %v", q, err)
	}
	return got
}

// indexHas asks the index itself whether a word is in it, going around the
// join that SearchContent uses to leave out locked notes. That join is a
// second line of defence; this checks the first.
func indexHas(t *testing.T, s *Store, word string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM notes_fts WHERE notes_fts MATCH ?`, `"`+word+`"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// dbFilesContain looks for raw bytes in the index database and its WAL, the
// way someone with the files and no password would.
func dbFilesContain(t *testing.T, s *Store, needle string) bool {
	t.Helper()
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Contains(bytes.ToLower(data), []byte(needle)) {
			return true
		}
	}
	return false
}

func TestSearchFindsWordsInNotes(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Groceries", "# Groceries\n\nOat milk and a Café crème from the bakery.\n")
	s.WriteNote("Work/Plan", "# Plan\n\nQuarterly budget review on Thursday.\n")
	s.WriteNote("Unrelated", "# Unrelated\n\nNothing to see.\n")

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"budget", []string{"Work/Plan"}},
		{"BUDGET", []string{"Work/Plan"}},             // case
		{"bud", []string{"Work/Plan"}},                // as it is being typed
		{"cafe creme", []string{"Groceries"}},         // accents
		{"quarterly thursday", []string{"Work/Plan"}}, // every word, any order
		{"budget bakery", nil},                        // no note has both
		{"nowhere", nil},
	} {
		if got := search(t, s, c.query); !slices.Equal(got, c.want) {
			t.Errorf("search %q = %v, want %v", c.query, got, c.want)
		}
	}
}

// Nothing typed into the search box can be read as query syntax. FTS5 has
// operators, column filters and quotes, and any of them unescaped would turn
// a search into an error, or into a different search.
func TestSearchTreatsEverythingTypedAsText(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Syntax", "# Syntax\n\nNear and not or body.\n")
	for _, q := range []string{
		`"`, `""`, `"unclosed`, `AND`, `OR NOT`, `NEAR(a b)`, `body:near`,
		`*`, `-near`, `^near`, `a`, `(`, `)`, `{body}`, `near"`, "\x00", "   ",
	} {
		if _, err := s.SearchContent(q, 10); err != nil {
			t.Errorf("search %q failed: %v", q, err)
		}
	}
	if got := search(t, s, "near"); !slices.Equal(got, []string{"Syntax"}) {
		t.Errorf("an operator word as text: got %v", got)
	}
}

func TestSearchFollowsRenamesAndDeletes(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Before", "# Before\n\nmarmalade\n")
	if err := s.RenameNote("Before", "Folder/After"); err != nil {
		t.Fatal(err)
	}
	if got := search(t, s, "marmalade"); !slices.Equal(got, []string{"Folder/After"}) {
		t.Errorf("after rename: %v", got)
	}
	if err := s.DeleteNote("Folder/After"); err != nil {
		t.Fatal(err)
	}
	if indexHas(t, s, "marmalade") {
		t.Error("a deleted note is still in the index")
	}
}

func TestSearchFollowsDeletedFolders(t *testing.T) {
	s := testStore(t)
	s.CreateFolder("Old")
	s.WriteNote("Old/Inside", "# Inside\n\nquokka\n")
	if err := s.DeleteFolder("Old"); err != nil {
		t.Fatal(err)
	}
	if indexHas(t, s, "quokka") {
		t.Error("a note in a deleted folder is still in the index")
	}
}

// The central promise: a locked note's words are not in the index, and not
// in the database file either, however it came to be locked.
func TestLockedNotesStayOutOfTheIndex(t *testing.T) {
	const secret = "zanzibarquokkafjord"
	s := lockedStore(t)
	s.WriteNote("Private", "# Private\n\n"+secret+"\n")
	if !indexHas(t, s, secret) {
		t.Fatal("setup: an ordinary note was not indexed")
	}
	if !dbFilesContain(t, s, secret) {
		t.Fatal("setup: the word should be findable in the database before locking, or this test proves nothing")
	}

	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if indexHas(t, s, secret) {
		t.Error("locking a note left its words in the index")
	}
	if got := search(t, s, secret); len(got) != 0 {
		t.Errorf("a locked note came back from search: %v", got)
	}
	if dbFilesContain(t, s, secret) {
		t.Error("locking a note left its words readable in the database file")
	}

	// Editing it while the vault is unlocked writes the encrypted form, and
	// must not put anything back.
	s.WriteNote("Private", "# Private\n\n"+secret+" edited\n")
	if indexHas(t, s, secret) || indexHas(t, s, "edited") {
		t.Error("saving a locked note put its words in the index")
	}
	if n, err := s.ResolveContent(context.Background()); err != nil || indexHas(t, s, secret) {
		t.Errorf("the content pass indexed a locked note (read %d, err %v)", n, err)
	}

	// Unlocked again, it is searchable again once the content pass has run.
	if err := s.UnlockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveContent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := search(t, s, secret); !slices.Equal(got, []string{"Private"}) {
		t.Errorf("an unlocked note is not searchable: %v", got)
	}
}

func TestNotesInALockedFolderStayOutOfTheIndex(t *testing.T) {
	const secret = "periwinklegazebo"
	s := lockedStore(t)
	s.CreateFolder("Vault")
	s.WriteNote("Vault/Inside", "# Inside\n\n"+secret+"\n")
	if err := s.LockFolder("Vault"); err != nil {
		t.Fatal(err)
	}
	if indexHas(t, s, secret) {
		t.Error("locking a folder left a note in it in the index")
	}
	if dbFilesContain(t, s, secret) {
		t.Error("locking a folder left a note's words readable in the database file")
	}
	// A note created in a locked folder is locked from its first save.
	s.WriteNote("Vault/New", "# New\n\nlabyrinthinecormorant\n")
	if indexHas(t, s, "labyrinthinecormorant") {
		t.Error("a new note in a locked folder was indexed")
	}
}

// A note changed on disk by something else (a sync tool, another machine) is
// marked by the scan and read again: its new words are found, and its old
// ones are not.
func TestSearchSeesChangesMadeOutsideTheApp(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Synced", "# Synced\n\nbefore\n")

	abs := filepath.Join(s.VaultPath, "Synced"+s.Compression().ext())
	if err := os.WriteFile(abs, []byte("# Synced\n\nafterwards\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	os.Chtimes(abs, later, later)

	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveContent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(search(t, s, "afterwards"), []string{"Synced"}) {
		t.Error("the new text is not searchable")
	}
	if len(search(t, s, "before")) != 0 {
		t.Error("the old text is still searchable")
	}
}

// An index from before search existed has every note queued, and one content
// pass makes all of it searchable.
func TestSearchIndexIsBuiltForAnExistingVault(t *testing.T) {
	dir := t.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")
	s, err := Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	s.WriteNote("Old", "# Old\n\nheirloom\n")
	s.db.Exec(`DROP TABLE notes_fts`) // as an index from before search looks
	s.Close()

	s, err = Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.PendingContent() == 0 {
		t.Fatal("an index without search queued nothing to read")
	}
	if _, err := s.ResolveContent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(search(t, s, "heirloom"), []string{"Old"}) {
		t.Error("an existing note is not searchable after the content pass")
	}
	if s.PendingContent() != 0 {
		t.Error("the content pass left notes queued")
	}
}

// The content pass must not write what it read over something newer. It
// reads with no lock held, so a save can land between its read and its
// write; that save has to win.
func TestContentPassDoesNotOverwriteANewerSave(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Busy", "# Busy\n\nstaleword\n")
	s.db.Exec(`UPDATE notes SET indexed = 0`) // queue it, as the scan would

	resolveReadHook = func() {
		resolveReadHook = nil
		if err := s.WriteNote("Busy", "# Busy\n\nfreshword\n"); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { resolveReadHook = nil })
	if _, err := s.ResolveContent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(search(t, s, "freshword"), []string{"Busy"}) {
		t.Error("the save made during the content pass is not searchable")
	}
	if len(search(t, s, "staleword")) != 0 {
		t.Error("the content pass wrote the older text over the newer save")
	}
}

func TestContentPassStopsWhenCancelled(t *testing.T) {
	s := testStore(t)
	for i := 0; i < resolveBatch*3; i++ {
		s.WriteNote(filepath.Join("Many", string(rune('a'+i%26))+string(rune('a'+i/26))), "# n\n\nword\n")
	}
	s.db.Exec(`UPDATE notes SET indexed = 0`)
	ctx, cancel := context.WithCancel(context.Background())
	resolveReadHook = cancel // cancel during the first batch
	t.Cleanup(func() { resolveReadHook = nil })
	n, err := s.ResolveContent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n >= resolveBatch*3 {
		t.Errorf("read %d notes after being cancelled", n)
	}
	if s.PendingContent() == 0 {
		t.Error("cancelling lost the notes it had not reached")
	}
}

func TestFTSQuery(t *testing.T) {
	for in, want := range map[string]string{
		"budget":         `"budget"*`,
		"Budget  Review": `"Budget"* "Review"*`,
		`he said "hi"`:   `"said"*`, // two-letter words match too much
		"a":              ``,
		"ab":             ``,
		"e-mail":         `"mail"*`,
		"2026 plan":      `"2026"* "plan"*`,
		"café":           `"café"*`,
		`AND OR NEAR`:    `"AND"* "NEAR"*`,
	} {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %s, want %s", in, got, want)
		}
	}
}

// A lock interrupted halfway leaves both forms of a note on disk. The scan
// sees one note twice, and a first scan, which inserts rather than upserts,
// must still come through with one row for it rather than failing outright.
func TestFirstScanSurvivesANoteOnDiskTwice(t *testing.T) {
	dir := t.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(nil)
	os.WriteFile(filepath.Join(vault, "Twice"+CompressionZstd.ext()), enc.EncodeAll([]byte("# Twice\n"), nil), 0o644)
	os.WriteFile(filepath.Join(vault, "Twice"+lockedExt), []byte("sealed bytes"), 0o644)

	s, err := Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Reindex(); err != nil {
		t.Fatalf("a first scan failed on a note present in both forms: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM notes WHERE path = 'Twice'`).Scan(&n)
	if n != 1 {
		t.Fatalf("the note is in the index %d times, want once", n)
	}
}
