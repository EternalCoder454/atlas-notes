package storage

import (
	"database/sql"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/markup"
)

// What the index knows about a note beyond its words.
//
// Three small tables record what a note's text says about other things: which
// notes it links to, which tags it carries, and which of its unfinished
// checklist items are due. They are what lets the app answer "what links here",
// "what is tagged this" and "what is due" without opening every note.
//
// They are filled from the same text, at the same moment, as the search index
// (see indexContent), so they cannot drift from it. They are a cache like the
// rest of the index: the notes are the only source of truth, and a table that
// goes missing is refilled from them.
//
// A locked note has no rows, for the reason it has no words in the search index:
// its tags and the names it links to describe what it says. The trigger below
// enforces that in the database, whichever code path marks a note locked.

// derivedSchema creates the tables. A note's rows go when the note does, through
// the foreign keys, and when it becomes locked, through the trigger.
//
// Each table is indexed by what it is looked up by and also by note_id: a
// note's rows are replaced on every save, and without the second index every
// save would read the whole table to find them.
const derivedSchema = `
CREATE TABLE IF NOT EXISTS note_links (
	note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	target  TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_note_links_target ON note_links(target);
CREATE INDEX IF NOT EXISTS idx_note_links_note   ON note_links(note_id);

CREATE TABLE IF NOT EXISTS note_tags (
	note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	tag     TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_note_tags_tag  ON note_tags(tag);
CREATE INDEX IF NOT EXISTS idx_note_tags_note ON note_tags(note_id);

CREATE TABLE IF NOT EXISTS note_due (
	note_id  INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	line     INTEGER NOT NULL,
	text     TEXT    NOT NULL,
	due      TEXT    NOT NULL,
	priority TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_note_due_due  ON note_due(due);
CREATE INDEX IF NOT EXISTS idx_note_due_note ON note_due(note_id);

-- A note that becomes locked leaves all three at once.
CREATE TRIGGER IF NOT EXISTS derived_lock AFTER UPDATE OF locked ON notes
WHEN new.locked = 1 BEGIN
	DELETE FROM note_links WHERE note_id = new.id;
	DELETE FROM note_tags  WHERE note_id = new.id;
	DELETE FROM note_due   WHERE note_id = new.id;
END;
`

// derivedTables are the tables deriveContent keeps, named once so the delete
// and the schema cannot disagree about what a note's rows are.
var derivedTables = []string{"note_links", "note_tags", "note_due"}

// migrateDerived adds the tables to a database that does not have them. New
// tables are empty, so every note is queued for reading, exactly as when the
// search index is new. The content pass writes the search index with a replace,
// so reading a note that is already in it is safe.
func (s *Store) migrateDerived() error {
	var existing int
	if err := s.db.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('note_links', 'note_tags', 'note_due')`).Scan(&existing); err != nil {
		return err
	}
	if _, err := s.db.Exec(derivedSchema); err != nil {
		return err
	}
	if existing < len(derivedTables) {
		if _, err := s.db.Exec(`UPDATE notes SET indexed = 0`); err != nil {
			return err
		}
	}
	return nil
}

// deriveContent replaces one note's links, tags and due tasks with what its text
// says now. It runs inside the caller's transaction, beside the search index
// write, and is only ever called for a note that is not locked.
func deriveContent(tx *sql.Tx, id int64, body string) error {
	for _, table := range derivedTables {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE note_id = ?`, id); err != nil {
			return err
		}
	}

	sum := markup.Summarize(body)
	// Targets are stored the way Resolve reads them: lowercased, without the
	// slashes around them. Summarize keeps "/a" and "a" apart, so two links can
	// land on one target here, and each target is stored once.
	seen := map[string]bool{}
	for _, l := range sum.Links {
		target := linkKey(l)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		if _, err := tx.Exec(`INSERT INTO note_links(note_id, target) VALUES (?, ?)`, id, target); err != nil {
			return err
		}
	}
	for _, tag := range sum.Tags {
		if _, err := tx.Exec(`INSERT INTO note_tags(note_id, tag) VALUES (?, ?)`, id, tag); err != nil {
			return err
		}
	}

	for n, line := range strings.Split(body, "\n") {
		it, ok := checklist.ParseLine(line)
		if !ok || it.Checked || !validDate(it.DueDate) {
			continue
		}
		// A priority that is not one of the known ones sorts as none, so it is
		// stored as none rather than as text the interface has no word for.
		priority := it.Priority
		if !priority.Valid() {
			priority = checklist.PriorityNone
		}
		if _, err := tx.Exec(
			`INSERT INTO note_due(note_id, line, text, due, priority) VALUES (?, ?, ?, ?, ?)`,
			id, n, it.Text, it.DueDate, string(priority)); err != nil {
			return err
		}
	}
	return nil
}

