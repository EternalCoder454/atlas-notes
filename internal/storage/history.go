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
func (s *Store) History(rel string) ([]Version, error) {
	if s.HistoryDir == "" {
		return nil, nil
	}
	return listVersions(s.historyDirFor(rel))
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

// resealHistory re-encrypts every sealed version, of every note, from oldKey to
// newKey, for when the vault's password changes: the notes are re-sealed then,
// and their history has to follow or it could never be opened again. The caller
// holds writeMu.
//
// A version that cannot be opened with oldKey is left as it is and the rest go
// on, so one bad file does not strand the others; the first failure is reported
// at the end.
func (s *Store) resealHistory(oldKey, newKey vaultlock.Key) error {
	if s.HistoryDir == "" {
		return nil
	}
	notes, err := os.ReadDir(s.HistoryDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var first error
	for _, n := range notes {
		if !n.IsDir() {
			continue
		}
		dir := filepath.Join(s.HistoryDir, n.Name())
		versions, err := listVersions(dir)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		for _, v := range versions {
			if !v.Locked {
				continue
			}
			if err := resealVersion(filepath.Join(dir, v.ID), oldKey, newKey); err != nil && first == nil {
				first = fmt.Errorf("re-sealing version %s: %w", v.ID, err)
			}
		}
	}
	return first
}

// resealVersion re-seals one version file in place.
func resealVersion(path string, oldKey, newKey vaultlock.Key) error {
	sealed, err := readFileCapped(path)
	if err != nil {
		return err
	}
	payload, err := vaultlock.Open(oldKey, sealed)
	if err != nil {
		return err
	}
	resealed, err := vaultlock.Seal(newKey, payload)
	if err != nil {
		return err
	}
	return atomicWrite(path, resealed)
}
