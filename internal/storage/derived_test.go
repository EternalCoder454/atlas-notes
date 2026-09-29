package storage

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// saveNote writes a note or fails the test.
func saveNote(t *testing.T, s *Store, rel, text string) {
	t.Helper()
	if err := s.WriteNote(rel, text); err != nil {
		t.Fatalf("WriteNote(%q): %v", rel, err)
	}
}

// readBack reads a note or fails the test.
func readBack(t *testing.T, s *Store, rel string) string {
	t.Helper()
	got, err := s.ReadNote(rel)
	if err != nil {
		t.Fatalf("ReadNote(%q): %v", rel, err)
	}
	return got
}

// settle runs the content pass to completion.
func settle(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.ResolveContent(context.Background()); err != nil {
		t.Fatalf("ResolveContent: %v", err)
	}
}

// stored reads one column of one of a note's rows straight from the tables,
// sorted, going around the queries the app uses: those leave out locked notes
// with a join, and these tests check the tables themselves.
func stored(t *testing.T, s *Store, table, column, rel string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT `+column+` FROM `+table+
		` WHERE note_id = (SELECT id FROM notes WHERE path = ?) ORDER BY `+column, rel)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// derivedRows counts the rows in all three tables.
func derivedRows(t *testing.T, s *Store) int {
	t.Helper()
	total := 0
	for _, table := range derivedTables {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

func backlinks(t *testing.T, s *Store, rel string) []string {
	t.Helper()
	got, err := s.Backlinks(rel)
	if err != nil {
		t.Fatalf("Backlinks(%q): %v", rel, err)
	}
	return got
}

func withTag(t *testing.T, s *Store, tag string) []string {
	t.Helper()
	notes, err := s.NotesWithTag(tag)
	if err != nil {
		t.Fatalf("NotesWithTag(%q): %v", tag, err)
	}
	var out []string
	for _, n := range notes {
		out = append(out, n.Path)
	}
	return out
}

func TestDerivedRowsFollowTheNote(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Plan", "# Plan\n\n"+
		"See [[Alpha|the first]], [[Work/Beta#Goals]] and [[alpha]].\n\n"+
		"Tagged #Foo and #bar/baz, but not `#inline` or C#.\n\n"+
		"- [ ] pay rent <!-- priority:high due:2026-03-01 -->\n"+
		"- [x] done thing <!-- due:2026-03-02 -->\n"+
		"- [ ] no date\n"+
		"- [ ] loose date <!-- due:soon -->\n"+
		"```\n[[Hidden]] #hidden\n```\n")

	if got, want := stored(t, s, "note_links", "target", "Plan"), []string{"alpha", "work/beta"}; !slices.Equal(got, want) {
		t.Errorf("links = %v, want %v", got, want)
	}
	if got, want := stored(t, s, "note_tags", "tag", "Plan"), []string{"bar/baz", "foo"}; !slices.Equal(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
	due, err := s.DueTasks("2099-01-01")
	if err != nil {
		t.Fatal(err)
	}
	want := []DueTask{{Path: "Plan", Line: 6, Text: "pay rent", Due: "2026-03-01", Priority: "high"}}
	if !slices.Equal(due, want) {
		t.Errorf("due = %+v, want %+v", due, want)
	}

	// Editing replaces what was there, and does not add to it.
	saveNote(t, s, "Plan", "# Plan\n\n[[Gamma]] #new\n- [ ] later <!-- due:2026-04-01 -->\n")
	if got, want := stored(t, s, "note_links", "target", "Plan"), []string{"gamma"}; !slices.Equal(got, want) {
		t.Errorf("links after an edit = %v, want %v", got, want)
	}
	if got, want := stored(t, s, "note_tags", "tag", "Plan"), []string{"new"}; !slices.Equal(got, want) {
		t.Errorf("tags after an edit = %v, want %v", got, want)
	}
	due, _ = s.DueTasks("2099-01-01")
	want = []DueTask{{Path: "Plan", Line: 3, Text: "later", Due: "2026-04-01"}}
	if !slices.Equal(due, want) {
		t.Errorf("due after an edit = %+v, want %+v", due, want)
	}

	// Ticking the task off takes it out of the due list.
	saveNote(t, s, "Plan", "# Plan\n\n[[Gamma]] #new\n- [x] later <!-- due:2026-04-01 -->\n")
	if due, _ = s.DueTasks("2099-01-01"); len(due) != 0 {
		t.Errorf("a finished task is still due: %+v", due)
	}

	if err := s.DeleteNotePermanently("Plan"); err != nil {
		t.Fatal(err)
	}
	if n := derivedRows(t, s); n != 0 {
		t.Errorf("deleting the note left %d rows behind", n)
	}
}