// validDate reports whether s is a calendar date in the form yyyy-mm-dd. Due
// dates are compared as text, which is only right for dates written in exactly
// that form, so a due date in any other form is not treated as one.
func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// linkKey is how a link target is stored and looked up. It is lowercased by Go
// rather than by SQLite, whose lower() only folds ASCII: a link to [[Über]] has
// to match the note however it is capitalised, and it is Go's rule that
// markup.Resolve applies when it compares names.
func linkKey(target string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(target)), "/")
}

// linkKeysFor are the stored targets a link to rel can have: its path, and its
// bare name.
func linkKeysFor(rel string) []string {
	full, base := linkKey(rel), linkKey(path.Base(rel))
	if full == base {
		return []string{full}
	}
	return []string{full, base}
}

// notePaths lists every note's path, locked ones included: a link to a locked
// note still names it, and a bare name has to be resolved against all of them.
func (s *Store) notePaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM notes ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// maxTargetsPerQuery keeps the number of parameters in one statement well under
// the smallest limit SQLite has ever shipped with. A folder holding thousands of
// notes has more link targets than that.
const maxTargetsPerQuery = 500

// notesLinkingTo finds the unlocked notes with a link to any of targets, and
// says which of them each one has. It is the candidate step of everything that
// asks who links where: the answer is a superset, because a stored target is
// only text, and only the caller knows what it resolves to.
func (s *Store) notesLinkingTo(targets []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	targets = slices.Compact(slices.Sorted(slices.Values(targets)))
	for len(targets) > 0 {
		n := min(len(targets), maxTargetsPerQuery)
		args := make([]any, n)
		for i, t := range targets[:n] {
			args[i] = t
		}
		targets = targets[n:]

		rows, err := s.db.Query(`
			SELECT n.path, l.target FROM note_links l
			JOIN notes n ON n.id = l.note_id
			WHERE n.locked = 0 AND l.target IN (?`+strings.Repeat(",?", n-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p, target string
			if err := rows.Scan(&p, &target); err != nil {
				rows.Close()
				return nil, err
			}
			if out[p] == nil {
				out[p] = map[string]bool{}
			}
			out[p][target] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Backlinks lists the notes that link to rel, sorted, not counting rel itself.
//
// A link written as a path means rel when it is rel, whatever the case. A link
// written as a bare name means rel only if markup.Resolve, given every note,
// says so: with two notes called "Todo", a link to [[Todo]] is a link to one of
// them, and listing it under both would be wrong. The database narrows the
// candidates by text and this decides what the text means.
func (s *Store) Backlinks(rel string) ([]string, error) {
	rel = normalizeRel(rel)
	if rel == "" {
		return nil, nil
	}
	found, err := s.notesLinkingTo(linkKeysFor(rel))
	if err != nil {
		return nil, err
	}
	delete(found, rel)
	if len(found) == 0 {
		return nil, nil
	}
	all, err := s.notePaths()
	if err != nil {
		return nil, err
	}

	full := linkKey(rel)
	meaning := map[string]bool{}
	means := func(target string) bool {
		if m, ok := meaning[target]; ok {
			return m
		}
		var m bool
		if strings.Contains(target, "/") {
			m = target == full
		} else {
			m = strings.EqualFold(markup.Resolve(target, all), rel)
		}
		meaning[target] = m
		return m
	}

	var out []string
	for p, targets := range found {
		for t := range targets {
			if means(t) {
				out = append(out, p)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// TagCount is a tag and how many notes carry it.
type TagCount struct {
	Tag   string // lowercased, without the "#"
	Notes int
}

// Tags lists every tag in the vault, sorted, with how many notes carry each. A
// nested tag is its own entry: "project/atlas" is counted as itself and not
// added to "project".
func (s *Store) Tags() ([]TagCount, error) {
	rows, err := s.db.Query(`
		SELECT t.tag, count(*) FROM note_tags t
		JOIN notes n ON n.id = t.note_id
		WHERE n.locked = 0
		GROUP BY t.tag ORDER BY t.tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var c TagCount
		if err := rows.Scan(&c.Tag, &c.Notes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotesWithTag lists the notes that carry a tag, newest modified first. The tag
// may be written with its "#" and in any case. A note carries "project" when it
// has that tag or one nested under it, "project/atlas", but not "projects",
// which is a different word.
func (s *Store) NotesWithTag(tag string) ([]NoteMeta, error) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.TrimRight(strings.TrimPrefix(tag, "#"), "/")
	if tag == "" {
		return nil, nil
	}
	// The tag is text to be matched, not a pattern: an underscore in a tag name
	// must not stand for any character, and nothing typed may be a wildcard.
	rows, err := s.db.Query(`
		SELECT path, folder, modified_at, created_at, locked, has_tasks FROM notes
		WHERE locked = 0 AND id IN (
			SELECT note_id FROM note_tags WHERE tag = ? OR tag LIKE ? ESCAPE '\')
		ORDER BY modified_at DESC, path`, tag, escapeLike(tag)+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteMeta
	for rows.Next() {
		var m NoteMeta
		var modified, created int64
		var locked, hasTasks int
		if err := rows.Scan(&m.Path, &m.Folder, &modified, &created, &locked, &hasTasks); err != nil {
			return nil, err
		}
		m.ModifiedAt = time.Unix(modified, 0)
		m.CreatedAt = time.Unix(created, 0)
		m.Locked = locked != 0
		m.HasTasks = hasTasks > 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// escapeLike escapes the characters LIKE treats as wildcards, for a pattern
// used with ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// DueTask is one unfinished checklist item that has a due date.
type DueTask struct {
	Path     string // the note it is in
	Line     int    // 0-based line in that note
	Text     string
	Due      string // yyyy-mm-dd
	Priority string // "high", "medium", "low", or "" for none
}

// DueTasks lists every unfinished task due on or before through, given as
// yyyy-mm-dd, so overdue ones are included. It is ordered by due date, then
// priority with the most urgent first, then by where the task is.
func (s *Store) DueTasks(through string) ([]DueTask, error) {
	rows, err := s.db.Query(`
		SELECT n.path, d.line, d.text, d.due, d.priority FROM note_due d
		JOIN notes n ON n.id = d.note_id
		WHERE n.locked = 0 AND d.due <= ?
		ORDER BY d.due,
			CASE d.priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END,
			n.path, d.line`, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueTask
	for rows.Next() {
		var t DueTask
		if err := rows.Scan(&t.Path, &t.Line, &t.Text, &t.Due, &t.Priority); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// move is one note's path before and after a rename.
type move struct{ from, to string }

// RenameNoteAndLinks renames a note and then rewrites the links in other notes
// that named it, returning how many notes that changed. It is what renaming
// from the interface calls, so that renaming a note never breaks a link to it.
// The rename is what matters: if it succeeds and a link could not be
// rewritten, the error says so but the note has still been renamed.
func (s *Store) RenameNoteAndLinks(oldRel, newRel string) (int, error) {
	if err := s.RenameNote(oldRel, newRel); err != nil {
		return 0, err
	}
	return s.UpdateLinksAfterRename(oldRel, newRel)
}

// RenameFolderAndLinks is RenameNoteAndLinks for a folder.
func (s *Store) RenameFolderAndLinks(oldRel, newRel string) (int, error) {
	if err := s.RenameFolder(oldRel, newRel); err != nil {
		return 0, err
	}
	return s.UpdateLinksAfterFolderRename(oldRel, newRel)
}

// UpdateLinksAfterRename rewrites the links in other notes that named oldRel so
// they name newRel, and returns how many notes it changed. It is called after
// RenameNote has succeeded, and reads the vault as it is now.
//
// A note that is open in an editor is read from disk like the rest, so a caller
// with unsaved edits in one has to save it first, and reload it afterwards.
func (s *Store) UpdateLinksAfterRename(oldRel, newRel string) (int, error) {
	oldRel, newRel = normalizeRel(oldRel), normalizeRel(newRel)
	if oldRel == "" || newRel == "" || oldRel == newRel {
		return 0, nil
	}
	after, err := s.notePaths()
	if err != nil {
		return 0, err
	}
	// A note the index does not have was not renamed as far as the index can
	// tell, and there are no links to it to follow.
	i := slices.Index(after, newRel)
	if i < 0 {
		return 0, nil
	}
	before := slices.Clone(after)
	before[i] = oldRel
	return s.rewriteLinks([]move{{oldRel, newRel}}, before, after)
}

// UpdateLinksAfterFolderRename is UpdateLinksAfterRename for a folder: every
// note that was under oldFolder is now under newFolder, and the links to any of
// them are rewritten in one pass. A note that links to several of them is
// changed once.
func (s *Store) UpdateLinksAfterFolderRename(oldFolder, newFolder string) (int, error) {
	oldFolder, newFolder = normalizeRel(oldFolder), normalizeRel(newFolder)
	if oldFolder == "" || newFolder == "" || oldFolder == newFolder {
		return 0, nil
	}
	after, err := s.notePaths()
	if err != nil {
		return 0, err
	}
	// The renamed folder's notes are the ones under its new name. Nothing was
	// under that name before, because a folder cannot be renamed onto one that
	// has notes in it.
	prefix := newFolder + "/"
	before := slices.Clone(after)
	var moves []move
	for i, p := range after {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			before[i] = oldFolder + "/" + rest
			moves = append(moves, move{before[i], p})
		}
	}
	if len(moves) == 0 {
		return 0, nil
	}
	return s.rewriteLinks(moves, before, after)
}

// rewriteLinks carries out renames on the notes that link to what moved. before
// and after are the whole vault's note lists either side of all of them, so
// what a bare name meant is judged against the vault as a whole, not one move
// at a time.
//
// Each linking note is read once and written once, and only if its text changed:
// a link that was already right, a bare name that still names the note, is left
// alone, and the note is not made to look edited. Locked notes are not in the
// link index and cannot be read without the key, so they are not touched.
//
// A note that cannot be read or written does not stop the others. The first
// failure is reported when the rest are done, as ConvertVault does.
func (s *Store) rewriteLinks(moves []move, before, after []string) (int, error) {
	var keys []string
	for _, m := range moves {
		keys = append(keys, linkKeysFor(m.from)...)
	}
	linking, err := s.notesLinkingTo(keys)
	if err != nil {
		return 0, err
	}
	paths := make([]string, 0, len(linking))
	for p := range linking {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	changed, failed := 0, 0
	var first error
	fail := func(p string, err error) {
		failed++
		if first == nil {
			first = fmt.Errorf("%s: %w", p, err)
		}
	}
	for _, p := range paths {
		text, err := s.ReadNote(p)
		if err != nil {
			fail(p, err)
			continue
		}
		out := text
		for _, m := range moves {
			// Only the moves this note links to are applied: rewriting is a
			// scan of every line and of the whole vault's names.
			if !slices.ContainsFunc(linkKeysFor(m.from), func(k string) bool { return linking[p][k] }) {
				continue
			}
			out, _ = markup.RewriteLinks(out, m.from, m.to, before, after)
		}
		if out == text {
			continue
		}
		if err := s.WriteNote(p, out); err != nil {
			fail(p, err)
			continue
		}
		changed++
	}
	if first != nil {
		return changed, fmt.Errorf("%d notes could not be updated, the first: %w", failed, first)
	}
	return changed, nil
}
