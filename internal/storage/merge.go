package storage

import (
	"errors"
	"fmt"
	"path"
	"regexp"
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

// NoteIsProtected reports whether a note is stored encrypted, either itself or
// because it is in a locked folder.
func (s *Store) NoteIsProtected(rel string) bool {
	rel = normalizeRel(rel)
	return s.IsNoteLocked(rel) || s.lockedByFolder(rel)
}

// splitFrontMatter cuts a leading "---" block off text. front is the lines
// between the fences, and body is what follows; front is nil when there is none.
func splitFrontMatter(text string) (front []string, body string) {
	lines := strings.Split(text, "\n")
	n := frontMatterLines(lines)
	if n == 0 {
		return nil, text
	}
	return lines[1 : n-1], strings.Join(lines[n:], "\n")
}

// frontTags reads the tags out of a front matter block, in the three ways they
// are written: "tags: a, b", "tags: [a, b]" and a list under "tags:".
func frontTags(front []string) []string {
	var out []string
	inList := false
	for _, l := range front {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "tags:"):
			v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "tags:")), "[]")
			inList = v == ""
			for _, x := range strings.Split(v, ",") {
				if x = strings.Trim(strings.TrimSpace(x), `"'`); x != "" {
					out = append(out, x)
				}
			}
		case inList && strings.HasPrefix(t, "- "):
			out = append(out, strings.Trim(strings.TrimSpace(t[2:]), `"'`))
		case t != "":
			inList = false
		}
	}
	return out
}

// addFrontTags adds tags that are not there yet to a front matter block.
func addFrontTags(front []string, tags []string) []string {
	have := map[string]bool{}
	for _, t := range frontTags(front) {
		have[strings.ToLower(t)] = true
	}
	var add []string
	for _, t := range tags {
		if !have[strings.ToLower(t)] {
			have[strings.ToLower(t)] = true
			add = append(add, t)
		}
	}
	if len(add) == 0 {
		return front
	}
	out := append([]string(nil), front...)
	for i, l := range out {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "tags:") {
			continue
		}
		if v := strings.TrimSpace(strings.TrimPrefix(t, "tags:")); v != "" {
			all := append(frontTags(front), add...)
			out[i] = "tags: [" + strings.Join(all, ", ") + "]"
			return out
		}
		j := i + 1
		for j < len(out) && strings.HasPrefix(strings.TrimSpace(out[j]), "- ") {
			j++
		}
		items := make([]string, len(add))
		for k, t := range add {
			items[k] = "  - " + t
		}
		return append(out[:j], append(items, out[j:]...)...)
	}
	return append(out, "tags: ["+strings.Join(add, ", ")+"]")
}

var mdImage = regexp.MustCompile(`(!?\[[^\]]*\]\()([^)\s]+)(\))`)

// relinkPaths rewrites the relative paths of pictures and files in text, written
// for a note in folder fromDir, so they still lead there from a note in intoDir.
func relinkPaths(text, fromDir, intoDir string) string {
	if fromDir == intoDir {
		return text
	}
	return mdImage.ReplaceAllStringFunc(text, func(m string) string {
		sub := mdImage.FindStringSubmatch(m)
		p := sub[2]
		if strings.Contains(p, "://") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "mailto:") {
			return m
		}
		abs := path.Join(fromDir, p)
		return sub[1] + relPath(intoDir, abs) + sub[3]
	})
}

// relPath is target written relative to the folder dir, both vault-relative.
func relPath(dir, target string) string {
	var from []string
	if dir != "" && dir != "." {
		from = strings.Split(dir, "/")
	}
	to := strings.Split(target, "/")
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	out := strings.Repeat("../", len(from)-i) + strings.Join(to[i:], "/")
	return out
}

// demoteHeadings moves every heading outside code one level down, so that the
// merged note's own headings sit under the one that names it.
func demoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, "#")); n > 0 && n < 6 && len(l) > n && l[n] == ' ' {
			lines[i] = "#" + l
		}
	}
	return strings.Join(lines, "\n")
}