// A note that goes away through its folder takes its rows with it, and a
// rename keeps them: the note is the same one.
func TestDerivedRowsSurviveRenamesAndGoWithFolders(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Old", "[[Target]] #kept\n")
	if err := s.RenameNote("Old", "Moved/New"); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, s, "note_tags", "tag", "Moved/New"); !slices.Equal(got, []string{"kept"}) {
		t.Errorf("a renamed note lost its tags: %v", got)
	}
	if err := s.DeleteFolderPermanently("Moved"); err != nil {
		t.Fatal(err)
	}
	if n := derivedRows(t, s); n != 0 {
		t.Errorf("deleting the folder left %d rows behind", n)
	}
}

func TestLockingTakesDerivedRowsOut(t *testing.T) {
	const (
		tag  = "zanzibartag"
		link = "quokkalink"
		task = "periwinklechore"
	)
	s := lockedStore(t)
	saveNote(t, s, "Public", "# Public\n\n#open\n")
	saveNote(t, s, "Secret", "# Secret\n\n#"+tag+" [[Public]] [["+link+"]]\n\n- [ ] "+task+" <!-- due:2026-05-01 -->\n")

	for _, w := range []string{tag, link, task} {
		if !dbFilesContain(t, s, w) {
			t.Fatalf("setup: %q should be in the database before locking, or this test proves nothing", w)
		}
	}
	if got := backlinks(t, s, "Public"); !slices.Equal(got, []string{"Secret"}) {
		t.Fatalf("setup: backlinks = %v", got)
	}

	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	for _, table := range derivedTables {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE note_id = (SELECT id FROM notes WHERE path = 'Secret')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("locking left %d rows in %s", n, table)
		}
	}
	if got := backlinks(t, s, "Public"); len(got) != 0 {
		t.Errorf("a locked note is a backlink: %v", got)
	}
	if tags, _ := s.Tags(); len(tags) != 1 || tags[0].Tag != "open" {
		t.Errorf("tags after locking = %v", tags)
	}
	if got := withTag(t, s, tag); len(got) != 0 {
		t.Errorf("a locked note has a tag: %v", got)
	}
	if due, _ := s.DueTasks("2099-01-01"); len(due) != 0 {
		t.Errorf("a locked note has a due task: %+v", due)
	}
	// The rows must be gone from the file, as the words are: someone with the
	// database and no password must not be able to read what the note says.
	for _, w := range []string{tag, link, task} {
		if dbFilesContain(t, s, w) {
			t.Errorf("locking left %q in the database file", w)
		}
	}

	// Unlocked, it is queued, and the content pass puts everything back.
	if err := s.UnlockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, s, "note_tags", "tag", "Secret"); len(got) != 0 {
		t.Errorf("unlocking alone filled the tables: %v", got)
	}
	settle(t, s)
	if got := stored(t, s, "note_tags", "tag", "Secret"); !slices.Equal(got, []string{tag}) {
		t.Errorf("tags after unlocking = %v", got)
	}
	if got := backlinks(t, s, "Public"); !slices.Equal(got, []string{"Secret"}) {
		t.Errorf("backlinks after unlocking = %v", got)
	}
	if due, _ := s.DueTasks("2099-01-01"); len(due) != 1 || due[0].Text != task {
		t.Errorf("due after unlocking = %+v", due)
	}
}

