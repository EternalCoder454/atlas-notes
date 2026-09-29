package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"atlas-notes/internal/vaultlock"
)

// Earlier versions of a note are kept in Store.HistoryDir, outside the vault,
// so that they stay on this device: history is a private convenience, and a
// synced vault would otherwise copy every version of every note to every other
// machine.
//
// The layout is <HistoryDir>/<key>/<id>. The key is the first 16 hex digits of
// the SHA-256 of the note's vault-relative name, so a name needs no escaping
// however odd it is, and note.txt in the same directory says which note the
// versions belong to. A version's id is the time its content was saved, in unix
// nanoseconds, followed by the extension of the file it was copied from. The
// bytes are copied as they are: a version of a note in a compressed format
// stays compressed, and a version of a locked note stays encrypted, so history
// never holds anything in the clear that the vault did not.

const (
	// historyMinGap is how close together two versions may be. Autosave writes
	// a note every few seconds, and a version for each would push everything of
	// interest out of the list within minutes.
	historyMinGap = 10 * time.Minute
	// historyMaxAge and historyMaxVersions bound how much a note keeps. The age
	// limit is applied first.
	historyMaxAge      = 90 * 24 * time.Hour
	historyMaxVersions = 50

	// historyNoteFile names, inside a note's history directory, the note it is
	// for. The directory name is a hash, so without this it could not be traced
	// back, and a rename could not tell which directory is whose.
	historyNoteFile = "note.txt"
)

// historyNow is the clock history uses to judge age, a variable so that tests
// can move it rather than wait ten minutes, or ninety days.
var historyNow = time.Now

var (
	errNoHistory  = errors.New("history is not being kept")
	errBadVersion = errors.New("not a version of this note")
)

// Version is one earlier state of a note.
type Version struct {
	ID     string    // names the version to ReadVersion and RestoreVersion
	Time   time.Time // when this content was saved
	Size   int64     // bytes on disk, which is smaller than the text if compressed
	Locked bool      // encrypted: reading it needs the vault password
}

// historyDirFor is where a note's versions live. Callers check HistoryDir is
// set first: with it empty this would be a relative path.
func (s *Store) historyDirFor(rel string) string {
	sum := sha256.Sum256([]byte(normalizeRel(rel)))
	return filepath.Join(s.HistoryDir, hex.EncodeToString(sum[:8]))
}

// versionExts is every extension a version can carry: those a note has in each
// format, and the locked one.
func versionExts() []string {
	var exts []string
	for _, c := range Compressions() {
		exts = append(exts, c.ext())
	}
	return append(exts, lockedExt)
}

// parseVersionID splits an id into its timestamp and extension. Anything that
// is not exactly that, note.txt and half-written temporary files included, is
// not a version.
func parseVersionID(id string) (ns int64, ext string, ok bool) {
	for _, e := range versionExts() {
		digits, found := strings.CutSuffix(id, e)
		if !found || digits == "" || strings.Trim(digits, "0123456789") != "" {
			continue
		}
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || n <= 0 {
			return 0, "", false
		}
		return n, e, true
	}
	return 0, "", false
}

// versionTaken reports whether any version already has this timestamp, whatever
// its extension.
func versionTaken(dir string, ns int64) bool {
	for _, e := range versionExts() {
		if _, err := os.Lstat(filepath.Join(dir, strconv.FormatInt(ns, 10)+e)); err == nil {
			return true
		}
	}
	return false
}

// freeVersionID names a new version, moving its timestamp on by a nanosecond
// until nothing has it. File times are coarse, so two saves in quick succession
// can share one, and a version must never be written over another.
func freeVersionID(dir string, ns int64, ext string) string {
	for versionTaken(dir, ns) {
		ns++
	}
	return strconv.FormatInt(ns, 10) + ext
}

