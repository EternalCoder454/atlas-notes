package storage

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
)

// Search inside notes.
//
// The index is SQLite's FTS5, in its contentless form: it keeps which words
// appear in which note and nothing else. The notes themselves are the only
// copy of their text, which keeps the database small and keeps it from being a
// second, uncompressed copy of the vault sitting beside the first.
//
// A locked note is never in it. The words of a note are most of what the note
// says, so indexing a locked one would leave a readable outline of it on disk
// next to the encrypted file. That rule is enforced by the database, not by
// remembering: the triggers below take a note out of the index the moment
// anything marks it locked, whichever code path does the marking, and put it
// back in the queue when it is unlocked.
//
// The index is filled in by ResolveContent, after the window is up and off
// the main thread. Writing a note keeps it current from then on, because the
// write path already has the text in hand.

// searchSchema creates the index and the triggers that keep it honest.
//
// The index's row id is the note's id. notes.id is an INTEGER PRIMARY KEY, so
// it survives VACUUM, and nothing in this package rewrites it: saves are
// upserts, and renames and moves are UPDATEs, which keep a note's id.
const searchSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
	body,
	content = '',
	contentless_delete = 1,
	tokenize = 'unicode61 remove_diacritics 2'
);

-- A deleted note leaves the index with its row, however it was deleted.
CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN
	DELETE FROM notes_fts WHERE rowid = old.id;
END;

-- A note that becomes locked leaves the index at once.
CREATE TRIGGER IF NOT EXISTS notes_fts_lock AFTER UPDATE OF locked ON notes
WHEN new.locked = 1 BEGIN
	DELETE FROM notes_fts WHERE rowid = new.id;
END;

-- A note that becomes unlocked goes back in the queue to be read.
CREATE TRIGGER IF NOT EXISTS notes_fts_unlock AFTER UPDATE OF locked ON notes
WHEN new.locked = 0 AND old.locked = 1 BEGIN
	UPDATE notes SET indexed = 0 WHERE id = new.id;
END;

-- The vault scan marks a note it saw change on disk as stale. What it says
-- has to be read again, so its place in the index does too.
CREATE TRIGGER IF NOT EXISTS notes_fts_stale AFTER UPDATE OF has_tasks ON notes
WHEN new.has_tasks = -2 BEGIN
	UPDATE notes SET indexed = 0 WHERE id = new.id;
END;
`

// indexedColumn records whether a note's words are in the index. 0 means it
// is waiting for ResolveContent.
const indexedColumn = `ALTER TABLE notes ADD COLUMN indexed INTEGER NOT NULL DEFAULT 0`

// migrateSearch adds the index to a database that does not have it. A new
// index is empty, so every note is queued for reading, whatever the column
// said: an index someone deleted by hand must be rebuilt, not trusted.
func (s *Store) migrateSearch() error {
	s.db.Exec(indexedColumn) // fails harmlessly when the column exists

	var existed int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'notes_fts'`).Scan(&existed); err != nil {
		return err
	}
	if _, err := s.db.Exec(searchSchema); err != nil {
		return err
	}
	if existed == 0 {
		if _, err := s.db.Exec(`UPDATE notes SET indexed = 0`); err != nil {
			return err
		}
	}
	return nil
}

