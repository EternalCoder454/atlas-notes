package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"atlas-notes/internal/storage"
)

// tool is one thing the model can do.
type tool struct {
	name, title, description string
	needs                    permission
	readOnly, destructive    bool
	idempotent               bool
	schema                   map[string]any
	run                      func(s *server, a storage.ClaudeAccess, args []byte) (string, error)
}

func (t *tool) describe() map[string]any {
	return map[string]any{
		"name":        t.name,
		"title":       t.title,
		"description": t.description,
		"inputSchema": t.schema,
		"annotations": map[string]any{
			"title":           t.title,
			"readOnlyHint":    t.readOnly,
			"destructiveHint": t.destructive,
			"idempotentHint":  t.idempotent,
			"openWorldHint":   false,
		},
	}
}

func toolNamed(name string) *tool {
	for i := range tools {
		if tools[i].name == name {
			return &tools[i]
		}
	}
	return nil
}

// object is a JSON Schema for a tool's arguments.
func object(required []string, props map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required,
		"additionalProperties": false}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func strings_(desc string, max int) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}, "maxItems": max}
}
func integer(desc string, min int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min}
}

// decode reads a tool's arguments.
func decode(args []byte, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("the arguments could not be read: %w", err)
	}
	return nil
}

const (
	notePathDesc   = `The note's path in the vault, without an extension, such as "Work/Spec".`
	defaultListMax = 50
	// maxListNotes and maxListFolders cap what one call returns whatever limit asks.
	maxListNotes   = 500
	maxListFolders = 500
	defaultFolders = 100
	// readChunk is how much of a long note read_note returns at once. Claude
	// Code refuses a tool result much past 25,000 tokens.
	readChunk = 60000
	// maxReadPaths is how many notes one read_note call may name.
	maxReadPaths = 10
	// importMax is the largest file import_file takes.
	importMax = 16 << 20
)

var tools []tool