// However a note comes to be marked locked, the database drops its rows: the
// rule does not depend on the code that does the marking.
func TestLockedFlagAloneClearsDerivedRows(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Note", "#tag [[Other]]\n- [ ] task <!-- due:2026-01-01 -->\n")
	if derivedRows(t, s) == 0 {
		t.Fatal("setup: nothing was recorded")
	}
	if _, err := s.db.Exec(`UPDATE notes SET locked = 1 WHERE path = 'Note'`); err != nil {
		t.Fatal(err)
	}
	if n := derivedRows(t, s); n != 0 {
		t.Errorf("%d rows survived the note being marked locked", n)
	}
}

func TestNoteInALockedFolderHasNoDerivedRows(t *testing.T) {
	s := lockedStore(t)
	s.CreateFolder("Vault")
	if err := s.LockFolder("Vault"); err != nil {
		t.Fatal(err)
	}
	saveNote(t, s, "Vault/Born", "#birthtag [[Somewhere]]\n- [ ] chore <!-- due:2026-01-01 -->\n")
	if n := derivedRows(t, s); n != 0 {
		t.Errorf("a note born locked has %d rows", n)
	}
	settle(t, s)
	if n := derivedRows(t, s); n != 0 {
		t.Errorf("the content pass gave a locked note %d rows", n)
	}
}

func TestBacklinksPathAndBareLinks(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Work/Plan", "# Plan\n\nA note may link to itself: [[Work/Plan]].\n")
	saveNote(t, s, "Path", "[[Work/Plan]]")
	saveNote(t, s, "Bare", "[[Plan|the plan]]")
	saveNote(t, s, "Heading", "[[plan#Goals]]")
	saveNote(t, s, "Cased", "[[WORK/plan]]")
	saveNote(t, s, "Slashes", "[[/Work/Plan/]]")
	saveNote(t, s, "Elsewhere", "[[Other/Plan]]")
	saveNote(t, s, "Unrelated", "[[Nothing]] and [text](Work/Plan)")
	saveNote(t, s, "Code", "`[[Plan]]`\n```\n[[Work/Plan]]\n```\n")

	want := []string{"Bare", "Cased", "Heading", "Path", "Slashes"}
	if got := backlinks(t, s, "Work/Plan"); !slices.Equal(got, want) {
		t.Errorf("Backlinks(Work/Plan) = %v, want %v", got, want)
	}
	// Only a note that is there has backlinks: a bare name that resolves to
	// nothing means nothing.
	if got := backlinks(t, s, "Nothing"); len(got) != 0 {
		t.Errorf("Backlinks to a note that does not exist = %v", got)
	}
	if got := backlinks(t, s, "Path"); len(got) != 0 {
		t.Errorf("Backlinks(Path) = %v, want none", got)
	}
}

func TestBacklinksWithAnAmbiguousName(t *testing.T) {
	t.Run("nearest the root wins", func(t *testing.T) {
		s := testStore(t)
		saveNote(t, s, "Todo", "root")
		saveNote(t, s, "Work/Todo", "work")
		saveNote(t, s, "Bare", "[[Todo]]")
		saveNote(t, s, "Direct", "[[Work/Todo]]")
		if got := backlinks(t, s, "Todo"); !slices.Equal(got, []string{"Bare"}) {
			t.Errorf("Backlinks(Todo) = %v, want [Bare]", got)
		}
		if got := backlinks(t, s, "Work/Todo"); !slices.Equal(got, []string{"Direct"}) {
			t.Errorf("Backlinks(Work/Todo) = %v, want [Direct]", got)
		}
	})
	t.Run("then the first alphabetically", func(t *testing.T) {
		s := testStore(t)
		saveNote(t, s, "B/Todo", "b")
		saveNote(t, s, "A/Todo", "a")
		saveNote(t, s, "Lister", "[[todo]]")
		if got := backlinks(t, s, "A/Todo"); !slices.Equal(got, []string{"Lister"}) {
			t.Errorf("Backlinks(A/Todo) = %v, want [Lister]", got)
		}
		if got := backlinks(t, s, "B/Todo"); len(got) != 0 {
			t.Errorf("Backlinks(B/Todo) = %v, want none: a bare name means one note", got)
		}
	})
}

