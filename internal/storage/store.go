// Package storage is the local-first persistence layer: zstd-compressed markdown
// files in a vault directory, indexed by an embedded (CGO-free) SQLite database.
// It has no UI dependency.
package storage

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"atlas-notes/internal/vaultlock"
)

// encoderWorkers caps the compressor's worker pool (see Open).
const encoderWorkers = 2

// maxNoteBytes bounds how large a note may be once decompressed.
const maxNoteBytes = 128 << 20 // 128 MiB

// Store is the storage layer: vault file I/O plus the SQLite index. It is safe
// to use from the GTK main thread; all SQLite access is serialized onto a single
// connection.
type Store struct {
	VaultPath string

	db  *sql.DB
	enc *zstd.Encoder
	dec *zstd.Decoder

	// lockMu guards the vault's password state. It is separate from writeMu
	// because IsUnlocked is asked on every note row the tree draws, while
	// writeMu is held for the length of a file write.
	lockMu  sync.Mutex
	lockCfg *vaultlock.Config
	lockKey vaultlock.Key

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
	//
	// The compressor's worker pool is capped. Writing a note is a background
	// operation on a few kilobytes, so extra workers buy nothing, while the
	// library allocates per-worker state for every CPU on the machine — over
	// 40 MB of heap on a 32-core desktop, for a note-taking app.
	//
	// The decompressor keeps the library default. Capping that too was measured
	// and rejected: it made opening a note about twice as slow, and that is the
	// one path the user waits on.
	workers := runtime.GOMAXPROCS(0)
	if workers > encoderWorkers {
		workers = encoderWorkers
	}
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(workers))
	if err != nil {
		return nil, err
	}
	// Cap what a single note may decompress to. The vault is the user's own
	// directory, but a note can arrive from a synced folder or a backup, and an
	// unbounded DecodeAll turns a few kilobytes of hostile input into an
	// out-of-memory kill. No real note comes close to this.
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxNoteBytes))
	if err != nil {
		enc.Close()
		return nil, err
	}

	// synchronous=NORMAL is safe under WAL (consistent, durable across app
	// crashes; only the last transaction can be lost on power loss) and keeps
	// fsync off the writer path so a slow disk can't stall the UI. The index is a
	// rebuildable cache (the .md.zst files are the source of truth), so even that
	// edge case is recoverable via Reindex.
	// cache_size is negative to mean KiB rather than pages; 8 MiB comfortably
	// holds the index of a large vault, so browsing never goes back to disk.
	// temp_store=memory keeps sorts (ORDER BY folder, title) out of the file.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)" +
		"&_pragma=synchronous(normal)&_pragma=foreign_keys(on)" +
		"&_pragma=cache_size(-8000)&_pragma=temp_store(memory)"
	db, err := openIndex(dsn)
	if err != nil {
		enc.Close()
		dec.Close()
		return nil, err
	}

	s := &Store{VaultPath: vaultPath, db: db, enc: enc, dec: dec}
	// A vault with no locked notes has no lock file, which is not an error.
	// One that cannot be read is: it would leave locked notes unopenable while
	// the app behaved as though nothing were wrong.
	if cfg, lerr := s.loadLockConfig(); lerr != nil {
		log.Printf("atlas-notes: vault lock configuration: %v", lerr)
	} else {
		s.lockCfg = cfg
	}
	if err := s.migrate(); err != nil {
		// The index is a cache; the notes on disk are the source of truth. A
		// database that cannot be opened — truncated by a full disk, damaged by
		// a sync client, written by a newer version — used to leave the app
		// with no vault at all and no way out but deleting the file by hand.
		// Move it aside and start a fresh one instead; the next scan refills it.
		db.Close()
		if qerr := quarantineIndex(dbPath); qerr != nil {
			s.enc.Close()
			s.dec.Close()
			return nil, fmt.Errorf("index unusable (%w) and could not be replaced: %w", err, qerr)
		}
		log.Printf("atlas-notes: index was unusable (%v); rebuilding it from the vault", err)
		if db, err = openIndex(dsn); err != nil {
			s.enc.Close()
			s.dec.Close()
			return nil, err
		}
		s.db = db
		if err := s.migrate(); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// openIndex opens the SQLite index with a single connection: SQLite serializes
// writers, and one connection avoids "database is locked".
func openIndex(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// quarantineIndex moves a damaged index (and its journal files) out of the way,
// keeping it for inspection rather than deleting it.
func quarantineIndex(dbPath string) error {
	suffix := time.Now().Format(".broken-20060102-150405")
	if err := os.Rename(dbPath, dbPath+suffix); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, side := range []string{"-wal", "-shm"} {
		os.Remove(dbPath + side)
	}
	return nil
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
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	path        TEXT    NOT NULL UNIQUE,
	folder      TEXT    NOT NULL DEFAULT '',
	modified_at INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_notes_folder ON notes(folder);
`

// lockedColumn is added separately: a vault indexed by an earlier version has
// the table already, and ALTER TABLE is how it gains the column.
const lockedColumn = `ALTER TABLE notes ADD COLUMN locked INTEGER NOT NULL DEFAULT 0`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Older databases predate created_at; add and backfill it. Errors are
	// ignored because the column already exists on up-to-date databases.
	s.db.Exec(`ALTER TABLE notes ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`UPDATE notes SET created_at = modified_at WHERE created_at = 0`)
	// Likewise for the locked flag. The vault scan fills it in from what is
	// actually on disk, so an older index needs no backfill here.
	s.db.Exec(lockedColumn)
	return nil
}