func init() {
	tools = []tool{
		{
			name: "atlas_status", title: "Atlas Notes status", needs: needsNothing,
			readOnly: true, idempotent: true,
			description: "Says what Atlas Notes currently lets Claude Code do with the person's notes, and how many notes are available. Use it when another tool says something is not allowed.",
			schema:      object(nil, map[string]any{}),
			run:         runStatus,
		},
		{
			name: "list_notes", title: "List notes", needs: needsRead, readOnly: true, idempotent: true,
			description: "Lists the notes in the vault, or in one folder, with when each was last changed. Checklists are marked. Use it to see what exists; use search_notes to find notes by their text.",
			schema: object(nil, map[string]any{
				"folder":    str(`Only notes in this folder, such as "Work". Leave out for the whole vault.`),
				"recursive": boolean("Include notes in folders inside the folder. Defaults to true."),
				"sort":      map[string]any{"type": "string", "enum": []string{"recent", "path"}, "description": `"recent" (default) or "path".`},
				"limit":     integer(fmt.Sprintf("Most notes to list. Defaults to %d.", defaultListMax), 1),
			}),
			run: runListNotes,
		},
		{
			name: "list_folders", title: "List folders", needs: needsRead, readOnly: true, idempotent: true,
			description: "Lists the folders in the vault, to see how it is organised.",
			schema: object(nil, map[string]any{
				"limit": integer(fmt.Sprintf("Most folders to list. Defaults to %d.", defaultFolders), 1),
			}),
			run: runListFolders,
		},
		{
			name: "read_note", title: "Read a note", needs: needsRead, readOnly: true, idempotent: true,
			description: "Reads a note's Markdown as stored. For a long note, ask for outline (its headings with line numbers) or one section by heading rather than the whole text. paths reads up to 10 notes in one call. A part of a note says which lines it holds; start_line reads on.",
			schema: object(nil, map[string]any{
				"path":       str(notePathDesc + " Give path or paths."),
				"paths":      strings_("Up to 10 notes to read in one call, instead of path.", maxReadPaths),
				"outline":    boolean("Return only the headings, as 12| ## Heading, with the note's line and character counts."),
				"heading":    str(`Return only the section under this heading, such as "Plan" (case and leading # ignored), up to the next heading of the same or higher level.`),
				"start_line": integer("First line to return, counting from 1. Defaults to 1.", 1),
				"line_count": integer("How many lines to return. Defaults to the rest of the note.", 1),
			}),
			run: runReadNote,
		},
		{
			name: "search_notes", title: "Search notes", needs: needsRead, readOnly: true, idempotent: true,
			description: "Finds notes whose name or text contains every word of the query (words match from their start; case and accents are ignored). Returns each note's path with up to 3 matching lines and their line numbers, so a hit can often be used without reading the note.",
			schema: object([]string{"query"}, map[string]any{
				"query":  str("Words to look for."),
				"folder": str(`Only look in this folder and the folders inside it, such as "Work".`),
				"limit":  integer("Most notes to return. Defaults to 20.", 1),
			}),
			run: runSearch,
		},
		{
			name: "list_tags", title: "List tags", needs: needsRead, readOnly: true, idempotent: true,
			description: "Lists the #tags used in the vault with how many notes carry each, or, given a tag, the notes that carry it. Use it to find notes by topic.",
			schema: object(nil, map[string]any{
				"tag": str(`A tag, with or without the "#", to list the notes that carry it.`),
			}),
			run: runTags,
		},
		{
			name: "create_note", title: "Create a note", needs: needsWrite, idempotent: false,
			description: "Creates a new note with the given Markdown. Fails if the name is taken; use edit_note or write_note to change an existing one.",
			schema: object([]string{"path", "content"}, map[string]any{
				"path":    str(notePathDesc),
				"content": str(`The note's Markdown. Usually starts with a "# Title" line.`),
			}),
			run: runCreate,
		},
		{
			name: "import_file", title: "Import a file as a note", needs: needsWrite,
			description: "Imports a text or Markdown file from this computer as a new note, without its contents passing through the conversation. Use it instead of reading a file and pasting it into create_note.",
			schema: object([]string{"source_path"}, map[string]any{
				"source_path": str("The file to import. Relative paths are from the current working directory."),
				"path":        str(`The note to create, such as "Work/Spec". Defaults to the file's name without its extension, inside folder.`),
				"folder":      str(`The folder to put the note in when path is left out. Defaults to the top of the vault.`),
				"overwrite":   boolean("Replace a note of that name if there is one. Defaults to false."),
			}),
			run: runImport,
		},
		{
			name: "edit_note", title: "Edit a note", needs: needsWrite,
			description: "Changes a note by replacing exact text, and shows the changed lines with line numbers so there is no need to read it again. Give old_text and new_text for one change, or edits for several, applied in order and all-or-nothing. old_text must match exactly and occur once unless replace_all is set. If it does not match, the error shows the lines as stored.",
			schema: object([]string{"path"}, map[string]any{
				"path":        str(notePathDesc),
				"old_text":    str("The text to replace, exactly as it is in the note. Not used with edits."),
				"new_text":    str("What to put in its place. Not used with edits."),
				"replace_all": boolean("Replace every occurrence. Defaults to false."),
				"edits": map[string]any{
					"type": "array", "description": "Several changes in one call, applied in order to the text as the earlier ones left it. If one fails, none is made, and the error names it by number.",
					"items": object([]string{"old_text", "new_text"}, map[string]any{
						"old_text":    str("The text to replace, exactly as it is in the note."),
						"new_text":    str("What to put in its place."),
						"replace_all": boolean("Replace every occurrence. Defaults to false."),
					}),
				},
			}),
			run: runEdit,
		},
		{
			name: "append_to_note", title: "Add to a note", needs: needsWrite,
			description: "Adds text on new lines to the end of a note, or to the end of one section with heading, such as a checklist item or a log entry. Shows the added lines with line numbers.",
			schema: object([]string{"path", "text"}, map[string]any{
				"path":    str(notePathDesc),
				"text":    str("The Markdown to add."),
				"heading": str(`Add at the end of the section under this heading, such as "Tasks", rather than at the end of the note.`),
			}),
			run: runAppend,
		},
		{
			name: "write_note", title: "Replace a note", needs: needsWrite, idempotent: true,
			description: "Replaces the whole text of an existing note with new Markdown. Prefer edit_note for partial changes, which is cheaper and safer.",
			schema: object([]string{"path", "content"}, map[string]any{
				"path":    str(notePathDesc),
				"content": str("The note's new Markdown, in full."),
			}),
			run: runWrite,
		},
		{
			name: "rename_note", title: "Rename or move a note", needs: needsWrite,
			description: "Renames a note or moves it to another folder. [[Links]] to it in other notes are updated.",
			schema: object([]string{"path", "new_path"}, map[string]any{
				"path":     str(notePathDesc),
				"new_path": str(`The new full path, such as "Archive/Spec".`),
			}),
			run: runRenameNote,
		},
		{
			name: "create_folder", title: "Create a folder", needs: needsWrite, idempotent: true,
			description: "Creates a folder in the vault, and any folders above it.",
			schema: object([]string{"path"}, map[string]any{
				"path": str(`The folder, such as "Work/Projects".`),
			}),
			run: runCreateFolder,
		},
		{
			name: "rename_folder", title: "Rename or move a folder", needs: needsWrite,
			description: "Renames a folder or moves it inside another, with everything in it. [[Links]] to its notes are updated.",
			schema: object([]string{"path", "new_path"}, map[string]any{
				"path":     str("The folder."),
				"new_path": str("Its new full path."),
			}),
			run: runRenameFolder,
		},
		{
			name: "delete_note", title: "Delete a note", needs: needsDelete, destructive: true,
			description: "Moves a note to the Trash, where the person can restore it from.",
			schema: object([]string{"path"}, map[string]any{
				"path": str(notePathDesc),
			}),
			run: runDeleteNote,
		},
		{
			name: "delete_folder", title: "Delete a folder", needs: needsDelete, destructive: true,
			description: "Moves a folder and every note in it to the Trash, where the person can restore it from.",
			schema: object([]string{"path"}, map[string]any{
				"path": str("The folder."),
			}),
			run: runDeleteFolder,
		},
	}
}

// --- reading ----------------------------------------------------------------

func runStatus(s *server, a storage.ClaudeAccess, _ []byte) (string, error) {
	if !a.Enabled {
		return refusal(a, needsRead).Error(), nil
	}
	var b strings.Builder
	// Each switch by the name Settings gives it, so the person can find it.
	b.WriteString("What Atlas Notes lets Claude Code do, switch by switch:\n")
	for _, p := range []struct {
		on         bool
		name, what string
	}{
		{a.Read, "Read and search notes", "list, read and search notes"},
		{a.Write, "Create and edit notes", "create, edit, rename and move notes and folders"},
		{a.Delete, "Delete notes", "move notes and folders to the Trash"},
	} {
		state := "off"
		if p.on {
			state = "on "
		}
		fmt.Fprintf(&b, "- %s  %q: %s\n", state, p.name, p.what)
	}
	b.WriteString("A switch that is off can be turned on in Atlas Notes, under Settings, Claude Code.\n")
	b.WriteString("Password-protected notes are never available.\n")
	if a.Read {
		s.refresh()
		if notes, err := s.visibleNotes(); err == nil {
			fmt.Fprintf(&b, "Notes available: %d\n", len(notes))
		}
	}
	return b.String(), nil
}

