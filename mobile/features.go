package bridge

import (
	"errors"
	"fmt"
	"time"

	"atlas-notes/internal/storage"
)

// The daily note, tags and due tasks.
//
// These are the desktop's own features, reached through the same storage
// functions, so a daily note made on a phone is the one the desktop opens and
// a tag means the same thing in both. As everywhere in this package, lists
// come back as JSON because that is all gomobile can carry.

// dateLayout is how a due date is written, in the notes and across this
// boundary: 2026-09-29.
const dateLayout = "2006-01-02"

// DailyNote returns the path of today's note, making it first if there is none
// yet. "Today" is the phone's own day, in its own time zone, because that is
// the day the person holding it means. A note in a locked folder while the
// vault is locked fails with ErrLockedMessage, which is a request for the
// password and not a failure, the same as reading a locked note.
func DailyNote() (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	rel, _, err := s.DailyNote(time.Now())
	if errors.Is(err, storage.ErrLocked) {
		return "", errors.New(ErrLockedMessage)
	}
	return rel, err
}

// dueRow is one unfinished task that has a due date. Like noteRow it is
// unexported because it crosses to Kotlin as JSON.
type dueRow struct {
	Path     string `json:"path"`
	Line     int    `json:"line"` // 0-based, as Tasks counts them
	Text     string `json:"text"`
	Due      string `json:"due"`      // yyyy-mm-dd
	Priority string `json:"priority"` // "high", "medium", "low", or ""
}

// DueTasks returns, as a JSON array, every unfinished task due on or before
// through, given as yyyy-mm-dd. What is overdue is included, because a task
// missed yesterday is still due today. They come most pressing first: by date,
// then priority. Tasks in locked notes are never listed, so a notification
// built from this cannot show what the password is there to hide.
func DueTasks(through string) (string, error) {
	// The comparison in the index is between strings, which is only a
	// comparison of dates while both are written the same way. Anything else
	// would quietly return the wrong tasks rather than none.
	if _, err := time.Parse(dateLayout, through); err != nil {
		return "", fmt.Errorf("%q is not a date written yyyy-mm-dd", through)
	}
	s, err := vault()
	if err != nil {
		return "", err
	}
	tasks, err := s.DueTasks(through)
	if err != nil {
		return "", err
	}
	out := make([]dueRow, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, dueRow{Path: t.Path, Line: t.Line, Text: t.Text, Due: t.Due, Priority: t.Priority})
	}
	return toJSON(out)
}

// tagRow is a tag and how many notes carry it.
type tagRow struct {
	Tag   string `json:"tag"` // lowercase, without the "#"
	Notes int    `json:"notes"`
}

// Tags returns every tag in the vault as a JSON array, sorted by name, with how
// many notes carry each.
func Tags() (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	tags, err := s.Tags()
	if err != nil {
		return "", err
	}
	out := make([]tagRow, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagRow{Tag: t.Tag, Notes: t.Notes})
	}
	return toJSON(out)
}

// NotesWithTag returns, as a JSON array of paths, the notes that carry a tag,
// newest first. The tag may be written with its "#" and in any case, and a
// note under a nested tag counts for the tag above it.
func NotesWithTag(tag string) (string, error) {
	s, err := vault()
	if err != nil {
		return "", err
	}
	notes, err := s.NotesWithTag(tag)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(notes))
	for _, n := range notes {
		paths = append(paths, n.Path)
	}
	return toJSON(paths)
}