// purgeRemovedWords makes the words the index has let go of unrecoverable
// from the database file, and is run whenever a note is locked.
//
// Taking a note out of the index is not the same as taking its words out of
// the file. A contentless FTS5 index deletes by writing a tombstone, and the
// original entry stays in its segment until the segment is merged; SQLite then
// frees the pages that held it without clearing them; and the WAL keeps its
// own copies of those pages until a checkpoint. Any one of those leaves a
// locked note's words readable to anything that can read index.db. So: the
// index is optimised, which rewrites it without the tombstoned entries; the
// connection runs with secure_delete on, so the pages that frees are zeroed;
// and the WAL is checkpointed and truncated.
//
// Locking is rare and explicit, so the cost of rewriting the index lands at a
// moment someone has just asked for something to happen.
func (s *Store) purgeRemovedWords() error {
	if _, err := s.db.Exec(`INSERT INTO notes_fts(notes_fts) VALUES('optimize')`); err != nil {
		return err
	}
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// indexContent puts one note's words in the index and marks it done. It runs
// inside the caller's transaction, and does nothing for a locked note.
func indexContent(tx *sql.Tx, id int64, body string) error {
	if _, err := tx.Exec(`INSERT OR REPLACE INTO notes_fts(rowid, body) VALUES (?, ?)`, id, body); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE notes SET indexed = 1 WHERE id = ?`, id)
	return err
}

// indexWritten updates the index for a note the app has just written, from
// the text it wrote. The caller holds writeMu.
func (s *Store) indexWritten(rel, body string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	var locked int
	err = tx.QueryRow(`SELECT id, locked FROM notes WHERE path = ?`, rel).Scan(&id, &locked)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if locked != 0 {
		return nil // the triggers have already taken it out
	}
	if err := indexContent(tx, id, body); err != nil {
		return err
	}
	return tx.Commit()
}

// resolveReadHook, when set, runs between reading a batch and writing it. It
// exists so a test can put a save exactly where a real one could land.
var resolveReadHook func()

// resolveBatch is how many notes ResolveContent reads before writing what it
// found. Reading happens with no lock held; only the write holds one, and it
// is short enough that a save made meanwhile waits a few milliseconds at most.
const resolveBatch = 64

// ResolveContent reads every note whose content has not been looked at since
// it last changed, and records what it found: whether it is a checklist, and
// its words in the search index. It returns how many notes it read.
//
// It is built to run off the main thread while the app is in use. Files are
// read and decompressed with no lock held, and each batch is written in a
// short transaction. A batch only writes to a note that is still waiting: if
// the note was saved, locked, renamed or deleted while it was being read, the
// write path has already dealt with it, and what was read is thrown away
// rather than written over something newer.
//
// It stops between batches when ctx is cancelled, and leaves the rest queued
// for next time. A note that cannot be read is recorded as unknown rather than
// retried forever.
func (s *Store) ResolveContent(ctx context.Context) (int, error) {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, nil
		}
		type job struct {
			id    int64
			rel   string
			body  string
			tasks int
			read  bool
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, path FROM notes
			WHERE locked = 0 AND (has_tasks = ? OR indexed = 0)
			ORDER BY id LIMIT ?`, tasksStale, resolveBatch)
		if err != nil {
			if ctx.Err() != nil {
				return total, nil
			}
			return total, err
		}
		var jobs []job
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.id, &j.rel); err != nil {
				rows.Close()
				return total, err
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		if len(jobs) == 0 {
			return total, nil
		}

		for i := range jobs {
			j := &jobs[i]
			j.tasks = tasksUnknown
			out, err := s.readPlain(j.rel)
			if err != nil {
				continue
			}
			j.body, j.read = string(out), true
			j.tasks = boolToInt(HasTasks(j.body))
		}
		if resolveReadHook != nil {
			resolveReadHook()
		}

		if err := s.writeResolved(ctx, func(tx *sql.Tx) error {
			for _, j := range jobs {
				res, err := tx.Exec(`
					UPDATE notes SET has_tasks = ?, indexed = 1
					WHERE id = ? AND locked = 0 AND (has_tasks = ? OR indexed = 0)`,
					j.tasks, j.id, tasksStale)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 || !j.read {
					continue // dealt with elsewhere meanwhile, or unreadable
				}
				if _, err := tx.Exec(`INSERT OR REPLACE INTO notes_fts(rowid, body) VALUES (?, ?)`, j.id, j.body); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			if ctx.Err() != nil {
				return total, nil
			}
			return total, err
		}
		total += len(jobs)
	}
}

// writeResolved runs one batch's writes in a transaction under writeMu.
func (s *Store) writeResolved(ctx context.Context, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveTaskFlags is ResolveContent run to completion. It keeps its name
// because it is what the rest of the package, and its tests, have always
// called to settle a vault.
func (s *Store) ResolveTaskFlags() (int, error) {
	return s.ResolveContent(context.Background())
}

// PendingContent reports how many notes are still waiting to be read, so the
// interface can say that a search may not be complete yet.
func (s *Store) PendingContent() int {
	var n int
	s.db.QueryRow(`SELECT count(*) FROM notes WHERE locked = 0 AND (has_tasks = ? OR indexed = 0)`, tasksStale).Scan(&n)
	return n
}

// SearchContent returns the notes whose text contains every word of query, at
// most limit of them, in no particular order: the caller knows what it wants
// them sorted by. Words match from their beginning, so a search typed a letter
// at a time finds things before the word is finished; case and accents are
// ignored. Locked notes are never returned.
//
// It does not rank by relevance. Ranking scores every match before the limit
// applies, which at 10,000 notes made a three-letter search take 25 ms, and a
// search box calls this as someone types. Unranked, the same search takes
// under 7 ms, and the index stays half the size that prefix indexes, the other
// way to make it fast, would need.
func (s *Store) SearchContent(query string, limit int) ([]string, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT n.path FROM notes_fts
		JOIN notes n ON n.id = notes_fts.rowid
		WHERE notes_fts MATCH ? AND n.locked = 0
		LIMIT ?`, q, limit)
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

// minSearchRunes is the shortest word the index is asked about. Two letters
// match the start of nearly every note in a large vault: at 10,000 notes a
// two-letter search found almost all of them and took 18 ms to say so. Names
// are still searched from the first letter; this is only the text.
const minSearchRunes = 3

// ftsQuery turns what someone typed into an FTS5 query: every word must
// appear, each as a prefix. Everything is quoted, so nothing typed can be read
// as query syntax. A search for AND, NEAR, a quote or a colon is a search for
// that text, not an instruction, and a stray quote cannot make the query fail.
func ftsQuery(input string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(input, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len([]rune(w)) < minSearchRunes {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}