// visibleNotes is every note the model may know of.
func (s *server) visibleNotes() ([]storage.NoteMeta, error) {
	all, err := s.store.ListNotes()
	if err != nil {
		return nil, err
	}
	v, err := s.visibility()
	if err != nil {
		return nil, err
	}
	out := make([]storage.NoteMeta, 0, len(all))
	for _, m := range all {
		if v.note(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

func runListNotes(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Folder    string `json:"folder"`
		Recursive *bool  `json:"recursive"`
		Sort      string `json:"sort"`
		Limit     int    `json:"limit"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	s.refresh()
	folder, err := s.visibleFolder(p.Folder)
	if err != nil {
		return "", err
	}
	recursive := p.Recursive == nil || *p.Recursive
	limit := p.Limit
	if limit <= 0 {
		limit = defaultListMax
	}
	limit = min(limit, maxListNotes)
	notes, err := s.visibleNotes()
	if err != nil {
		return "", err
	}
	var picked []storage.NoteMeta
	for _, m := range notes {
		switch {
		case folder == "":
			if !recursive && m.Folder != "" {
				continue
			}
		case recursive:
			if m.Folder != folder && !strings.HasPrefix(m.Folder, folder+"/") {
				continue
			}
		default:
			if m.Folder != folder {
				continue
			}
		}
		picked = append(picked, m)
	}
	if p.Sort != "path" {
		sort.SliceStable(picked, func(i, j int) bool { return picked[i].ModifiedAt.After(picked[j].ModifiedAt) })
	}
	if len(picked) == 0 {
		if folder != "" {
			return fmt.Sprintf("There are no notes in %q.", folder), nil
		}
		return "The vault has no notes yet.", nil
	}
	var b strings.Builder
	shown := min(len(picked), limit)
	fmt.Fprintf(&b, "%d notes", len(picked))
	if shown < len(picked) {
		if limit == maxListNotes {
			fmt.Fprintf(&b, " (showing %d, the most one call lists; pick a folder for more)", shown)
		} else {
			fmt.Fprintf(&b, " (showing %d; raise limit or pick a folder for more)", shown)
		}
	}
	b.WriteString(":\n")
	for _, m := range picked[:shown] {
		b.WriteString(m.Path)
		if m.HasTasks {
			b.WriteString("  [checklist]")
		}
		fmt.Fprintf(&b, "  (edited %s)\n", m.ModifiedAt.Format("2006-01-02 15:04"))
	}
	return b.String(), nil
}

func runListFolders(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Limit int `json:"limit"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultFolders
	}
	limit = min(limit, maxListFolders)
	folders, err := s.store.ListFolders()
	if err != nil {
		return "", err
	}
	v, err := s.visibility()
	if err != nil {
		return "", err
	}
	var out []string
	for _, f := range folders {
		if v.folder(f) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return "The vault has no folders; every note is at the top.", nil
	}
	head := fmt.Sprintf("%d folders", len(out))
	if len(out) > limit {
		if limit == maxListFolders {
			head += fmt.Sprintf(" (showing %d, the most one call lists)", limit)
		} else {
			head += fmt.Sprintf(" (showing %d; raise limit for more)", limit)
		}
		out = out[:limit]
	}
	return fmt.Sprintf("%s:\n%s\n", head, strings.Join(out, "\n")), nil
}

// readText is the text of a visible note. A note that is locked answers as
// one that is not there.
func (s *server) readText(rel string) (string, error) {
	text, err := s.store.ReadNote(rel)
	if err != nil {
		if errors.Is(err, storage.ErrLocked) || errors.Is(err, storage.ErrNoPassword) {
			return "", s.errMissing(rel)
		}
		return "", err
	}
	return text, nil
}

func runReadNote(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		Outline   bool     `json:"outline"`
		Heading   string   `json:"heading"`
		StartLine int      `json:"start_line"`
		LineCount int      `json:"line_count"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	o := readOpts{outline: p.Outline, heading: p.Heading, start: p.StartLine, count: p.LineCount}
	switch {
	case p.Path != "" && len(p.Paths) > 0:
		return "", errors.New("give path for one note or paths for several, not both")
	case p.Path == "" && len(p.Paths) == 0:
		return "", errors.New(`a note is needed: give path, such as "Work/Spec", or paths for up to 10 notes`)
	case len(p.Paths) > maxReadPaths:
		return "", fmt.Errorf("paths names %d notes; at most %d can be read in one call", len(p.Paths), maxReadPaths)
	}
	if p.Path != "" {
		rel, err := s.existingNote(p.Path)
		if err != nil {
			return "", err
		}
		text, err := s.readText(rel)
		if err != nil {
			return "", err
		}
		return renderNote(rel, text, o, readChunk)
	}

	// Several notes share one output budget, so a long first note cannot be
	// followed by nine more of the same size. Everything written counts against
	// it: headers, errors and outlines too.
	o.batch = true
	var b strings.Builder
	left := readChunk
	emit := func(text string) {
		if len(text) > left {
			text = cutBytes(text, left) + "\n[output limit reached]\n"
		}
		b.WriteString(text)
		left = max(left-len(text), 0)
	}
	var skipped []string
	for _, name := range p.Paths {
		// Too little left for a useful part of a note: it waits for a later call.
		if left < minReadRoom {
			skipped = append(skipped, fmt.Sprintf("%q", clip(strings.TrimSpace(name), 100)))
			continue
		}
		rel, err := s.existingNote(name)
		var text string
		if err == nil {
			text, err = s.readText(rel)
		}
		if err != nil {
			emit(fmt.Sprintf("=== %s ===\n%s\n\n", clip(strings.TrimSpace(name), 200), err))
			continue
		}
		emit(fmt.Sprintf("=== %s (%s) ===\n", rel, size(text)))
		// Less two for the line breaks added below, so emit never cuts the
		// hint that says how to read on.
		out, err := renderNote(rel, text, o, left-2)
		if err != nil {
			out = err.Error()
		}
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		emit(out + "\n")
	}
	res := strings.TrimRight(b.String(), "\n") + "\n"
	if len(skipped) > 0 {
		res += fmt.Sprintf("[not shown: the output limit was reached; read_note with paths [%s] reads them]\n", strings.Join(skipped, ", "))
	}
	return res, nil
}

// maxReserve is the most renderNote sets aside for what it says after a part.
const maxReserve = 1024

// minReadRoom is the least output budget a note in a batch read is started
// with.
const minReadRoom = 2000

// readOpts is what read_note was asked for besides which note.
type readOpts struct {
	outline      bool
	heading      string
	start, count int
	batch        bool // several notes are being read, so hints name the note
}

// renderNote is a note as read_note returns it: whole when it fits in budget
// bytes and nothing narrower was asked for, otherwise a part that says which
// lines it holds and how to carry on. The part and what it says after it fit
// in budget together.
func renderNote(rel, text string, o readOpts, budget int) (string, error) {
	plain := splitLines(text)
	total := len(plain)
	if o.outline {
		return fmt.Sprintf("Outline of %q: %s, %d characters.\n%s", rel, size(text), utf8.RuneCountInString(text), listHeadings(plain)), nil
	}
	lo, hi := 1, total
	note := ""
	if o.heading != "" {
		from, to, matches, ok := sectionOf(plain, o.heading)
		if !ok {
			return "", errNoHeading(rel, o.heading, plain)
		}
		lo, hi = from, to
		if matches > 1 {
			note = fmt.Sprintf("; %d headings match, this is the first", matches)
		}
	} else if o.start <= 1 && o.count <= 0 && len(text) <= budget {
		if text == "" {
			return "(the note is empty)", nil
		}
		return text, nil
	}
	lo = max(lo, o.start)
	if o.heading != "" && lo > hi {
		return fmt.Sprintf("(the section ends at line %d)", hi), nil
	}
	if o.count > 0 {
		hi = min(hi, lo-1+o.count)
	}
	if lo > total {
		return fmt.Sprintf("(the note has only %d lines)", total), nil
	}
	how := func(from int) string {
		h := fmt.Sprintf("start_line=%d", from)
		if o.batch {
			h = fmt.Sprintf("path=%q %s", rel, h)
		}
		if o.heading != "" {
			h += fmt.Sprintf(" and heading=%q", o.heading)
		}
		return h
	}
	// Room for the lines is what is left once the longest the words after
	// them can be is set aside.
	longFmt := "[line %d is longer than shown]\n"
	moreFmt := "[lines %d-%d of %d%s; read_note with %s continues]"
	// It is capped, so a path or heading of absurd length cannot leave no
	// room at all; the words may then run past budget.
	reserve := min(1+len(fmt.Sprintf(longFmt, total))+len(fmt.Sprintf(moreFmt, total, total, total, note, how(total))), maxReserve)
	room := budget - reserve
	// A line no call could show whole is shown in part by a call for one
	// note. In a batch it waits for such a call, which has room for more of it.
	whole := readChunk - maxReserve
	raw := strings.SplitAfter(text, "\n")
	var b strings.Builder
	last := lo - 1
	long := 0
	for i := lo; i <= hi; i++ {
		line := raw[i-1]
		if b.Len()+len(line) <= room {
			b.WriteString(line)
			last = i
			continue
		}
		if b.Len() == 0 && len(line) > whole && !o.batch {
			b.WriteString(cutBytes(line, max(room, 0)))
			long = i
			last = i
		}
		break
	}
	if last < lo {
		return fmt.Sprintf("[line %d does not fit in what is left of this answer; read_note with %s continues]", lo, how(lo)), nil
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	if long > 0 {
		fmt.Fprintf(&b, longFmt, long)
	}
	if last < hi {
		fmt.Fprintf(&b, moreFmt, lo, last, total, note, how(last+1))
	} else {
		fmt.Fprintf(&b, "[lines %d-%d of %d%s]", lo, last, total, note)
	}
	return b.String(), nil
}

// searchCap is how many notes the text index is asked for. The folder filter
// is applied afterwards, so it is well above the most a call shows.
const (
	searchCap         = 1000
	searchCapInFolder = 100000
	// maxSearchShown is the most notes one search lists, and maxSearchOutput
	// about the most text it writes.
	maxSearchShown  = 50
	maxSearchOutput = 50000
)

func runSearch(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Query  string `json:"query"`
		Folder string `json:"folder"`
		Limit  int    `json:"limit"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	words := strings.Fields(strings.ToLower(p.Query))
	if len(words) == 0 {
		return "", errors.New("query is empty")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, maxSearchShown)
	s.refresh()
	folder, err := s.visibleFolder(p.Folder)
	if err != nil {
		return "", err
	}
	in := func(rel string) bool { return folder == "" || strings.HasPrefix(rel, folder+"/") }
	notes, err := s.visibleNotes()
	if err != nil {
		return "", err
	}
	visible := make(map[string]bool, len(notes))
	var hits []string
	seen := map[string]bool{}
	for _, m := range notes {
		visible[m.Path] = true
		name := strings.ToLower(m.Path)
		all := true
		for _, w := range words {
			if !strings.Contains(name, w) {
				all = false
				break
			}
		}
		if all && in(m.Path) {
			hits = append(hits, m.Path)
			seen[m.Path] = true
		}
	}
	// The index cannot filter by folder, so a search in one asks for far more
	// and filters here, rather than losing hits past the first thousand.
	ask := searchCap
	if folder != "" {
		ask = searchCapInFolder
	}
	byText, err := s.store.SearchContent(p.Query, ask)
	if err != nil {
		return "", err
	}
	sort.Strings(byText)
	for _, rel := range byText {
		if visible[rel] && !seen[rel] && in(rel) && !s.hidden(rel) {
			hits = append(hits, rel)
			seen[rel] = true
		}
	}
	where := ""
	if folder != "" {
		where = fmt.Sprintf(" in %q", folder)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No notes match %q%s.", p.Query, where), nil
	}
	var b strings.Builder
	total := fmt.Sprint(len(hits))
	if len(byText) >= ask {
		total += "+"
	}
	fmt.Fprintf(&b, "%s notes match %q%s", total, p.Query, where)
	if len(hits) > limit {
		if limit == maxSearchShown {
			fmt.Fprintf(&b, " (showing %d, the most one search lists; narrow the query or use folder for more)", limit)
		} else {
			fmt.Fprintf(&b, " (showing %d; raise limit for more)", limit)
		}
		hits = hits[:limit]
	}
	b.WriteString(":\n")
	for i, rel := range hits {
		if b.Len() >= maxSearchOutput {
			fmt.Fprintf(&b, "(… %d more notes left out to keep this short; narrow the query or use folder)\n", len(hits)-i)
			break
		}
		b.WriteString(rel)
		b.WriteString("\n")
		b.WriteString(s.matchingLines(rel, words, 3))
	}
	return b.String(), nil
}

// matchingLines is up to most lines of a note that hold one of the words, with
// their line numbers and each cut to a readable length, so a search result
// says why it matched. The note is read once.
func (s *server) matchingLines(rel string, words []string, most int) string {
	text, err := s.store.ReadNote(rel)
	if err != nil {
		return ""
	}
	var b strings.Builder
	n := 0
	for i, line := range splitLines(text) {
		lower := strings.ToLower(line)
		for _, w := range words {
			if strings.Contains(lower, w) {
				fmt.Fprintf(&b, "%4d: %s\n", i+1, clip(strings.TrimSpace(line), 160))
				n++
				break
			}
		}
		if n == most {
			break
		}
	}
	return b.String()
}

func runTags(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Tag string `json:"tag"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	s.refresh()
	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p.Tag), "#"))
	if tag == "" {
		pairs, err := s.store.AllTagged()
		if err != nil {
			return "", err
		}
		v, err := s.visibility()
		if err != nil {
			return "", err
		}
		// Counted from the notes Claude Code may see, so a tag that only a
		// protected note carries is not shown, nor counted. A note counts for
		// a tag it carries or one nested under it, as NotesWithTag has it.
		sets := map[string]map[string]bool{}
		exact := map[string]bool{}
		for _, pr := range pairs {
			if !v.note(pr.Note) {
				continue
			}
			exact[pr.Tag] = true
			for t := pr.Tag; ; {
				if sets[t] == nil {
					sets[t] = map[string]bool{}
				}
				sets[t][pr.Note.Path] = true
				i := strings.LastIndex(t, "/")
				if i < 0 {
					break
				}
				t = t[:i]
			}
		}
		names := make([]string, 0, len(exact))
		for t := range exact {
			names = append(names, t)
		}
		sort.Strings(names)
		var b strings.Builder
		shown := 0
		for _, t := range names {
			fmt.Fprintf(&b, "#%s  (%d)\n", t, len(sets[t]))
			shown++
		}
		if shown == 0 {
			return "No notes have tags.", nil
		}
		return fmt.Sprintf("%d tags:\n%s", shown, b.String()), nil
	}
	notes, err := s.store.NotesWithTag(tag)
	if err != nil {
		return "", err
	}
	v, err := s.visibility()
	if err != nil {
		return "", err
	}
	var out []string
	for _, m := range notes {
		if v.note(m) && !s.hidden(m.Path) {
			out = append(out, m.Path)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("No notes are tagged #%s.", tag), nil
	}
	return fmt.Sprintf("%d notes tagged #%s:\n%s\n", len(out), tag, strings.Join(out, "\n")), nil
}

// --- writing ----------------------------------------------------------------

// newNote resolves where a new note may go: a name that is free, not in a
// locked folder, and not a protected note's.
func (s *server) newNote(p string) (string, error) {
	rel, err := s.noteName(p)
	if err != nil {
		return "", err
	}
	// A protected note's name answers as any taken name does. A name inside a
	// locked folder has to be refused, and is, in words that do not say why.
	if s.store.NoteIsProtected(rel) && !s.store.InLockedFolder(path.Dir(rel)) {
		return "", errTaken(rel)
	}
	if s.hidden(rel) {
		return "", errNotHere(rel)
	}
	return rel, nil
}

func errTaken(rel string) error {
	return fmt.Errorf("a note called %q already exists. Use edit_note or write_note to change it, or choose another name", rel)
}

// errNotHere is the refusal for a place Claude Code may not put things.
func errNotHere(rel string) error {
	return fmt.Errorf("%q is not a place Claude Code can create or move things to; choose another name or folder", rel)
}

func runCreate(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	if p.Content == nil {
		return "", errors.New("content is needed (it can be empty)")
	}
	rel, err := s.newNote(p.Path)
	if err != nil {
		return "", err
	}
	return s.create(rel, *p.Content, false)
}

// create writes a new note, or replaces one when overwrite is set.
func (s *server) create(rel, content string, overwrite bool) (string, error) {
	if s.store.NoteExists(rel) {
		if !overwrite {
			return "", errTaken(rel)
		}
		if err := s.store.WriteNoteVersioned(rel, content); err != nil {
			return "", err
		}
		changed(storage.ExternalChange{Op: storage.ChangeWrite, Path: rel})
		return fmt.Sprintf("Replaced %q (%s).", rel, size(content)), nil
	}
	made, err := s.store.CreateNote(rel, content)
	if err != nil {
		return "", err
	}
	if !made {
		return "", errTaken(rel)
	}
	changed(storage.ExternalChange{Op: storage.ChangeWrite, Path: rel})
	return fmt.Sprintf("Created %q (%s).", rel, size(content)), nil
}

func size(text string) string {
	n := strings.Count(text, "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		n++
	}
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

func runImport(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Source    string `json:"source_path"`
		Path      string `json:"path"`
		Folder    string `json:"folder"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	// An empty source would resolve to the working directory and fail as "is
	// not a file", which sends the model looking for the wrong mistake.
	src := strings.TrimSpace(p.Source)
	if src == "" {
		return "", errors.New("source_path is required: the file to import")
	}
	if strings.HasPrefix(src, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			src = filepath.Join(home, src[2:])
		}
	}
	src, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("%s could not be read: %w", src, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", src)
	}
	if info.Size() > importMax {
		return "", fmt.Errorf("%s is too large to import (%d MB; the limit is %d MB)", src, info.Size()>>20, importMax>>20)
	}
	// The vault is never a source: a note is copied with rename_note, and a
	// protected one must not be copied out of its encryption by way of here.
	// Nor is the app's own data, which holds Version History and the index.
	if inside(src, s.store.VaultPath, storage.DataDir(), filepath.Dir(storage.ConfigPath())) {
		return "", errors.New("that file belongs to Atlas Notes itself and cannot be imported; use read_note for a note")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%s is not a text file", src)
	}
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	content := strings.ReplaceAll(string(data), "\r\n", "\n")

	dest := p.Path
	if strings.TrimSpace(dest) == "" {
		base := filepath.Base(src)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if f := folderName(p.Folder); f != "" {
			base = f + "/" + base
		}
		dest = base
	}
	rel, err := s.newNote(dest)
	if err != nil {
		return "", err
	}
	msg, err := s.create(rel, content, p.Overwrite)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Imported %s. %s", filepath.Base(src), msg), nil
}

// inside reports whether the file p is in any of dirs, at any depth. It
// compares directories by identity, not by spelling, so a symlink or another
// letter case on a file system that ignores case cannot get round it, and it
// says yes when it cannot tell.
func inside(p string, dirs ...string) bool {
	var targets []os.FileInfo
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil {
			targets = append(targets, info)
		}
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return true
	}
	for dir := filepath.Dir(real); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return true
		}
		for _, t := range targets {
			if os.SameFile(info, t) {
				return true
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			return false
		}
	}
}

