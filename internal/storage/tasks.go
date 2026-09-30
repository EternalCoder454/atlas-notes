package storage

import (
	"errors"
	"strings"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/markup"
)

// ErrTaskMoved means the task a caller asked to complete is no longer where the
// index said it was, because the note was edited since. The Tasks page refreshes
// on it rather than tick some other line.
var ErrTaskMoved = errors.New("that task has changed; the list was out of date")

// OpenTasks lists every unfinished task in the vault: the dated ones by date,
// as DueTasks orders them, then the undated ones, most urgent first, then by note and line. An undated
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
		ORDER BY CASE u.priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END,
			n.path, u.line`)
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

// CompleteTask ticks a task, as the Tasks page does. t is the task as the index
// listed it. It is found by what the index keeps of it (its text, date and
// priority) at the line the index gave, or else on the one line of the note that
// says exactly that; a task that is not found once, or that is now ticked, is
// not touched and the answer is ErrTaskMoved. Lines inside a code block are
// never tasks, as in the index.
//
// Only the box changes: "[ ]" becomes "[x]" and the rest of the line stays as it
// was written. The read, the edit and the write are one hold of the write lock,
// so a save landing in between cannot be overwritten, and two ticks in one note
// cannot lose each other. The current text is kept as a version first, so a tick
// can be undone from the history. A locked note is never written here: it is not
// in the index, and if one was locked since, the answer is ErrLocked.
func (s *Store) CompleteTask(rel string, t DueTask) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rel = normalizeRel(rel)
	if s.IsNoteLocked(rel) || s.lockedByFolder(rel) {
		return ErrLocked
	}
	raw, err := s.readPlain(rel)
	if err != nil {
		return err
	}
	body := string(raw)
	lines := strings.Split(body, "\n")
	fenced := markup.InCodeFence(body)
	is := func(i int) bool { return !fenced[i] && sameTask(lines[i], t) }
	at := -1
	if t.Line >= 0 && t.Line < len(lines) && is(t.Line) {
		at = t.Line
	} else {
		for i := range lines {
			if is(i) {
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
	lines[at] = strings.Replace(lines[at], "[ ]", "[x]", 1)
	return s.writeNoteLocked(rel, strings.Join(lines, "\n"), true)
}

// sameTask says whether a line is the unticked task t, by the same reading of
// it the index made: text, a due date only if it is a real one, and a priority
// only if it is a known one.
func sameTask(line string, t DueTask) bool {
	it, ok := checklist.ParseLine(line)
	if !ok || it.Checked || it.Text != t.Text {
		return false
	}
	due := it.DueDate
	if !validDate(due) {
		due = ""
	}
	pri := it.Priority
	if !pri.Valid() {
		pri = checklist.PriorityNone
	}
	return due == t.Due && string(pri) == t.Priority
}
