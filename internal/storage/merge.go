package storage

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Splitting one note into two, and merging two into one. Both go through the
// store's ordinary write path, so history, locking and the write lock apply as
// they do to any save.

// ErrNoTrash is returned when a merge is asked for and the store has no Trash to
// put the merged note in. A merge takes a note out of the vault, and it must
// always be possible to get that note back.
var ErrNoTrash = errors.New("there is no Trash to keep the merged note in")

// CreateNote writes a new note, but only if no note of that name exists, and says
// whether it did. It is what splitting a note off another uses: the name was
// free when the person chose it, and a note made in the meantime by a sync must
// not be written over.
func (s *Store) CreateNote(rel, content string) (bool, error) {
	return s.createNote(normalizeRel(rel), content)
}

// MergedText is the text of into with the text of a merged note added at the end,
// under a heading with that note's name. A first line of from that only repeats
// the name as a title is left out, since the heading now says it.
func MergedText(into, name, from string) string {
	from = strings.ReplaceAll(from, "\r\n", "\n")
	if first, rest, ok := strings.Cut(from, "\n"); ok || first != "" {
		if strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(first, "# ")), name) &&
			strings.HasPrefix(first, "# ") {
			from = strings.TrimLeft(rest, "\n")
		}
	}
	from = strings.TrimRight(from, "\n ")
	head := strings.TrimRight(strings.ReplaceAll(into, "\r\n", "\n"), "\n ")
	var b strings.Builder
	if head != "" {
		b.WriteString(head)
		b.WriteString("\n\n")
	}
	b.WriteString("## " + name + "\n")
	if from != "" {
		b.WriteString("\n" + from + "\n")
	}
	return b.String()
}

// MergeNote adds the text of fromRel to the end of intoRel, under a heading with
// its name, rewrites the links in other notes that named fromRel so that they
// name intoRel, and moves fromRel to the Trash. It returns how many notes had a
// link rewritten.
//
// The order is what keeps a failure harmless. The text is added first, so it is
// never only in the Trash; the note is trashed next, and only if that worked are
// links pointed at the target, so a link never leads to a note that is still
// there under its old name. A failure part way leaves the text in both notes
// rather than in neither, and the error says which step it was.
//
// A note open in an editor is read from disk like the rest, so a caller with
// unsaved edits in either has to save them first, and reload both afterwards.
func (s *Store) MergeNote(fromRel, intoRel string) (int, error) {
	fromRel, intoRel = normalizeRel(fromRel), normalizeRel(intoRel)
	switch {
	case fromRel == "" || intoRel == "":
		return 0, errors.New("a merge needs two notes")
	case strings.EqualFold(fromRel, intoRel):
		return 0, errors.New("a note cannot be merged into itself")
	case s.Trash == nil:
		return 0, ErrNoTrash
	}
	fromText, err := s.ReadNote(fromRel)
	if err != nil {
		return 0, err
	}

	// Reading the target and writing it back are one hold of the write lock, so
	// a save of it from anywhere else cannot fall between them and be lost.
	if err := s.appendTo(intoRel, path.Base(fromRel), fromText); err != nil {
		return 0, err
	}
	if err := s.DeleteNote(fromRel); err != nil {
		return 0, fmt.Errorf("the text was added to %q, but the merged note could not be moved to the Trash: %w", intoRel, err)
	}

	after, err := s.notePaths()
	if err != nil {
		return 0, err
	}
	before := append(append([]string(nil), after...), fromRel)
	return s.rewriteLinks([]move{{fromRel, intoRel}}, before, after)
}

// appendTo adds a merged note's text to the end of intoRel.
func (s *Store) appendTo(intoRel, name, fromText string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	intoText, err := s.ReadNote(intoRel)
	if err != nil {
		return err
	}
	return s.writeNoteLocked(intoRel, MergedText(intoText, name, fromText), false)
}