// rewrite changes an existing, visible note through fn, keeping the old text
// in Version History.
func (s *server) rewrite(p string, fn func(rel, text string) (string, error)) (string, string, error) {
	rel, err := s.existingNote(p)
	if err != nil {
		return "", "", err
	}
	text, err := s.store.ReadNote(rel)
	if err != nil {
		if errors.Is(err, storage.ErrLocked) || errors.Is(err, storage.ErrNoPassword) {
			return "", "", s.errMissing(rel)
		}
		return "", "", err
	}
	next, err := fn(rel, text)
	if err != nil {
		return "", "", err
	}
	if next == text {
		return rel, next, nil
	}
	// The window may have saved the note since it was read. Writing now would
	// put back what it replaced, so the model is asked to read it again.
	if now, err := s.store.ReadNote(rel); err != nil || now != text {
		return "", "", fmt.Errorf("%q changed while this edit was being made, and was left alone. Read it again and redo the change", rel)
	}
	if err := s.store.WriteNoteVersioned(rel, next); err != nil {
		return "", "", err
	}
	changed(storage.ExternalChange{Op: storage.ChangeWrite, Path: rel})
	return rel, next, nil
}

// editSpec is one change of edit_note's edits. new_text is a pointer so that
// a missing one can be told from an empty one, which deletes.
type editSpec struct {
	Old        string  `json:"old_text"`
	New        *string `json:"new_text"`
	ReplaceAll bool    `json:"replace_all"`
}

