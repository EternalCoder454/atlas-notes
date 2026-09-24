// Package update checks whether a newer Atlas Notes has been published, and
// what changed in it. It knows nothing about GTK or about installing anything:
// it fetches a file, reads it, and compares two version numbers.
//
// The notes it reads are the ones written for people using the app, not the
// changelog written for people working on it.
package update

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultNotesURL is where the user-facing release notes for a branch live.
// The %s is the branch the app's update channel follows.
const DefaultNotesURL = "https://raw.githubusercontent.com/EternalCoder454/atlas-notes/%s/WHATSNEW.md"

// maxNotesBytes caps what the check will read, so a wrong URL cannot pull
// something enormous into memory.
const maxNotesBytes = 64 << 10

// maxNoteLines is how many bullets a release may show before the list is cut
// short. Eight is what fits in the dialog without scrolling at an ordinary
// window height — measured, not guessed: at ten the last line sits under the
// buttons, and a list you have to scroll is not the plain summary this is
// meant to be. The shipped notes are held to the same limit in
// TestShippedNotes, so a release can never quietly lose its last line.
const maxNoteLines = 8

// Checker looks for a newer version. The zero value is not useful; call New.
type Checker struct {
	// NotesURL is a template with one %s, the branch to read notes from.
	NotesURL string
	// Client is the HTTP client used for the one request a check makes.
	Client *http.Client
}

// New returns a Checker pointed at the project's published release notes, with
// a short timeout — an update check must never be something the user waits on.
func New() *Checker {
	return &Checker{
		NotesURL: DefaultNotesURL,
		Client:   &http.Client{Timeout: 8 * time.Second},
	}
}

// Release is a published version and what changed in it.
type Release struct {
	Version string   // e.g. "0.5.5"
	Notes   []string // one short line per change, in plain language
}

// Check asks whether branch has a version newer than current. It returns a nil
// Release when the app is already up to date — the common case, and the one
// that must stay quiet.
func (c *Checker) Check(ctx context.Context, branch, current string) (*Release, error) {
	rel, err := c.latest(ctx, branch)
	if err != nil {
		return nil, err
	}
	if !Newer(current, rel.Version) {
		return nil, nil
	}
	return rel, nil
}

// latest fetches the newest entry from a branch's release notes.
func (c *Checker) latest(ctx context.Context, branch string) (*Release, error) {
	url := fmt.Sprintf(c.NotesURL, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Nothing about this machine goes with the request; it is a plain read.
	req.Header.Set("Accept", "text/plain")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release notes: %s", resp.Status)
	}
	return ParseNotes(io.LimitReader(resp.Body, maxNotesBytes))
}

// ParseNotes reads release notes in the form
//
//	## 0.5.5
//	- A line about a change
//	- Another one
//
// and returns the first (newest) entry. Anything that is not a version heading
// or a bullet under one is ignored, so the file can carry a title and
// explanation for anyone who opens it directly.
func ParseNotes(r io.Reader) (*Release, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 8<<10), maxNotesBytes)

	var rel *Release
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "## "):
			if rel != nil {
				return rel, nil // the next heading ends the newest entry
			}
			version := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			version = strings.Trim(version, "vV[]")
			if version == "" {
				continue
			}
			rel = &Release{Version: version}
		case rel != nil && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")):
			if len(rel.Notes) < maxNoteLines {
				rel.Notes = append(rel.Notes, strings.TrimSpace(line[2:]))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if rel == nil {
		return nil, fmt.Errorf("release notes: no version found")
	}
	return rel, nil
}

// Newer reports whether remote is a later version than current. Versions are
// compared field by field as numbers, so 0.5.10 is newer than 0.5.9. Anything
// that does not parse is treated as "not newer": a check that cannot make sense
// of what it fetched must not offer an update.
func Newer(current, remote string) bool {
	c, okC := parseVersion(current)
	r, okR := parseVersion(remote)
	if !okC || !okR {
		return false
	}
	for i := 0; i < len(c) || i < len(r); i++ {
		cv, rv := at(c, i), at(r, i)
		if rv != cv {
			return rv > cv
		}
	}
	return false
}

// parseVersion splits "0.5.4" into its numbers, ignoring a leading "v" and any
// pre-release suffix ("0.5.4-beta" compares as 0.5.4).
func parseVersion(s string) ([]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return nil, false
	}
	var out []int
	for _, field := range strings.Split(s, ".") {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func at(v []int, i int) int {
	if i < len(v) {
		return v[i]
	}
	return 0
}
