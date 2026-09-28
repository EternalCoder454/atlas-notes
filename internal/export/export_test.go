package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// everything is a note that uses every piece of Markdown the exporter knows,
// and a few things it has to get right that are easy to get wrong.
const everything = `# Trip plan

Leaving on **Friday**, back *Sunday*. Bring ~~the tent~~ a *warm* coat.
A second line of the same paragraph, with ` + "`inline code`" + ` and a [link](https://example.org/a?b=1&c=2).

## Packing
- Boots
- Socks
  - Wool, not cotton
- [ ] Book the ferry <!-- priority:high due:2026-10-03 -->
- [x] Charge the camera <!-- priority:low -->

### Route
1. North on the coast road
2. Ferry at 10
3. Walk to the hut

> Take the ferry, not the bridge.
> The bridge is closed until spring.

---

` + "```go\nfunc main() {\n\tfmt.Println(\"a < b && c > d\")\n    indented\n}\n```" + `

Ends with a character XML cannot hold: ` + "\x01" + ` and an ampersand & a <tag>.
`

func TestParseUnderstandsTheNote(t *testing.T) {
	bl := parse(everything)
	var kinds []kind
	for _, b := range bl {
		kinds = append(kinds, b.kind)
	}
	want := []kind{heading, paragraph, heading, bullet, bullet, bullet, task, task,
		heading, numbered, numbered, numbered, quote, divider, code, paragraph}
	if len(kinds) != len(want) {
		t.Fatalf("got %d blocks %v, want %d", len(kinds), kinds, len(want))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("block %d is %v, want %v", i, kinds[i], want[i])
		}
	}
	if bl[1].lines == nil || len(bl[1].lines) != 2 {
		t.Errorf("a paragraph's second line should stay a line of it: %+v", bl[1].lines)
	}
	if bl[5].level != 1 {
		t.Errorf("a nested bullet has depth %d", bl[5].level)
	}
	if bl[6].detail != "high priority, due 3 October 2026" || bl[6].checked {
		t.Errorf("task detail: %q, checked %v", bl[6].detail, bl[6].checked)
	}
	if !bl[7].checked || bl[7].detail != "low priority" {
		t.Errorf("done task: %+v", bl[7])
	}
	if len(bl[12].lines) != 2 {
		t.Errorf("a two-line quote became %d lines", len(bl[12].lines))
	}
	if !strings.Contains(bl[14].code, "\tfmt.Println") || bl[14].lang != "go" {
		t.Errorf("code block lost its indentation or language: %q %q", bl[14].code, bl[14].lang)
	}
}

func TestInlineFormatting(t *testing.T) {
	runs := parseInline("a **b *c* d** ~~e~~ `*f*` [g **h**](http://x) *")
	var got []string
	for _, r := range runs {
		tag := ""
		if r.bold {
			tag += "B"
		}
		if r.italic {
			tag += "I"
		}
		if r.strike {
			tag += "S"
		}
		if r.mono {
			tag += "M"
		}
		if r.link != "" {
			tag += "L"
		}
		got = append(got, tag+":"+r.text)
	}
	want := []string{":a ", "B:b ", "BI:c", "B: d", ": ", "S:e", ": ", "M:*f*", ": ", "L:g ", "BL:h", ": *"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("runs:\n got %v\nwant %v", got, want)
	}
}

// Every zip format's XML parts must be well-formed, whatever the note held.
func TestZipFormatsAreWellFormed(t *testing.T) {
	for _, id := range []string{"docx", "odt"} {
		data, err := Render(id, `A "quoted" <title> & more`, everything)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s is not a zip: %v", id, err)
		}
		for _, f := range z.File {
			if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
				continue
			}
			r, _ := f.Open()
			dec := xml.NewDecoder(r)
			for {
				if _, err := dec.Token(); err == io.EOF {
					break
				} else if err != nil {
					t.Errorf("%s: %s is not well-formed XML: %v", id, f.Name, err)
					break
				}
			}
			r.Close()
		}
	}
}