func runEdit(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path       string     `json:"path"`
		Old        string     `json:"old_text"`
		New        *string    `json:"new_text"`
		ReplaceAll bool       `json:"replace_all"`
		Edits      []editSpec `json:"edits"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	single := p.Old != "" || p.New != nil
	switch {
	case single && len(p.Edits) > 0:
		return "", errors.New("give either old_text and new_text, or edits, not both")
	case !single && len(p.Edits) == 0:
		return "", errors.New("nothing to change: give old_text and new_text for one change, or edits for several")
	case p.ReplaceAll && len(p.Edits) > 0:
		return "", errors.New("replace_all goes inside each edit when edits is used, not beside it")
	}
	batch := len(p.Edits) > 0
	specs := p.Edits
	if !batch {
		specs = []editSpec{{p.Old, p.New, p.ReplaceAll}}
	}

	var spans []span
	replaced := 0
	rel, next, err := s.rewrite(p.Path, func(rel, text string) (string, error) {
		crlf := crlfNote(text)
		for i, e := range specs {
			// Say which edit failed, and that none was made, when there are several.
			fail := func(format string, a ...any) (string, error) {
				msg := fmt.Sprintf(format, a...)
				if batch {
					msg = fmt.Sprintf("edit %d of %d: %s\nNone of the edits was made.", i+1, len(specs), msg)
					if i > 0 {
						msg += fmt.Sprintf(" Line numbers count the text as edits 1-%d left it.", i)
					}
				}
				return "", errors.New(msg)
			}
			switch {
			case e.Old == "":
				return fail("old_text is empty. To add to the end of a note use append_to_note; to replace all of it use write_note")
			case e.New == nil:
				return fail("new_text is needed (use an empty string to delete)")
			case strings.ReplaceAll(e.Old, "\r\n", "\n") == strings.ReplaceAll(*e.New, "\r\n", "\n"):
				return fail("old_text and new_text are the same")
			}
			// A line break matches \n or \r\n, and the new text is written with
			// the line ends of what it replaces.
			at := findText(text, e.Old)
			switch {
			case len(at) == 0:
				return fail("%s", whyNotFound(rel, text, e.Old))
			case len(at) > 1 && !e.ReplaceAll:
				lines := make([]int, min(len(at), 10))
				for j := range lines {
					lines[j] = strings.Count(text[:at[j].start], "\n") + 1
				}
				list := lineNumbers(lines, 10)
				if len(at) > 10 {
					list += fmt.Sprintf(", and %d more", len(at)-10)
				}
				return fail("old_text occurs %d times in %q, at lines %s. Include more of the surrounding text so it occurs once, or set replace_all", len(at), rel, list)
			}
			if !e.ReplaceAll {
				at = at[:1]
			}
			repls := make([]string, len(at))
			for j, a := range at {
				repls[j] = endsLike(*e.New, text[a.start:a.end], crlf)
			}
			text, spans = replaceAt(text, at, repls, spans)
			replaced += len(at)
		}
		return text, nil
	})
	if err != nil {
		return "", err
	}
	var msg string
	switch {
	case batch:
		msg = fmt.Sprintf("Applied %d edits to %q (%d replacements).", len(specs), rel, replaced)
	case replaced > 1:
		msg = fmt.Sprintf("Edited %q: replaced %d occurrences.", rel, replaced)
	default:
		msg = fmt.Sprintf("Edited %q.", rel)
	}
	return msg + " The changed lines now read:\n" + changeView(next, spans), nil
}

func runAppend(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Text    string `json:"text"`
		Heading string `json:"heading"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	if p.Text == "" {
		return "", errors.New("text is empty")
	}
	add := p.Text
	if !strings.HasSuffix(add, "\n") {
		add += "\n"
	}
	var spans []span
	rel, next, err := s.rewrite(p.Path, func(rel, text string) (string, error) {
		a, nl := add, "\n"
		if crlfNote(text) {
			a, nl = toCRLF(add), "\r\n" // keep to the note's own line ends
		}
		at, prefix := len(text), ""
		if p.Heading != "" {
			plain := splitLines(text)
			from, to, _, ok := sectionOf(plain, p.Heading)
			if !ok {
				return "", errNoHeading(rel, p.Heading, plain)
			}
			// After the section's last line with something on it, so blank lines
			// that separate it from the next heading stay where they are.
			for to > from && strings.TrimSpace(plain[to-1]) == "" {
				to--
			}
			at = 0
			for _, l := range strings.SplitAfter(text, "\n")[:to] {
				at += len(l)
			}
		}
		if at > 0 && text[at-1] != '\n' {
			prefix = nl
		}
		spans = []span{{at + len(prefix), at + len(prefix) + len(a)}}
		return text[:at] + prefix + a + text[at:], nil
	})
	if err != nil {
		return "", err
	}
	under := ""
	if p.Heading != "" {
		under = fmt.Sprintf(" under %q", headingQuery(p.Heading))
	}
	return fmt.Sprintf("Added %s to %q%s. The added lines:\n%s", size(add), rel, under, changeView(next, spans)), nil
}