// mergedText is the text of into with a merged note added at the end under a
// heading with its name, and the heading it used: a second merge of a note with
// the same name gets "Name 2", so that no two headings are alike and a link can
// name its part. The note's front matter is not prose: its tags are added to
// into's own front matter if it has one, and it is dropped otherwise. A first line
// that only repeats the name as a title is left out, since the heading says it,
// the note's headings move one level down, and the paths of its pictures are
// rewritten for the folder of the note they now live in.
func mergedText(into, name, from, fromDir, intoDir string) (text, heading string) {
	from = strings.ReplaceAll(from, "\r\n", "\n")
	into = strings.ReplaceAll(into, "\r\n", "\n")
	fromFront, from := splitFrontMatter(from)
	from = strings.TrimLeft(from, "\n")
	if first, rest, ok := strings.Cut(from, "\n"); (ok || first != "") && strings.HasPrefix(first, "# ") &&
		strings.EqualFold(strings.TrimSpace(first[2:]), name) {
		from = strings.TrimLeft(rest, "\n")
	}
	from = strings.TrimRight(relinkPaths(demoteHeadings(from), fromDir, intoDir), "\n ")

	if intoFront, body := splitFrontMatter(into); intoFront != nil {
		into = "---\n" + strings.Join(addFrontTags(intoFront, frontTags(fromFront)), "\n") + "\n---\n" + body
	}
	head := strings.TrimRight(into, "\n ")

	heading = name
	for n := 2; hasHeading(head, "## "+heading); n++ {
		heading = fmt.Sprintf("%s %d", name, n)
	}
	var b strings.Builder
	if head != "" {
		b.WriteString(head)
		b.WriteString("\n\n")
	}
	b.WriteString("## " + heading + "\n")
	if from != "" {
		b.WriteString("\n" + from + "\n")
	}
	return b.String(), heading
}

func hasHeading(text, heading string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.EqualFold(strings.TrimRight(l, " \t"), heading) {
			return true
		}
	}
	return false
}

// MergedText is the text a merge would leave in the target. See mergedText.
func MergedText(into, name, from string) string {
	t, _ := mergedText(into, name, from, "", "")
	return t
}

// MergeNote adds the text of fromRel to the end of intoRel, under a heading with
// its name, rewrites the links in other notes that named fromRel so that they
// name that part of intoRel, and moves fromRel to the Trash. It returns how many
// notes had a link rewritten.
//
// The order is what keeps a failure harmless. The text is added first, so it is
// never only in the Trash; the note is trashed next, and if that fails the
// target is put back as it was, so that trying again cannot add the text twice;
// only when it worked are links pointed at the target. A note that is protected
// in the target's vault is sealed before it goes to the Trash, so its text is not
// left there in the clear.
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
	fromLocked, intoLocked := s.NoteIsProtected(fromRel), s.NoteIsProtected(intoRel)
	if fromLocked && !intoLocked {
		return 0, errors.New("a protected note can't be merged into one that is not protected")
	}

	// Reading both and writing the target are one hold of the write lock, so a
	// save from anywhere else cannot fall between them and be lost.
	prev, heading, err := s.appendTo(fromRel, intoRel)
	if err != nil {
		return 0, err
	}
	fail := func(err error) (int, error) {
		if rerr := s.writeNote(intoRel, prev, true); rerr != nil {
			err = fmt.Errorf("%w (and %q could not be put back: %v)", err, intoRel, rerr)
		}
		return 0, err
	}
	if intoLocked && !fromLocked {
		if err := s.LockNote(fromRel); err != nil {
			return fail(fmt.Errorf("the merged note could not be protected before it went to the Trash: %w", err))
		}
	}
	if err := s.DeleteNote(fromRel); err != nil {
		return fail(fmt.Errorf("the merged note could not be moved to the Trash, so nothing was merged: %w", err))
	}

	after, err := s.notePaths()
	if err != nil {
		return 0, err
	}
	before := append(append([]string(nil), after...), fromRel)
	return s.rewriteLinks([]move{{from: fromRel, to: intoRel, heading: heading}}, before, after)
}

// appendTo adds fromRel's text to the end of intoRel and returns what intoRel
// held before, and the heading the text went under.
func (s *Store) appendTo(fromRel, intoRel string) (prev, heading string, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	fromText, err := s.ReadNote(fromRel)
	if err != nil {
		return "", "", err
	}
	prev, err = s.ReadNote(intoRel)
	if err != nil {
		return "", "", err
	}
	dir := func(p string) string {
		if d := path.Dir(p); d != "." {
			return d
		}
		return ""
	}
	text, heading := mergedText(prev, path.Base(fromRel), fromText, dir(fromRel), dir(intoRel))
	if err := s.writeNoteLocked(intoRel, text, true); err != nil {
		return "", "", err
	}
	return prev, heading, nil
}