func TestNestedTags(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "N1", "#project")
	saveNote(t, s, "N2", "#project/atlas and #project")
	saveNote(t, s, "N3", "#project/atlas/ui")
	saveNote(t, s, "N4", "#projects")
	saveNote(t, s, "N5", "#Project/Atlas")
	for path, at := range map[string]int{"N3": 500, "N5": 400, "N2": 300, "N1": 200, "N4": 100} {
		if _, err := s.db.Exec(`UPDATE notes SET modified_at = ? WHERE path = ?`, at, path); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		tag  string
		want []string
	}{
		{"project", []string{"N3", "N5", "N2", "N1"}}, // newest first, N2 once
		{"#Project", []string{"N3", "N5", "N2", "N1"}},
		{"project/", []string{"N3", "N5", "N2", "N1"}},
		{"project/atlas", []string{"N3", "N5", "N2"}},
		{"projects", []string{"N4"}},
		{"proj", nil},
		{"#", nil},
		{"", nil},
	} {
		if got := withTag(t, s, c.tag); !slices.Equal(got, c.want) {
			t.Errorf("NotesWithTag(%q) = %v, want %v", c.tag, got, c.want)
		}
	}

	tags, err := s.Tags()
	if err != nil {
		t.Fatal(err)
	}
	want := []TagCount{{"project", 2}, {"project/atlas", 2}, {"project/atlas/ui", 1}, {"projects", 1}}
	if !slices.Equal(tags, want) {
		t.Errorf("Tags() = %v, want %v", tags, want)
	}
}