func runWrite(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	if p.Content == nil {
		return "", errors.New("content is needed")
	}
	rel, _, err := s.rewrite(p.Path, func(string, string) (string, error) { return *p.Content, nil })
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Replaced the text of %q (%s).", rel, size(*p.Content)), nil
}

func runRenameNote(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	from, err := s.existingNote(p.Path)
	if err != nil {
		return "", err
	}
	to, err := s.newNote(p.NewPath)
	if err != nil {
		return "", err
	}
	if to == from {
		return fmt.Sprintf("%q already has that name.", from), nil
	}
	if !strings.EqualFold(to, from) && s.store.NoteExists(to) {
		return "", fmt.Errorf("a note called %q already exists", to)
	}
	s.refresh()
	linking := s.keepLinking([]string{from})
	n, err := s.store.RenameNoteAndLinks(from, to)
	moved := s.store.NoteExists(to) && !s.store.NoteExists(from)
	if err != nil && !moved {
		if errors.Is(err, storage.ErrNameTaken) {
			return "", errTaken(to)
		}
		return "", err
	}
	s.invalidate()
	changed(storage.ExternalChange{Op: storage.ChangeRename, Path: from, To: to})
	logWrites(linking, from, to)
	msg := fmt.Sprintf("Renamed %q to %q.", from, to)
	if err != nil {
		return msg + fmt.Sprintf(" Some links to it could not be updated: %v", err), nil
	}
	if n > 0 {
		msg += fmt.Sprintf(" Updated links in %d other notes.", n)
	}
	return msg, nil
}