// listVersions reads a history directory, newest first. A directory that is not
// there is a note with no history, not a failure.
func listVersions(dir string) ([]Version, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, e := range entries {
		ns, ext, ok := parseVersionID(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since the listing: it is not a version now
		}
		out = append(out, Version{
			ID:     e.Name(),
			Time:   time.Unix(0, ns),
			Size:   info.Size(),
			Locked: ext == lockedExt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.After(out[j].Time)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// History lists a note's earlier versions, newest first. A note with none, or a
// store that keeps none, has an empty list and no error.
//
// A note that is locked has no plain versions to show. One that a sync brought
// in from a device that locked it might still have some here, from before, so
// with the key they are sealed first, and without it they are left out of the
// list: their existence is not private, but they are the old text in the clear.
func (s *Store) History(rel string) ([]Version, error) {
	if s.HistoryDir == "" {
		return nil, nil
	}
	rel = normalizeRel(rel)
	locked := s.historyLocked(rel)
	sealed := false
	if locked {
		if key, err := s.key(); err == nil {
			sealed = true
			if err := s.sealHistoryLocked(rel, key); err != nil {
				log.Printf("atlas-notes: sealing the history of %q: %v", rel, err)
			}
		}
	}
	versions, err := listVersions(s.historyDirFor(rel))
	if err != nil || !locked || sealed {
		return versions, err
	}
	kept := versions[:0]
	for _, v := range versions {
		if v.Locked {
			kept = append(kept, v)
		}
	}
	return kept, nil
}

// historyLocked reports whether a note's history has to be kept sealed: the note
// is stored locked. A note in a locked folder that is still plain, because it has
// not been saved since the folder was locked, is not locked yet, and its versions
// are as readable as the note is; they are sealed when a save locks it.
func (s *Store) historyLocked(rel string) bool {
	rel = normalizeRel(rel)
	return s.IsNoteLocked(rel) || (s.lockedByFolder(rel) && s.newestPlain(rel) == "")
}

// sealHistoryLocked is sealHistory for a caller that does not hold writeMu.
func (s *Store) sealHistoryLocked(rel string, key vaultlock.Key) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.sealHistory(rel, key)
}

// SealLockedHistory seals the plain versions of every note that is locked. A
// note locked on another device arrives without its history, but one locked
// here, or synced from somewhere that did not seal it, can have plain versions
// in this device's history, and this is what to call once the vault is unlocked
// to put that right. It needs the key, and fails with ErrLocked without it. It
// finds the notes from the history itself, not from the index, so it does not
// depend on a scan having been done. It carries on past a note it cannot deal
// with and reports the failures together.
func (s *Store) SealLockedHistory() error {
	if s.HistoryDir == "" {
		return nil
	}
	key, err := s.key()
	if err != nil {
		return err
	}
	dirs, err := os.ReadDir(s.HistoryDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var errs []error
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name, err := os.ReadFile(filepath.Join(s.HistoryDir, d.Name(), historyNoteFile))
		if err != nil || len(name) == 0 {
			continue
		}
		rel := normalizeRel(string(name))
		if rel == "" || !s.historyLocked(rel) {
			continue
		}
		if err := s.sealHistory(rel, key); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
		}
	}
	return errors.Join(errs...)
}

// versionFile checks an id and maps it to its file. The id arrives from the
// interface, so it is treated as untrusted: only a timestamp and one of the
// known extensions is accepted, which leaves no room for a path, and the result
// is checked to be in the note's own directory as well.
func (s *Store) versionFile(rel, id string) (path, ext string, err error) {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || id != filepath.Base(id) {
		return "", "", fmt.Errorf("%q: %w", id, errBadVersion)
	}
	_, ext, ok := parseVersionID(id)
	if !ok {
		return "", "", fmt.Errorf("%q: %w", id, errBadVersion)
	}
	dir := s.historyDirFor(rel)
	p := filepath.Join(dir, id)
	if filepath.Dir(p) != dir {
		return "", "", fmt.Errorf("%q: %w", id, errBadVersion)
	}
	return p, ext, nil
}

// ReadVersion returns the text of one earlier version. A version of a locked
// note fails with ErrLocked until the vault is unlocked, as the note itself
// does.
func (s *Store) ReadVersion(rel, id string) (string, error) {
	if s.HistoryDir == "" {
		return "", errNoHistory
	}
	p, ext, err := s.versionFile(rel, id)
	if err != nil {
		return "", err
	}
	// A plain version of a note that is locked is not read in the clear. With the
	// key it is sealed first and the sealed one is read; without it, it is as
	// locked as the note is.
	if ext != lockedExt && s.historyLocked(rel) {
		key, kerr := s.key()
		if kerr != nil {
			return "", ErrLocked
		}
		if err := s.sealHistoryLocked(rel, key); err != nil {
			log.Printf("atlas-notes: sealing the history of %q: %v", rel, err)
		}
		ns, _, _ := parseVersionID(id)
		if p, ext, err = s.versionFile(rel, strconv.FormatInt(ns, 10)+lockedExt); err != nil {
			return "", err
		}
	}
	if ext == lockedExt {
		key, err := s.key()
		if err != nil {
			return "", err
		}
		sealed, err := readFileCapped(p)
		if err != nil {
			return "", err
		}
		payload, err := vaultlock.Open(key, sealed)
		if err != nil {
			return "", err
		}
		out, err := s.decodePayload(payload)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	data, err := readFileCapped(p)
	if err != nil {
		return "", err
	}
	c, _ := plainCompression(id)
	out, err := s.decode(c, data)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// RestoreVersion puts an earlier version back as the note's text. What the note
// says now is kept as a version first, whatever its age, so a restore that was a
// mistake can itself be undone. If that cannot be done the note is left alone.
// A locked note stays locked: this is an ordinary save.
func (s *Store) RestoreVersion(rel, id string) error {
	content, err := s.ReadVersion(rel, id)
	if err != nil {
		return err
	}
	return s.writeNote(rel, content, true)
}

// currentFile finds the file a note is stored in now, looking where ReadNote
// does: the unlocked forms first, then the locked one. ok is false for a note
// that has no file yet.
func (s *Store) currentFile(rel string) (path, ext string, locked, ok bool, err error) {
	files, err := s.plainFiles(rel)
	if err != nil {
		return "", "", false, false, err
	}
	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			return f.path, f.c.ext(), false, true, nil
		}
	}
	lockedAbs, err := s.lockedPathSafe(rel)
	if err != nil {
		return "", "", false, false, err
	}
	if _, err := os.Stat(lockedAbs); err == nil {
		return lockedAbs, lockedExt, true, true, nil
	}
	return "", "", false, false, nil
}

// snapshotHistory prepares a note's history for a save that is about to replace
// its file, and copies that file into it. The caller holds writeMu.
//
// A note that this save writes locked gets its older unencrypted versions
// sealed first, whatever else happens here, since the note is about to leave
// the clear and its history must not be the place its old text stays. Without
// the key that is not possible, and the save cannot go ahead either, so nothing
// is done.
//
// The copy is made unless force is not set and the newest version is younger
// than historyMinGap. It is stamped with the file's modification time, which is
// when that text was written, not when it was replaced: the list should say when
// each version was the note.
func (s *Store) snapshotHistory(rel string, force bool) error {
	if s.HistoryDir == "" {
		return nil
	}
	rel = normalizeRel(rel)
	var key vaultlock.Key
	var sealErr error
	if s.IsNoteLocked(rel) || s.lockedByFolder(rel) {
		var err error
		if key, err = s.key(); err != nil {
			return nil // the save fails for the same reason, and changes nothing
		}
		sealErr = s.sealHistory(rel, key)
	}
	return errors.Join(sealErr, s.keepVersion(rel, force, key))
}

// keepVersion is the copying half of snapshotHistory. key is set when the save
// writes the note locked, so that a plain file is sealed on the way in.
func (s *Store) keepVersion(rel string, force bool, key vaultlock.Key) error {
	src, ext, locked, ok, err := s.currentFile(rel)
	if err != nil || !ok {
		return err // a note with no file yet has nothing to keep
	}
	dir := s.historyDirFor(rel)
	if !force {
		versions, err := listVersions(dir)
		if err != nil {
			return err
		}
		if len(versions) > 0 {
			// A newest version dated far in the future, from a clock that was
			// wrong, would otherwise stop history until the day arrived.
			age := historyNow().Sub(versions[0].Time)
			if age > -historyMinGap && age < historyMinGap {
				return nil
			}
		}
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := readFileCapped(src)
	if err != nil {
		return err
	}
	// A note in the clear that this save writes locked, because it has moved
	// into a locked folder, is not kept in the clear either.
	if !locked && key != nil {
		c, _ := plainCompression(src)
		text, err := s.decode(c, data)
		if err != nil {
			return err
		}
		if data, err = vaultlock.Seal(key, s.enc.EncodeAll(text, nil)); err != nil {
			return err
		}
		ext = lockedExt
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeHistoryNote(dir, rel); err != nil {
		return err
	}
	id := freeVersionID(dir, info.ModTime().UnixNano(), ext)
	if err := atomicWrite(filepath.Join(dir, id), data); err != nil {
		return err
	}
	return pruneHistory(dir, id)
}

// writeHistoryNote records which note a history directory is for.
func writeHistoryNote(dir, rel string) error {
	p := filepath.Join(dir, historyNoteFile)
	if cur, err := os.ReadFile(p); err == nil && string(cur) == rel {
		return nil
	}
	return atomicWrite(p, []byte(rel))
}

// pruneHistory applies the limits: versions older than historyMaxAge go, then
// all but the newest historyMaxVersions. The newest version, and the one just
// made, keep, are exempt from the age limit: a note untouched for months and
// edited today would otherwise have the only copy of its old text deleted the
// moment it was taken.
func pruneHistory(dir, keep string) error {
	versions, err := listVersions(dir)
	if err != nil {
		return err
	}
	var first error
	remove := func(v Version) {
		if err := os.Remove(filepath.Join(dir, v.ID)); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}

	cutoff := historyNow().Add(-historyMaxAge)
	live := versions[:0:0]
	room := historyMaxVersions
	for i, v := range versions {
		switch {
		case v.ID == keep:
			room-- // a place is held for it whatever its age
			live = append(live, v)
		case i > 0 && v.Time.Before(cutoff):
			remove(v)
		default:
			live = append(live, v)
		}
	}
	for _, v := range live { // newest first, so the surplus is the oldest
		if v.ID == keep {
			continue
		}
		if room > 0 {
			room--
			continue
		}
		remove(v)
	}
	return first
}

// moveHistory makes a note's history follow it to a new name. Called by
// RenameNote, and by a folder rename for each note inside; the caller holds
// writeMu. The note has already moved, so a failure here is logged and not
// returned: history is not worth failing a rename over.
func (s *Store) moveHistory(oldRel, newRel string) {
	if s.HistoryDir == "" {
		return
	}
	oldRel, newRel = normalizeRel(oldRel), normalizeRel(newRel)
	oldDir, newDir := s.historyDirFor(oldRel), s.historyDirFor(newRel)
	if oldDir == newDir {
		return
	}
	if _, err := os.Stat(oldDir); err != nil {
		return // no history to move
	}
	if err := mergeHistory(oldDir, newDir, newRel); err != nil {
		log.Printf("atlas-notes: moving the history of %q to %q: %v", oldRel, newRel, err)
	}
}

// mergeHistory moves one history directory to another name. When nothing is
// there yet that is a rename; when the new name already has history, which
// happens when a note takes over the name of one that was deleted to the Trash,
// the versions join it.
func mergeHistory(oldDir, newDir, newRel string) error {
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(newDir), 0o700); err != nil {
			return err
		}
		if err := os.Rename(oldDir, newDir); err != nil {
			return err
		}
		return writeHistoryNote(newDir, newRel)
	}

	versions, err := listVersions(oldDir)
	if err != nil {
		return err
	}
	for _, v := range versions {
		ns, ext, _ := parseVersionID(v.ID)
		id := freeVersionID(newDir, ns, ext)
		if err := os.Rename(filepath.Join(oldDir, v.ID), filepath.Join(newDir, id)); err != nil {
			return err
		}
	}
	if err := writeHistoryNote(newDir, newRel); err != nil {
		return err
	}
	return os.RemoveAll(oldDir)
}

// removeHistory deletes a note's history, for a note that is gone for good.
func (s *Store) removeHistory(rel string) {
	if s.HistoryDir == "" {
		return
	}
	if err := os.RemoveAll(s.historyDirFor(rel)); err != nil {
		log.Printf("atlas-notes: removing the history of %q: %v", rel, err)
	}
}

// sealHistory encrypts every unencrypted version of a note, for when the note is
// locked: the text it had before must not stay readable in history after the
// note itself has been put out of reach. Each version becomes a sealed one with
// the same timestamp, and only then is the plain one removed. The caller holds
// writeMu and has the vault's key.
//
// It carries on past a version it cannot deal with, so that one bad file does
// not leave the rest in the clear, and reports the first failure at the end.
func (s *Store) sealHistory(rel string, key vaultlock.Key) error {
	if s.HistoryDir == "" {
		return nil
	}
	dir := s.historyDirFor(rel)
	versions, err := listVersions(dir)
	if err != nil {
		return err
	}
	var first error
	for _, v := range versions {
		if v.Locked {
			continue
		}
		if err := s.sealVersion(dir, v, key); err != nil && first == nil {
			first = fmt.Errorf("sealing version %s: %w", v.ID, err)
		}
	}
	return first
}

// sealVersion seals one plain version and removes it.
func (s *Store) sealVersion(dir string, v Version, key vaultlock.Key) error {
	old := filepath.Join(dir, v.ID)
	data, err := readFileCapped(old)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// The payload is zstd, as for a locked note, so that a sealed version reads
	// like any other. One that will not decode is sealed as it is: it cannot be
	// read back either way, but it must not stay in the clear.
	payload := data
	c, _ := plainCompression(v.ID)
	if text, derr := s.decode(c, data); derr == nil {
		payload = s.enc.EncodeAll(text, nil)
	}
	sealed, err := vaultlock.Seal(key, payload)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, strconv.FormatInt(v.Time.UnixNano(), 10)+lockedExt)
	if err := atomicWrite(target, sealed); err != nil {
		return err
	}
	return os.Remove(old)
}

// planHistoryReseal is the first half of resealing history, for when the vault's
// password changes: the notes are re-sealed then, and their sealed versions have
// to follow or they could never be opened again. It opens every sealed version,
// of every note, with oldKey and seals it again under newKey in memory, and
// returns what is to be written. It writes nothing; the caller does, with the
// notes, so that all of it happens or none of it does. The caller holds writeMu.
//
// A version that cannot be read or opened with oldKey is left out of the plan
// and the first such failure is returned with the rest of it: the version could
// not be read before and is no worse off, and one bad file must not strand the
// others.
func (s *Store) planHistoryReseal(oldKey, newKey vaultlock.Key) ([]sealedFile, error) {
	if s.HistoryDir == "" {
		return nil, nil
	}
	notes, err := os.ReadDir(s.HistoryDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan []sealedFile
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, n := range notes {
		if !n.IsDir() {
			continue
		}
		dir := filepath.Join(s.HistoryDir, n.Name())
		versions, err := listVersions(dir)
		if err != nil {
			note(err)
			continue
		}
		for _, v := range versions {
			if !v.Locked {
				continue
			}
			p := filepath.Join(dir, v.ID)
			sealed, err := readFileCapped(p)
			if err == nil {
				var f sealedFile
				if f, err = resealBytes(p, sealed, oldKey, newKey); err == nil {
					plan = append(plan, f)
				}
			}
			if err != nil {
				note(fmt.Errorf("version %s: %w", v.ID, err))
			}
		}
	}
	return plan, first
}
