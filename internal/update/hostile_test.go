package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What the update check reads comes off the network, so it is treated as
// hostile input. These are the shapes a server can send that a parser written
// for well-formed notes would not expect: more than it said, less than it
// said, nothing at all, or something designed to be rendered rather than read.

// serve runs a checker against a handler and returns what Check made of it.
func serve(t *testing.T, current string, h http.HandlerFunc) (*Release, error) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := &Checker{NotesURL: srv.URL + "/%s/WHATSNEW.md", Client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Check(ctx, "main", current)
}

// TestOversizedBodyIsBounded: a server can answer a request for a few hundred
// bytes with as much as it likes. The check must not read it all.
func TestOversizedBodyIsBounded(t *testing.T) {
	const huge = 64 << 20 // 64 MB of bullets
	var served int
	rel, err := serve(t, "0.5.0", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("## 9.9.9\n"))
		line := []byte("- " + strings.Repeat("A", 200) + "\n")
		for served = 0; served < huge; served += len(line) {
			if _, werr := w.Write(line); werr != nil {
				return // the client hung up, which is the point
			}
		}
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel == nil {
		t.Fatal("no release from a body that did contain one")
	}
	if len(rel.Notes) > maxNoteLines {
		t.Errorf("kept %d notes from an endless body, cap is %d", len(rel.Notes), maxNoteLines)
	}
	for _, n := range rel.Notes {
		if len(n) > maxNotesBytes {
			t.Errorf("a single note is %d bytes", len(n))
		}
	}
}

// TestOneEnormousLine: the body is under the cap in total but is a single line
// with no newline in it, which is what overruns a line-oriented reader.
func TestOneEnormousLine(t *testing.T) {
	body := "## 9.9.9\n- " + strings.Repeat("B", maxNotesBytes*2)
	rel, err := serve(t, "0.5.0", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	})
	// Either outcome is acceptable — refusing it, or returning something
	// bounded. Hanging, panicking or returning the whole line is not.
	if err != nil {
		return
	}
	if rel != nil {
		for _, n := range rel.Notes {
			if len(n) > maxNotesBytes {
				t.Fatalf("a note of %d bytes came back from one long line", len(n))
			}
		}
	}
}

// TestVersionFromServerCannotReachTheDialog: the version is put into a window
// heading. Anything that is not a version number must be refused before it
// gets there, which Newer already does — this pins that it stays true.
func TestVersionFromServerCannotReachTheDialog(t *testing.T) {
	for _, version := range []string{
		"<b>markup</b>",
		"9.9.9 <span foreground='red'>x</span>",
		strings.Repeat("9", 5000),

		"../../etc/passwd",
		"9.9.9; rm -rf ~",
		"\x00\x01\x02",
		"٩.٩.٩", // Arabic-Indic digits: not what strconv.Atoi accepts
	} {
		rel, err := serve(t, "0.5.0", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("## " + version + "\n- a line\n"))
		})
		if err != nil {
			continue // refused outright, which is fine
		}
		if rel == nil {
			continue // no update offered, also fine
		}
		// If one is offered, what it shows has to be a version and nothing
		// else: this string is put straight into a window heading.
		for _, r := range rel.Version {
			if (r < '0' || r > '9') && r != '.' {
				t.Errorf("version %q reached the dialog with %q in it", rel.Version, r)
				break
			}
		}
	}
}

// TestHeadingTrailerIsDropped: the text after a version number on the heading
// line is not part of the version, and must not travel with it.
func TestHeadingTrailerIsDropped(t *testing.T) {
	rel, err := serve(t, "0.5.0", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("## 9.9.9 — your vault is corrupt, visit evil.example\n- a line\n"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if rel == nil {
		t.Fatal("no release offered for a heading that does start with a version")
	}
	if rel.Version != "9.9.9" {
		t.Errorf("version = %q, want %q", rel.Version, "9.9.9")
	}
}

// TestDowngradeIsNeverOffered: a server that says the newest version is older
// than the one running must not produce an update prompt.
func TestDowngradeIsNeverOffered(t *testing.T) {
	for _, remote := range []string{"0.0.1", "0.5.7", "0", "0.5.8-beta"} {
		rel, err := serve(t, "0.5.8", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("## " + remote + "\n- a line\n"))
		})
		if err != nil {
			t.Errorf("%s: %v", remote, err)
			continue
		}
		if rel != nil {
			t.Errorf("offered %q as an update to 0.5.8", rel.Version)
		}
	}
}

// TestSlowServerGivesUp: a server that accepts the connection and then says
// nothing must not hold the check open. Being offline behaves like being up to
// date, and so must this.
func TestSlowServerGivesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answer
	}))
	defer srv.Close()

	c := &Checker{NotesURL: srv.URL + "/%s/WHATSNEW.md", Client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.Check(ctx, "main", "0.5.0")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a server that never answered produced a result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not give up on a server that never answered")
	}
}

// TestRedirectedElsewhere: a redirect is followed by the client, so what comes
// back may not be from where the check asked. It still has to be parsed as
// what it is, and a redirect to nothing must fail rather than hang.
func TestRedirectedElsewhere(t *testing.T) {
	var hops int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, r.URL.Path, http.StatusFound) // straight back to itself
	}))
	defer srv.Close()
	c := &Checker{NotesURL: srv.URL + "/%s/WHATSNEW.md", Client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Check(ctx, "main", "0.5.0"); err == nil {
		t.Error("an endless redirect produced a result")
	}
	if hops > 20 {
		t.Errorf("followed %d redirects", hops)
	}
}

// TestBinaryBodies: anything at all can come back with a 200.
func TestBinaryBodies(t *testing.T) {
	bodies := map[string][]byte{
		"empty":       {},
		"nul bytes":   make([]byte, 4096),
		"png header":  {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"html":        []byte("<html><body>404 not found</body></html>"),
		"json":        []byte(`{"error":"nope"}`),
		"broken utf8": {0xff, 0xfe, 0xfd, '\n', '#', '#', ' ', '1', '\n'},
	}
	for name, body := range bodies {
		rel, err := serve(t, "0.5.0", func(w http.ResponseWriter, r *http.Request) {
			w.Write(body)
		})
		if err != nil {
			continue // refused, which is the expected outcome for most of these
		}
		if rel != nil {
			t.Errorf("%s: produced an update offer for version %q", name, rel.Version)
		}
	}
}