// keepLinking keeps the current text of every visible note that links to one
// of notes in Version History, before a rename rewrites those links, and says
// which notes they were. The rewrite is a change Claude Code made, and has to
// be undoable like any other.
func (s *server) keepLinking(notes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, rel := range notes {
		links, _ := s.store.Backlinks(rel)
		for _, l := range links {
			if seen[l] || s.hidden(l) {
				continue
			}
			seen[l] = true
			if err := s.store.KeepCurrentVersion(l); err != nil {
				log.Printf("keeping a version of %q: %v", l, err)
			}
			out = append(out, l)
		}
	}
	return out
}

// logWrites tells the window that notes changed, at the names they have after
// a move of from to to.
func logWrites(notes []string, from, to string) {
	for _, rel := range notes {
		if rel == from {
			rel = to
		} else if strings.HasPrefix(rel, from+"/") {
			rel = to + strings.TrimPrefix(rel, from)
		}
		changed(storage.ExternalChange{Op: storage.ChangeWrite, Path: rel})
	}
}

func runCreateFolder(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	rel := folderName(p.Path)
	if rel == "" {
		return "", errors.New(`a folder path is needed, such as "Work/Projects"`)
	}
	if storage.ReservedPath(rel) || s.store.InLockedFolder(rel) {
		return "", errNotHere(rel)
	}
	if s.store.FolderExists(rel) {
		return fmt.Sprintf("The folder %q already exists.", rel), nil
	}
	if s.store.NoteExists(rel) {
		return "", fmt.Errorf("there is a note called %q; a folder needs another name", rel)
	}
	if err := s.store.CreateFolder(rel); err != nil {
		return "", err
	}
	changed(storage.ExternalChange{Op: storage.ChangeFolder, Path: rel})
	return fmt.Sprintf("Created the folder %q.", rel), nil
}

