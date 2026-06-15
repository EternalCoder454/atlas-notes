// Package storage is the local-first persistence layer: zstd-compressed markdown
// files in a vault directory, indexed by an embedded (CGO-free) SQLite database.
// It has no UI dependency.
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

// Store is the storage layer: vault file I/O plus the SQLite index. It is safe
// to use from the GTK main thread; all SQLite access is serialized onto a single
// connection.
type Store struct {
	VaultPath string
	DBPath    string

	db  *sql.DB
	enc *zstd.Encoder
	dec *zstd.Decoder

	// writeMu serializes mutating operations (note/folder writes, renames,
	// deletes) so they are safe to call from a background goroutine — e.g. the
	// app's async autosave — without overlapping the filesystem/index steps.
	// Reads (ReadNote/List*) don't take it: atomic renames keep files consistent
	// and database/sql serializes the single SQLite connection.
	writeMu sync.Mutex
}

// Open initializes the vault directory, opens and migrates the SQLite index, and
// prepares the zstd codecs. Empty vaultPath/dbPath fall back to the XDG defaults.
func Open(vaultPath, dbPath string) (*Store, error) {
	if vaultPath == "" {
		vaultPath = DefaultVaultPath()
	}
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		return nil, fmt.Errorf("create vault: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Level 3 ("SpeedDefault" in klauspost terms): fast with a good ratio.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		enc.Close()
		return nil, err
	}

	// synchronous=NORMAL is safe under WAL (consistent, durable across app
	// crashes; only the last transaction can be lost on power loss) and keeps
	// fsync off the writer path so a slow disk can't stall the UI. The index is a
	// rebuildable cache (the .md.zst files are the source of truth), so even that
	// edge case is recoverable via Reindex.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		enc.Close()
		dec.Close()
		return nil, err
	}
	// SQLite serializes writers; a single connection avoids "database is locked".
	db.SetMaxOpenConns(1)

	s := &Store{VaultPath: vaultPath, DBPath: dbPath, db: db, enc: enc, dec: dec}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database and codec resources, waiting for any in-flight
// write to finish first.
func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.enc != nil {
		s.enc.Close()
	}
	if s.dec != nil {
		s.dec.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS notes (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	path         TEXT    NOT NULL UNIQUE,
	folder       TEXT    NOT NULL DEFAULT '',
	title        TEXT    NOT NULL DEFAULT '',
	modified_at  INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL DEFAULT 0,
	is_checklist INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS checklist_items (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	note_id    INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
	text       TEXT    NOT NULL DEFAULT '',
	priority   TEXT    NOT NULL DEFAULT '',
	due_date   TEXT    NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 0,
	checked    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_notes_folder ON notes(folder);
CREATE INDEX IF NOT EXISTS idx_items_note ON checklist_items(note_id, sort_order);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Older databases predate created_at; add and backfill it. Errors are
	// ignored because the column already exists on up-to-date databases.
	s.db.Exec(`ALTER TABLE notes ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`UPDATE notes SET created_at = modified_at WHERE created_at = 0`)
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
