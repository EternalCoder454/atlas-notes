package storage

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Daily notes and templates.
//
// Both are ordinary notes in ordinary folders, so they sync, export and open
// in other Markdown apps like any other: a daily note is "Daily/2026-09-29",
// and a template is any note in "Templates". Nothing about them is kept
// anywhere else. The phone and the desktop call the same functions, so a
// daily note made on one is the one the other opens.

// DailyFolder holds one note per day, named by its date.
const DailyFolder = "Daily"

// TemplatesFolder holds the notes offered as templates.
const TemplatesFolder = "Templates"

// dailyTemplate is the template a new daily note starts from, when it exists.
const dailyTemplate = TemplatesFolder + "/Daily"

// DailyNoteName is the note for a day: "Daily/2026-09-29". The ISO date sorts
// by day in any file list and reads the same in every language.
func DailyNoteName(day time.Time) string {
	return DailyFolder + "/" + day.Format("2006-01-02")
}

// DailyNote returns the note for day, creating it if there is none yet. A new
// one starts from "Templates/Daily" when that exists and can be read, and
// otherwise from a heading with the date written out. created says whether
// it had to be made.
func (s *Store) DailyNote(day time.Time) (rel string, created bool, err error) {
	rel = DailyNoteName(day)
	if s.noteTaken(rel) {
		return rel, false, nil
	}
	title := day.Format("Monday 2 January 2006")
	body := "# " + title + "\n\n"
	// A template that cannot be read, because it is locked and the vault is
	// not, gives way to the plain heading rather than stopping the day's note.
	if s.noteTaken(dailyTemplate) {
		if text, terr := s.ReadNote(dailyTemplate); terr == nil {
			body = ExpandTemplate(text, title, day)
		}
	}
	// Created only if it is still absent: another device's sync, or another
	// goroutine, may have made the day's note while the template was read.
	made, err := s.createNote(rel, body)
	if err != nil {
		return "", false, err
	}
	return rel, made, nil
}

// Templates lists the notes in the templates folder, including any in folders
// inside it, sorted by name.
func (s *Store) Templates() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM notes WHERE folder = ? OR folder LIKE ? ESCAPE '\'`,
		TemplatesFolder, escapeLike(TemplatesFolder)+"/%")
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
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, rows.Err()
}

// NewFromTemplate makes a note called title in folder from the template at
// templateRel, with its placeholders filled in, and returns the new note's
// path. The name is made unique as for any new note.
func (s *Store) NewFromTemplate(templateRel, folder, title string, now time.Time) (string, error) {
	text, err := s.ReadNote(templateRel)
	if err != nil {
		return "", fmt.Errorf("the template could not be read: %w", err)
	}
	if strings.TrimSpace(title) == "" {
		title = path.Base(normalizeRel(templateRel))
	}
	// A name that was free when it was chosen can be taken by the time the note
	// is written, so a lost race chooses again rather than writing over it.
	for range 20 {
		rel := s.UniqueName(folder, title)
		made, err := s.createNote(rel, ExpandTemplate(text, path.Base(rel), now))
		if err != nil {
			return "", err
		}
		if made {
			return rel, nil
		}
	}
	return "", errors.New("could not find a free name for the new note")
}

// ExpandTemplate fills in a template's placeholders:
//
//	{{title}}    the new note's name
//	{{date}}     2026-09-29
//	{{time}}     14:05
//	{{datetime}} 2026-09-29 14:05
//	{{weekday}}  Tuesday
//	{{longdate}} Tuesday 29 September 2026
//
// Names are matched without regard to case or spaces inside the braces, and
// anything else in braces is left as it was written, so a template can still
// talk about {{placeholders}} it does not mean.
func ExpandTemplate(text, title string, now time.Time) string {
	values := map[string]string{
		"title":    title,
		"date":     now.Format("2006-01-02"),
		"time":     now.Format("15:04"),
		"datetime": now.Format("2006-01-02 15:04"),
		"weekday":  now.Format("Monday"),
		"longdate": now.Format("Monday 2 January 2006"),
	}
	var b strings.Builder
	for {
		open := strings.Index(text, "{{")
		if open < 0 {
			break
		}
		end := strings.Index(text[open+2:], "}}")
		if end < 0 {
			break
		}
		name := strings.ToLower(strings.TrimSpace(text[open+2 : open+2+end]))
		b.WriteString(text[:open])
		if v, ok := values[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(text[open : open+2+end+2])
		}
		text = text[open+2+end+2:]
	}
	b.WriteString(text)
	return b.String()
}