// errFolderInApp is the answer for a folder that holds something protected.
// It is named, but not opened: the person deals with it in Atlas Notes.
func errFolderInApp(rel, what string) error {
	return fmt.Errorf("Claude Code cannot %s %q; the person can do it in Atlas Notes", what, rel)
}

func runRenameFolder(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	from := folderName(p.Path)
	if from == "" {
		return "", errors.New("the top of the vault cannot be renamed")
	}
	if _, err := s.visibleFolder(from); err != nil {
		return "", err
	}
	if s.store.FolderIsProtected(from) {
		return "", errFolderInApp(from, "rename or move")
	}
	to := folderName(p.NewPath)
	if to == "" {
		return "", errors.New("new_path is needed")
	}
	if to == from {
		return fmt.Sprintf("%q already has that name.", from), nil
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return "", errors.New("a folder cannot be moved inside itself")
	}
	if storage.ReservedPath(to) || s.store.InLockedFolder(to) {
		return "", errNotHere(to)
	}
	if s.store.FolderExists(to) || s.store.NoteExists(to) {
		return "", fmt.Errorf("%q is already taken", to)
	}
	s.refresh()
	notes, err := s.visibleNotes()
	if err != nil {
		return "", err
	}
	var inFolder []string
	for _, m := range notes {
		if strings.HasPrefix(m.Path, from+"/") {
			inFolder = append(inFolder, m.Path)
		}
	}
	linking := s.keepLinking(inFolder)
	n, err := s.store.RenameFolderAndLinks(from, to)
	moved := s.store.FolderExists(to) && !s.store.FolderExists(from)
	if err != nil && !moved {
		return "", err
	}
	s.invalidate()
	changed(storage.ExternalChange{Op: storage.ChangeRename, Path: from, To: to})
	logWrites(linking, from, to)
	msg := fmt.Sprintf("Renamed the folder %q to %q.", from, to)
	if err != nil {
		return msg + fmt.Sprintf(" Some links to its notes could not be updated: %v", err), nil
	}
	if n > 0 {
		msg += fmt.Sprintf(" Updated links in %d notes.", n)
	}
	return msg, nil
}

func runDeleteNote(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	if s.store.Trash == nil {
		return "", errors.New("there is no Trash on this system to move the note to, so it was not deleted")
	}
	rel, err := s.existingNote(p.Path)
	if err != nil {
		return "", err
	}
	if err := s.store.DeleteNote(rel); err != nil {
		if errors.Is(err, storage.ErrTrashFailed) {
			return "", fmt.Errorf("%q could not be moved to the Trash, so nothing was deleted", rel)
		}
		return "", err
	}
	changed(storage.ExternalChange{Op: storage.ChangeDelete, Path: rel})
	return fmt.Sprintf("Moved %q to the Trash.", rel), nil
}

func runDeleteFolder(s *server, _ storage.ClaudeAccess, args []byte) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := decode(args, &p); err != nil {
		return "", err
	}
	if s.store.Trash == nil {
		return "", errors.New("there is no Trash on this system to move the folder to, so it was not deleted")
	}
	rel := folderName(p.Path)
	if rel == "" {
		return "", errors.New("the top of the vault cannot be deleted")
	}
	if _, err := s.visibleFolder(rel); err != nil {
		return "", err
	}
	if s.store.FolderIsProtected(rel) {
		return "", errFolderInApp(rel, "delete")
	}
	if err := s.store.DeleteFolder(rel); err != nil {
		if errors.Is(err, storage.ErrTrashFailed) {
			return "", fmt.Errorf("%q could not be moved to the Trash, so nothing was deleted", rel)
		}
		return "", err
	}
	changed(storage.ExternalChange{Op: storage.ChangeDelete, Path: rel})
	return fmt.Sprintf("Moved the folder %q and everything in it to the Trash.", rel), nil
}
