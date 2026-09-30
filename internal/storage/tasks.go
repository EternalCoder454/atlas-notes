package storage

import (
	"errors"
	"strings"

	"atlas-notes/internal/checklist"
)

// ErrTaskMoved means the task a caller asked to complete is no longer where the
// index said it was, because the note was edited since. The Tasks page refreshes
// on it rather than tick some other line.
var ErrTaskMoved = errors.New("that task has changed; the list was out of date")

// OpenTasks lists every unfinished task in the vault: the dated ones by date,
// as DueTasks orders them, then the undated ones by note and line. An undated
// task has an empty Due. Locked notes are not in the index, so they are not here.
func (s *Store) OpenTasks() ([]DueTask, error) {
	out, err := s.DueTasks("9999-12-31")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT n.path, u.line, u.text, u.priority FROM note_undated u
		JOIN notes n ON n.id = u.note_id
		WHERE n.locked = 0
		ORDER BY n.path, u.line`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t DueTask
		if err := rows.Scan(&t.Path, &t.Line, &t.Text, &t.Priority); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CompleteTask ticks the task on line (0-based) of a note, as the Tasks page
// does. text is what the index says the task says: if the line no longer holds
// that task, the task is looked for by its text on another line, and if it is
// not found exactly once the note is left alone and ErrTaskMoved comes back.
//
// Only the box changes: "[ ]" becomes "[x]" and the rest of the line stays as it
// was written. The save goes through WriteNote, so history, indexing and the
// write lock all apply. A locked note is never written here: it is not in the
// index, and if one was locked since, the answer is ErrLocked.
func (s *Store) CompleteTask(rel string, line int, text string) error {
	rel = normalizeRel(rel)
	if s.IsNoteLocked(rel) || s.lockedByFolder(rel) {
		return ErrLocked
	}
	// The read and the write are not one hold of the write lock, so a save that
	// lands between them would be lost. Nothing else here writes a note without
	// going through the app's own save, which the page never runs concurrently
	// with a note open, so the gap is the same one every save has.
	body, err := s.ReadNote(rel)
	if err != nil {
		return err
	}
	lines := strings.Split(body, "\n")
	at := -1
	if line >= 0 && line < len(lines) && openTaskText(lines[line]) == text {
		at = line
	} else {
		for i, l := range lines {
			if openTaskText(l) == text {
				if at >= 0 {
					return ErrTaskMoved // two candidates: which one is not a guess to make
				}
				at = i
			}
		}
	}
	if at < 0 {
		return ErrTaskMoved
	}
	l := lines[at]
	lines[at] = strings.Replace(l, "[ ]", "[x]", 1)
	return s.WriteNote(rel, strings.Join(lines, "\n"))
}

// openTaskText is the text of an unticked task line, or "\x00" for anything
// else, which no task text can equal.
func openTaskText(line string) string {
	it, ok := checklist.ParseLine(line)
	if !ok || it.Checked {
		return "\x00"
	}
	return it.Text
}