// A tag is text to be matched, not a pattern. Without escaping, the "_" in
// "a_b" would stand for any character and pull in the notes under "axb".
func TestTagsAreNotPatterns(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "N1", "#a_b/child")
	saveNote(t, s, "N2", "#axb/child")
	saveNote(t, s, "N3", "#a_b")

	got := withTag(t, s, "a_b")
	slices.Sort(got)
	if want := []string{"N1", "N3"}; !slices.Equal(got, want) {
		t.Errorf("NotesWithTag(a_b) = %v, want %v", got, want)
	}
	for _, wild := range []string{"%", "_", "a%", "a_", `a\`, "a_b%"} {
		if got := withTag(t, s, wild); len(got) != 0 {
			t.Errorf("NotesWithTag(%q) = %v, want none", wild, got)
		}
	}
}

func TestDueTasksOrder(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "B", "- [ ] b-late <!-- priority:high due:2026-03-05 -->\n"+
		"- [ ] b-plain <!-- due:2026-03-01 -->\n")
	saveNote(t, s, "A", "- [ ] a-low <!-- priority:low due:2026-03-01 -->\n"+
		"- [ ] a-medium <!-- priority:medium due:2026-03-01 -->\n"+
		"- [ ] a-high <!-- priority:high due:2026-03-01 -->\n"+
		"- [x] a-done <!-- priority:high due:2026-02-01 -->\n"+
		"- [ ] a-undated\n")
	saveNote(t, s, "C", "- [ ] c-second <!-- priority:high due:2026-03-01 -->\n"+
		"- [ ] c-first <!-- priority:high due:2026-03-01 -->\n")

	texts := func(through string) []string {
		tasks, err := s.DueTasks(through)
		if err != nil {
			t.Fatalf("DueTasks(%q): %v", through, err)
		}
		var out []string
		for _, d := range tasks {
			out = append(out, d.Text)
		}
		return out
	}

	all := []string{"a-high", "c-second", "c-first", "a-medium", "a-low", "b-plain", "b-late"}
	if got := texts("2026-03-05"); !slices.Equal(got, all) {
		t.Errorf("DueTasks(2026-03-05) = %v, want %v", got, all)
	}
	// The date is inclusive, and what is overdue is included.
	if got := texts("2026-03-01"); !slices.Equal(got, all[:6]) {
		t.Errorf("DueTasks(2026-03-01) = %v, want %v", got, all[:6])
	}
	if got := texts("2026-02-28"); len(got) != 0 {
		t.Errorf("DueTasks(2026-02-28) = %v, want none: the only earlier task is done", got)
	}

	tasks, _ := s.DueTasks("2026-03-01")
	if want := (DueTask{Path: "A", Line: 2, Text: "a-high", Due: "2026-03-01", Priority: "high"}); tasks[0] != want {
		t.Errorf("first task = %+v, want %+v", tasks[0], want)
	}
	if want := (DueTask{Path: "B", Line: 1, Text: "b-plain", Due: "2026-03-01"}); tasks[5] != want {
		t.Errorf("last task = %+v, want %+v", tasks[5], want)
	}
}

// An index from before these tables existed has nothing in them, and every
// note has to be read again to fill them.
func TestDerivedTablesAreBuiltForAnExistingVault(t *testing.T) {
	dir := t.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")
	s, err := Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	saveNote(t, s, "A", "#t [[B]]\n- [ ] x <!-- due:2026-01-01 -->\n")
	saveNote(t, s, "B", "plain")
	if s.PendingContent() != 0 {
		t.Fatal("setup: written notes should not be waiting to be read")
	}
	// Put the index back the way the last version left it.
	if _, err := s.db.Exec(`
		DROP TRIGGER derived_lock;
		DROP TABLE note_links; DROP TABLE note_tags; DROP TABLE note_due;`); err != nil {
		t.Fatalf("simulating the older schema: %v", err)
	}
	s.Close()

	s, err = Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PendingContent(); got != 2 {
		t.Fatalf("after the migration %d notes are queued, want 2", got)
	}
	if tags, _ := s.Tags(); len(tags) != 0 {
		t.Fatalf("the new tables were not empty: %v", tags)
	}
	settle(t, s)
	if tags, _ := s.Tags(); len(tags) != 1 || tags[0] != (TagCount{"t", 1}) {
		t.Errorf("Tags() after the content pass = %v", tags)
	}
	if got := backlinks(t, s, "B"); !slices.Equal(got, []string{"A"}) {
		t.Errorf("Backlinks(B) after the content pass = %v", got)
	}
	if due, _ := s.DueTasks("2026-01-01"); len(due) != 1 {
		t.Errorf("DueTasks after the content pass = %+v", due)
	}
	if got := s.PendingContent(); got != 0 {
		t.Errorf("the content pass left %d notes queued", got)
	}
	s.Close()

	// With the tables already there, opening the vault queues nothing.
	s, err = Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.PendingContent(); got != 0 {
		t.Errorf("reopening an up-to-date index queued %d notes", got)
	}
}

func TestRenameKeepsAliasesAndSkipsCode(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Old", "# Old\n")
	saveNote(t, s, "Ref", "See [[Old|the old one]] and [[Old#Goals]] and [[old#Goals|goals]].\n\n"+
		"`[[Old]]`\n\n```\n[[Old]]\n```\n\nafter")
	saveNote(t, s, "CodeOnly", "`[[Old]]` and\n```\n[[Old]]\n```\n")
	saveNote(t, s, "Other", "nothing to see")

	if err := s.RenameNote("Old", "New"); err != nil {
		t.Fatal(err)
	}
	n, err := s.UpdateLinksAfterRename("Old", "New")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d notes, want 1", n)
	}
	want := "See [[New|the old one]] and [[New#Goals]] and [[New#Goals|goals]].\n\n" +
		"`[[Old]]`\n\n```\n[[Old]]\n```\n\nafter"
	if got := readBack(t, s, "Ref"); got != want {
		t.Errorf("Ref = %q\nwant %q", got, want)
	}
	if got := readBack(t, s, "CodeOnly"); got != "`[[Old]]` and\n```\n[[Old]]\n```\n" {
		t.Errorf("a link inside code was rewritten: %q", got)
	}
	if got := backlinks(t, s, "New"); !slices.Equal(got, []string{"Ref"}) {
		t.Errorf("Backlinks(New) = %v: the index did not follow the rewrite", got)
	}
}

func TestRenameDoesNotCaptureASameNamedNote(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Todo", "root")
	saveNote(t, s, "Work/Todo", "work")
	saveNote(t, s, "Reader", "[[Todo]] [[Work/Todo]] [[todo|t]]")

	if err := s.RenameNote("Work/Todo", "Work/Tasks"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateLinksAfterRename("Work/Todo", "Work/Tasks"); err != nil || n != 1 {
		t.Fatalf("UpdateLinksAfterRename = %d, %v", n, err)
	}
	// The bare [[Todo]] meant the root note, and still does.
	if got, want := readBack(t, s, "Reader"), "[[Todo]] [[Work/Tasks]] [[todo|t]]"; got != want {
		t.Errorf("Reader = %q, want %q", got, want)
	}

	// The other way round: the bare name meant the note that moved, and follows
	// it to its new name.
	if err := s.RenameNote("Todo", "Inbox"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateLinksAfterRename("Todo", "Inbox"); err != nil || n != 1 {
		t.Fatalf("UpdateLinksAfterRename = %d, %v", n, err)
	}
	if got, want := readBack(t, s, "Reader"), "[[Inbox]] [[Work/Tasks]] [[Inbox|t]]"; got != want {
		t.Errorf("Reader = %q, want %q", got, want)
	}
}

func TestRenameWritesOnlyWhatChanged(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Old", "x")
	saveNote(t, s, "Ref", "[[Old]]")
	if _, err := s.db.Exec(`UPDATE notes SET modified_at = 1000`); err != nil {
		t.Fatal(err)
	}

	// Moving a note to a folder leaves its bare name meaning it, so the link is
	// already right and the note that holds it is not rewritten.
	if err := s.RenameNote("Old", "Archive/Old"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateLinksAfterRename("Old", "Archive/Old"); err != nil || n != 0 {
		t.Fatalf("UpdateLinksAfterRename = %d, %v, want 0", n, err)
	}
	var modified int64
	s.db.QueryRow(`SELECT modified_at FROM notes WHERE path = 'Ref'`).Scan(&modified)
	if modified != 1000 {
		t.Error("a note whose links were already right was saved anyway")
	}
}

func TestRenameMakesAnAmbiguousBareNameAPath(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Work/Plan", "mine")
	saveNote(t, s, "X/Plan", "theirs")
	saveNote(t, s, "Reader", "[[plan]] and [[Work/Plan]]")

	// Deeper down, "Plan" would mean X/Plan, so the link has to say where.
	if err := s.RenameNote("Work/Plan", "Deep/Er/Plan"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateLinksAfterRename("Work/Plan", "Deep/Er/Plan"); err != nil || n != 1 {
		t.Fatalf("UpdateLinksAfterRename = %d, %v", n, err)
	}
	if got, want := readBack(t, s, "Reader"), "[[Deep/Er/Plan]] and [[Deep/Er/Plan]]"; got != want {
		t.Errorf("Reader = %q, want %q", got, want)
	}
}

func TestRenameFollowsALinkToItself(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Old", "I am [[Old]].")
	if err := s.RenameNote("Old", "New"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateLinksAfterRename("Old", "New"); err != nil || n != 1 {
		t.Fatalf("UpdateLinksAfterRename = %d, %v", n, err)
	}
	if got := readBack(t, s, "New"); got != "I am [[New]]." {
		t.Errorf("New = %q", got)
	}
}

func TestRenameLeavesLockedNotesAlone(t *testing.T) {
	s := lockedStore(t)
	saveNote(t, s, "Old", "x")
	saveNote(t, s, "Open", "[[Old]]")
	saveNote(t, s, "Hidden", "[[Old]]")
	if err := s.LockNote("Hidden"); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameNote("Old", "New"); err != nil {
		t.Fatal(err)
	}
	n, err := s.UpdateLinksAfterRename("Old", "New")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d notes, want 1", n)
	}
	if got := readBack(t, s, "Open"); got != "[[New]]" {
		t.Errorf("Open = %q", got)
	}
	if got := readBack(t, s, "Hidden"); got != "[[Old]]" {
		t.Errorf("a locked note was rewritten: %q", got)
	}
	if !s.IsNoteLocked("Hidden") {
		t.Error("the locked note is no longer locked")
	}
}

func TestFolderRenameRewritesPathLinks(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Work/a", "A links [[Work/sub/b]] and [[b]].")
	saveNote(t, s, "Work/sub/b", "B")
	saveNote(t, s, "Workshop/x", "X")
	saveNote(t, s, "Index", "[[Work/a]] [[Work/sub/b|bee]] [[a]] [[Workshop/x]] [[Work/a#Top|top]]")
	saveNote(t, s, "Unrelated", "[[Workshop/x]] [[Elsewhere]]")

	if err := s.RenameFolder("Work", "Office"); err != nil {
		t.Fatal(err)
	}
	n, err := s.UpdateLinksAfterFolderRename("Work", "Office")
	if err != nil {
		t.Fatal(err)
	}
	// Index has several links to change and is rewritten once; Office/a, which
	// moved, has one to a note that moved with it.
	if n != 2 {
		t.Errorf("changed %d notes, want 2", n)
	}
	// Bare names still mean the same notes and stay bare. A folder called
	// Workshop is not Work.
	if got, want := readBack(t, s, "Index"), "[[Office/a]] [[Office/sub/b|bee]] [[a]] [[Workshop/x]] [[Office/a#Top|top]]"; got != want {
		t.Errorf("Index = %q\nwant %q", got, want)
	}
	if got, want := readBack(t, s, "Office/a"), "A links [[Office/sub/b]] and [[b]]."; got != want {
		t.Errorf("Office/a = %q\nwant %q", got, want)
	}
	if got := readBack(t, s, "Unrelated"); got != "[[Workshop/x]] [[Elsewhere]]" {
		t.Errorf("Unrelated = %q", got)
	}
	if got := backlinks(t, s, "Office/sub/b"); !slices.Equal(got, []string{"Index", "Office/a"}) {
		t.Errorf("Backlinks(Office/sub/b) = %v", got)
	}
}

// Renaming nothing is not an error, and a folder with no links into it costs
// nothing.
func TestUpdateLinksWithNothingToDo(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Lonely/a", "a")
	if n, err := s.UpdateLinksAfterRename("Ghost", "Spirit"); err != nil || n != 0 {
		t.Errorf("a rename of a note that is not there = %d, %v", n, err)
	}
	if n, err := s.UpdateLinksAfterRename("", ""); err != nil || n != 0 {
		t.Errorf("an empty rename = %d, %v", n, err)
	}
	if n, err := s.UpdateLinksAfterFolderRename("Lonely", "Lonely"); err != nil || n != 0 {
		t.Errorf("a folder renamed to itself = %d, %v", n, err)
	}
	if n, err := s.UpdateLinksAfterFolderRename("Nowhere", "Elsewhere"); err != nil || n != 0 {
		t.Errorf("a folder with nothing in it = %d, %v", n, err)
	}
}