// An OpenDocument file is recognised by its first bytes: the mimetype entry
// first in the zip, and stored, so its text sits at a fixed offset.
func TestODTStartsWithItsMimetype(t *testing.T) {
	data, err := Render("odt", "t", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if string(data[30:38]) != "mimetype" || string(data[38:77]) != "application/vnd.oasis.opendocument.text" {
		t.Fatalf("the file does not start with a stored mimetype: %q", data[30:80])
	}
	// Its header has to say how big it is. Streamed, it says 0 and sets the
	// data-descriptor flag, and LibreOffice refuses to open the file at all.
	flags := uint16(data[6]) | uint16(data[7])<<8
	size := uint32(data[18]) | uint32(data[19])<<8 | uint32(data[20])<<16 | uint32(data[21])<<24
	if flags&0x8 != 0 || size != 39 {
		t.Fatalf("the mimetype entry is streamed (flags %#04x, size %d in its header)", flags, size)
	}
}

func TestODTKeepsWhitespace(t *testing.T) {
	got := odtText("    four\tand  two")
	want := `<text:s text:c="4"/>four<text:tab/>and <text:s text:c="1"/>two`
	if got != want {
		t.Errorf("odtText:\n got %s\nwant %s", got, want)
	}
}

func TestHTMLIsEscapedAndSafe(t *testing.T) {
	page, _ := Render("html", "<script>", "a <b> & [click](javascript:alert(1)) [ok](https://x.org)")
	s := string(page)
	for _, bad := range []string{"<script>", "<b>", "javascript:"} {
		if strings.Contains(s, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	if !strings.Contains(s, `href="https://x.org"`) {
		t.Error("a safe link was dropped")
	}
}

func TestMarkdownIsTheNoteAsItIs(t *testing.T) {
	got, _ := Render("md", "t", everything)
	if string(got) != everything {
		t.Error("the Markdown export changed the note")
	}
}

func TestFileName(t *testing.T) {
	f, _ := Lookup("docx")
	for in, want := range map[string]string{
		"Trip plan":      "Trip plan.docx",
		`a/b\c:d*e?f"g`:  "a b c d e f g.docx",
		"   ":            "Note.docx",
		"Café \x01notes": "Café notes.docx",
	} {
		if got := FileName(in, f); got != want {
			t.Errorf("FileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The real test: LibreOffice opens each document, and the text a person would
// read is all there. ATLAS_SOFFICE picks which LibreOffice; otherwise the one
// on PATH. Skipped in -short, where there is none, and where the one there
// cannot open a text document at all: a LibreOffice installed without Writer
// says "source file could not be loaded" about everything, which is true of
// it and says nothing about these files.
func TestLibreOfficeOpensTheExports(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	soffice := os.Getenv("ATLAS_SOFFICE")
	if soffice == "" {
		var err error
		if soffice, err = exec.LookPath("soffice"); err != nil {
			t.Skip("LibreOffice is not installed")
		}
	}
	dir := t.TempDir()
	convert := func(in, outdir string) ([]byte, error) {
		// Its own profile, so this never touches, or waits on, a LibreOffice
		// the user has open; and no display, so it can never show a window.
		cmd := exec.Command(soffice, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--headless", "--convert-to", "txt:Text (encoded):UTF8", "--outdir", outdir, in)
		cmd.Env = append(os.Environ(), "DISPLAY=", "WAYLAND_DISPLAY=")
		return cmd.CombinedOutput()
	}

	probe := filepath.Join(dir, "probe.txt")
	os.WriteFile(probe, []byte("probe\n"), 0o644)
	if out, err := convert(probe, filepath.Join(dir, "probe-out")); err != nil {
		t.Skipf("this LibreOffice cannot open a text document (is Writer installed?): %v %s", err, out)
	}

	for _, id := range []string{"docx", "odt", "html"} {
		f, _ := Lookup(id)
		data, err := Render(id, "Trip plan", everything)
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, "in-"+id+f.Ext)
		os.WriteFile(in, data, 0o644)
		out := filepath.Join(dir, "out-"+id)
		if b, err := convert(in, out); err != nil {
			t.Fatalf("%s: LibreOffice could not open it: %v\n%s", id, err, b)
		}
		text, err := os.ReadFile(filepath.Join(out, "in-"+id+".txt"))
		if err != nil {
			t.Fatalf("%s: LibreOffice produced nothing: %v", id, err)
		}
		for _, want := range []string{"Trip plan", "Friday", "Wool, not cotton", "Book the ferry",
			"high priority, due 3 October 2026", "Walk to the hut", "The bridge is closed",
			"a < b && c > d", "an ampersand & a <tag>", "link"} {
			if !strings.Contains(string(text), want) {
				t.Errorf("%s: LibreOffice's reading of it lacks %q", id, want)
			}
		}
	}
}
