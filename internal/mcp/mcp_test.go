package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"atlas-notes/internal/storage"
)

// testEnv points the data and config directories at a temporary folder, so
// nothing here comes near a real vault, and returns the vault's path.
func testEnv(t *testing.T, access storage.ClaudeAccess) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("ATLAS_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("ATLAS_CONFIG_HOME", filepath.Join(root, "config"))
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	setAccess(t, vault, access)
	return vault
}

func setAccess(t *testing.T, vault string, access storage.ClaudeAccess) {
	t.Helper()
	cfg := storage.DefaultConfig()
	cfg.VaultPath = vault
	cfg.Claude = access
	if err := storage.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

var everything = storage.ClaudeAccess{Enabled: true, Read: true, Write: true, Delete: true}

// testServer is a server whose deletes go to a folder beside the vault.
func testServer(t *testing.T, vault string) (*server, string) {
	t.Helper()
	trash := filepath.Join(filepath.Dir(vault), "trash")
	os.MkdirAll(trash, 0o755)
	s := newServer(Options{Version: "test", Out: &bytes.Buffer{}, Trash: func(p string) error {
		return os.Rename(p, filepath.Join(trash, filepath.Base(p)))
	}})
	t.Cleanup(s.close)
	return s, trash
}

func rpc(t *testing.T, s *server, method string, params any) *response {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return s.handleMessage(raw)
}

// call runs a tool and returns its text and whether it failed.
func call(t *testing.T, s *server, name string, args map[string]any) (string, bool) {
	t.Helper()
	resp := rpc(t, s, "tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		t.Fatalf("%s: protocol error %v", name, resp.Error.Message)
	}
	r := resp.Result.(toolResult)
	return r.Content[0].Text, r.IsError
}

func mustCall(t *testing.T, s *server, name string, args map[string]any) string {
	t.Helper()
	text, failed := call(t, s, name, args)
	if failed {
		t.Fatalf("%s(%v) failed: %s", name, args, text)
	}
	return text
}

func mustFail(t *testing.T, s *server, name string, args map[string]any) string {
	t.Helper()
	text, failed := call(t, s, name, args)
	if !failed {
		t.Fatalf("%s(%v) succeeded but should not have: %s", name, args, text)
	}
	return text
}

func toolNames(t *testing.T, s *server) []string {
	t.Helper()
	resp := rpc(t, s, "tools/list", nil)
	var names []string
	for _, tl := range resp.Result.(map[string]any)["tools"].([]map[string]any) {
		names = append(names, tl["name"].(string))
	}
	return names
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	testEnv(t, everything)
	s, _ := testServer(t, "")
	r := rpc(t, s, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if got := r.Result.(map[string]any)["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("asked for 2025-06-18, got %v", got)
	}
	r = rpc(t, s, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	if got := r.Result.(map[string]any)["protocolVersion"]; got != protocolVersions[0] {
		t.Errorf("unknown version answered with %v, want %s", got, protocolVersions[0])
	}
	if r := rpc(t, s, "no/such/method", nil); r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method: %+v", r)
	}
	if r := s.handleMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled"}`)); r != nil {
		t.Errorf("a notification got a reply: %+v", r)
	}
	if r := s.handleMessage([]byte(`{not json`)); r == nil || r.Error.Code != codeParse {
		t.Errorf("bad JSON: %+v", r)
	}
}

func TestRunSpeaksLineDelimitedJSON(t *testing.T) {
	testEnv(t, everything)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":"two","method":"ping"}` + "\n")
	var out bytes.Buffer
	if code := Run(Options{In: in, Out: &out}); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 replies, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[1], `"id":"two"`) {
		t.Errorf("ping reply lost its id: %s", lines[1])
	}
}

func TestSwitchedOffOffersOnlyStatus(t *testing.T) {
	vault := testEnv(t, storage.ClaudeAccess{Enabled: false, Read: true, Write: true, Delete: true})
	os.WriteFile(filepath.Join(vault, "Note.md"), []byte("hello"), 0o644)
	s, _ := testServer(t, vault)
	if names := toolNames(t, s); len(names) != 1 || names[0] != "atlas_status" {
		t.Errorf("tools offered while switched off: %v", names)
	}
	msg := mustFail(t, s, "read_note", map[string]any{"path": "Note"})
	if !strings.Contains(msg, "Settings") {
		t.Errorf("refusal does not say where to turn it on: %s", msg)
	}
	if s.store != nil {
		t.Error("the vault was opened while access was off")
	}
	if status := mustCall(t, s, "atlas_status", nil); !strings.Contains(status, "Let Claude Code use your notes") {
		t.Errorf("status: %s", status)
	}
}

func TestMissingConfigAllowsNothing(t *testing.T) {
	testEnv(t, everything)
	os.Remove(storage.ConfigPath())
	s, _ := testServer(t, "")
	mustFail(t, s, "list_notes", nil)
	if _, err := os.Stat(storage.ConfigPath()); err == nil {
		t.Error("the server wrote a config file")
	}
}

func TestEachSwitchGatesItsTools(t *testing.T) {
	vault := testEnv(t, storage.ClaudeAccess{Enabled: true, Read: true})
	os.WriteFile(filepath.Join(vault, "Note.md"), []byte("hello"), 0o644)
	s, _ := testServer(t, vault)
	names := strings.Join(toolNames(t, s), " ")
	for _, want := range []string{"read_note", "list_notes", "search_notes"} {
		if !strings.Contains(names, want) {
			t.Errorf("%s missing with read on: %s", want, names)
		}
	}
	for _, unwanted := range []string{"create_note", "edit_note", "delete_note"} {
		if strings.Contains(names, unwanted) {
			t.Errorf("%s offered with only read on", unwanted)
		}
	}
	mustCall(t, s, "read_note", map[string]any{"path": "Note"})
	if msg := mustFail(t, s, "create_note", map[string]any{"path": "New", "content": "x"}); !strings.Contains(msg, "Create and edit notes") {
		t.Errorf("write refusal: %s", msg)
	}
	mustFail(t, s, "delete_note", map[string]any{"path": "Note"})

	// The switch is read on every call, not when the server started.
	setAccess(t, vault, storage.ClaudeAccess{Enabled: true, Write: true})
	mustCall(t, s, "create_note", map[string]any{"path": "New", "content": "x"})
	mustFail(t, s, "read_note", map[string]any{"path": "Note"})
}

func TestNoteLifecycle(t *testing.T) {
	vault := testEnv(t, everything)
	s, trash := testServer(t, vault)

	mustCall(t, s, "create_note", map[string]any{"path": "Work/Spec.md", "content": "# Spec\n\nThe widget is blue. #design\n"})
	if !s.store.NoteExists("Work/Spec") {
		t.Fatal(`"Work/Spec.md" should have made the note "Work/Spec"`)
	}
	mustFail(t, s, "create_note", map[string]any{"path": "Work/Spec", "content": "again"})

	if got := mustCall(t, s, "read_note", map[string]any{"path": "Work/Spec"}); got != "# Spec\n\nThe widget is blue. #design\n" {
		t.Errorf("read back %q", got)
	}
	mustCall(t, s, "edit_note", map[string]any{"path": "Work/Spec", "old_text": "blue", "new_text": "green"})
	mustFail(t, s, "edit_note", map[string]any{"path": "Work/Spec", "old_text": "purple", "new_text": "red"})
	mustCall(t, s, "append_to_note", map[string]any{"path": "Work/Spec", "text": "- [ ] Paint it"})
	got := mustCall(t, s, "read_note", map[string]any{"path": "Work/Spec"})
	if got != "# Spec\n\nThe widget is green. #design\n- [ ] Paint it\n" {
		t.Errorf("after edits: %q", got)
	}
	if h, _ := s.store.History("Work/Spec"); len(h) < 2 {
		t.Errorf("each edit should keep the previous text in history; have %d versions", len(h))
	}

	if out := mustCall(t, s, "search_notes", map[string]any{"query": "green"}); !strings.Contains(out, "Work/Spec") || !strings.Contains(out, "widget is green") {
		t.Errorf("search: %s", out)
	}
	if out := mustCall(t, s, "list_tags", map[string]any{"tag": "#design"}); !strings.Contains(out, "Work/Spec") {
		t.Errorf("tags: %s", out)
	}
	if out := mustCall(t, s, "list_notes", map[string]any{"folder": "Work"}); !strings.Contains(out, "Work/Spec  [checklist]") {
		t.Errorf("list: %s", out)
	}

	mustCall(t, s, "create_note", map[string]any{"path": "Index", "content": "See [[Work/Spec]].\n"})
	s.lastRefresh = s.lastRefresh.Add(-refreshGap) // let the rename see the link
	mustCall(t, s, "rename_note", map[string]any{"path": "Work/Spec", "new_path": "Archive/Spec"})
	if idx := mustCall(t, s, "read_note", map[string]any{"path": "Index"}); !strings.Contains(idx, "[[Archive/Spec]]") {
		t.Errorf("link not updated: %q", idx)
	}

	mustCall(t, s, "delete_note", map[string]any{"path": "Archive/Spec"})
	if s.store.NoteExists("Archive/Spec") {
		t.Error("deleted note is still there")
	}
	if entries, _ := os.ReadDir(trash); len(entries) != 1 {
		t.Errorf("the note did not go to the Trash: %v", entries)
	}

	changes, _, _ := storage.ReadExternalChanges(0)
	var ops []string
	for _, c := range changes {
		ops = append(ops, c.Op)
	}
	if joined := strings.Join(ops, ","); !strings.Contains(joined, "rename") || !strings.Contains(joined, "delete") {
		t.Errorf("changes logged for the window: %v", ops)
	}
}

func TestImportFile(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	src := filepath.Join(t.TempDir(), "spec.md")
	os.WriteFile(src, []byte("\uFEFF# Spec\r\nLine two\r\n"), 0o644)

	if msg := mustCall(t, s, "import_file", map[string]any{"source_path": src, "folder": "Work"}); !strings.Contains(msg, `"Work/spec"`) {
		t.Errorf("import: %s", msg)
	}
	if got, _ := s.store.ReadNote("Work/spec"); got != "# Spec\nLine two\n" {
		t.Errorf("imported text %q", got)
	}
	mustFail(t, s, "import_file", map[string]any{"source_path": src, "folder": "Work"})
	mustCall(t, s, "import_file", map[string]any{"source_path": src, "path": "Work/spec", "overwrite": true})

	bin := filepath.Join(t.TempDir(), "photo.md")
	os.WriteFile(bin, []byte{0x89, 'P', 'N', 'G', 0, 0}, 0o644)
	mustFail(t, s, "import_file", map[string]any{"source_path": bin})

	// A model that names the note but not the file (path instead of
	// source_path) has to be told which argument it left out.
	if msg := mustFail(t, s, "import_file", map[string]any{"path": src}); !strings.Contains(msg, "source_path") {
		t.Errorf("got %q, want it to name source_path", msg)
	}
}

func TestLongNoteComesInParts(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	line := strings.Repeat("x", 99) + "\n"
	mustCall(t, s, "create_note", map[string]any{"path": "Long", "content": strings.Repeat(line, 1500)})
	first := mustCall(t, s, "read_note", map[string]any{"path": "Long"})
	// The hint after the part counts against the chunk too.
	if len(first) > readChunk || !strings.HasSuffix(first, "[lines 1-598 of 1500; read_note with start_line=599 continues]") {
		t.Errorf("first part is %d bytes and ends %q", len(first), first[len(first)-80:])
	}
	if part := mustCall(t, s, "read_note", map[string]any{"path": "Long", "start_line": 1500}); part != line+"[lines 1500-1500 of 1500]" {
		t.Errorf("last line: %q", part)
	}
}

// protectedVault sets up a vault with protected notes in every arrangement:
// a locked note, a locked folder, and a folder that holds both kinds.
func protectedVault(t *testing.T) (vault string, sealed map[string][]byte) {
	t.Helper()
	vault = testEnv(t, everything)
	st, err := storage.Open(vault, filepath.Join(t.TempDir(), "app-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, text string) {
		if err := st.WriteNote(rel, text); err != nil {
			t.Fatal(err)
		}
	}
	write("Secret", "# Secret\nthe vault code is zebra-7781 #private\n")
	write("Private/Plan", "# Plan\nzebra plans\n")
	write("Mixed/Hidden", "# Hidden\nzebra hidden\n")
	write("Mixed/Open", "# Open\nzebra in the open\n")
	write("Public", "# Public\nNothing to see.\n")
	if err := st.SetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Secret", "Mixed/Hidden"} {
		if err := st.LockNote(rel); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.LockFolder("Private"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	sealed = map[string][]byte{}
	filepath.WalkDir(vault, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			sealed[p] = data
		}
		return nil
	})
	return vault, sealed
}

func TestProtectedNotesAreInvisible(t *testing.T) {
	vault, before := protectedVault(t)
	s, trash := testServer(t, vault)

	listing := mustCall(t, s, "list_notes", nil) + mustCall(t, s, "list_folders", nil) +
		mustCall(t, s, "list_notes", map[string]any{"folder": "Mixed"}) +
		mustCall(t, s, "search_notes", map[string]any{"query": "zebra"}) +
		mustCall(t, s, "search_notes", map[string]any{"query": "secret"}) +
		mustCall(t, s, "search_notes", map[string]any{"query": "plan"}) +
		mustCall(t, s, "list_tags", nil) +
		mustCall(t, s, "list_tags", map[string]any{"tag": "private"}) +
		mustCall(t, s, "atlas_status", nil)
	for _, leak := range []string{"Secret", "Private", "Hidden", "Plan", "7781"} {
		if strings.Contains(listing, leak) {
			t.Errorf("%q shows up in what Claude can see:\n%s", leak, listing)
		}
	}
	if !strings.Contains(listing, "Mixed/Open") || !strings.Contains(listing, "Public") {
		t.Errorf("unprotected notes are missing:\n%s", listing)
	}
	mustFail(t, s, "list_notes", map[string]any{"folder": "Private"})

	// A protected note is answered for exactly as a missing one is.
	missing := mustFail(t, s, "read_note", map[string]any{"path": "Nothing"})
	for _, rel := range []string{"Secret", "Secret.md", "Private/Plan", "Mixed/Hidden", "/Secret", "Mixed/../Secret"} {
		got := mustFail(t, s, "read_note", map[string]any{"path": rel})
		want := strings.Replace(missing, `"Nothing"`, `"`+storage.CleanNoteName(strings.TrimSuffix(rel, ".md"))+`"`, 1)
		if got != want {
			t.Errorf("read_note(%q) = %q, want the not-found answer %q", rel, got, want)
		}
	}

	for _, rel := range []string{"Secret", "Private/Plan", "Mixed/Hidden"} {
		mustFail(t, s, "edit_note", map[string]any{"path": rel, "old_text": "zebra", "new_text": "x"})
		mustFail(t, s, "append_to_note", map[string]any{"path": rel, "text": "x"})
		mustFail(t, s, "write_note", map[string]any{"path": rel, "content": "x"})
		mustFail(t, s, "rename_note", map[string]any{"path": rel, "new_path": "Stolen"})
		mustFail(t, s, "delete_note", map[string]any{"path": rel})
		mustFail(t, s, "create_note", map[string]any{"path": rel, "content": "x"})
	}
	// Nothing goes into a locked folder or on top of a protected note.
	mustFail(t, s, "create_note", map[string]any{"path": "Private/New", "content": "x"})
	mustFail(t, s, "create_note", map[string]any{"path": "Private/Deeper/New", "content": "x"})
	mustFail(t, s, "rename_note", map[string]any{"path": "Public", "new_path": "Private/Public"})
	mustFail(t, s, "rename_note", map[string]any{"path": "Public", "new_path": "Secret"})
	mustFail(t, s, "create_folder", map[string]any{"path": "Private/Sub"})
	mustFail(t, s, "import_file", map[string]any{"source_path": filepath.Join(vault, "Secret.md.enc")})
	mustFail(t, s, "import_file", map[string]any{"source_path": filepath.Join(vault, "Mixed", "Open.md")})
	src := filepath.Join(t.TempDir(), "x.md")
	os.WriteFile(src, []byte("x"), 0o644)
	mustFail(t, s, "import_file", map[string]any{"source_path": src, "path": "Private/x"})
	mustFail(t, s, "import_file", map[string]any{"source_path": src, "path": "Secret", "overwrite": true})

	// Folders with anything protected in them stay where they are.
	mustFail(t, s, "rename_folder", map[string]any{"path": "Private", "new_path": "Elsewhere"})
	mustFail(t, s, "rename_folder", map[string]any{"path": "Mixed", "new_path": "Elsewhere"})
	mustFail(t, s, "rename_folder", map[string]any{"path": "Mixed", "new_path": "Private/Mixed"})
	mustFail(t, s, "delete_folder", map[string]any{"path": "Private"})
	mustFail(t, s, "delete_folder", map[string]any{"path": "Mixed"})
	mustFail(t, s, "delete_folder", map[string]any{"path": ""})

	// The unprotected note beside a protected one is still usable.
	mustCall(t, s, "edit_note", map[string]any{"path": "Mixed/Open", "old_text": "open", "new_text": "light"})

	if entries, _ := os.ReadDir(trash); len(entries) != 0 {
		t.Errorf("something went to the Trash: %v", entries)
	}
	for p, data := range before {
		if !strings.HasSuffix(p, ".enc") && filepath.Base(p) != ".atlas-locked" && filepath.Base(p) != ".atlas-lock.json" {
			continue
		}
		now, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(now, data) {
			t.Errorf("protected file %s changed or went missing (%v)", p, err)
		}
	}
	for _, stray := range []string{"Secret.md", "Private/New.md", "Private/Sub", "Stolen.md", "Elsewhere"} {
		if _, err := os.Stat(filepath.Join(vault, stray)); err == nil {
			t.Errorf("%s was created", stray)
		}
	}
}

func TestReservedFoldersAreOutOfReach(t *testing.T) {
	vault, _ := protectedVault(t)
	// A locked note's pictures are sealed in the attachments folder.
	os.MkdirAll(filepath.Join(vault, "attachments"), 0o755)
	sealedImage := filepath.Join(vault, "attachments", "Secret-1.png.enc")
	os.WriteFile(sealedImage, []byte("sealed"), 0o644)
	os.MkdirAll(filepath.Join(vault, ".git"), 0o755)
	os.WriteFile(filepath.Join(vault, ".git", "config.md"), []byte("x"), 0o644)
	s, trash := testServer(t, vault)

	for _, dir := range []string{"attachments", ".git"} {
		mustFail(t, s, "delete_folder", map[string]any{"path": dir})
		mustFail(t, s, "rename_folder", map[string]any{"path": dir, "new_path": "Moved"})
		mustFail(t, s, "list_notes", map[string]any{"folder": dir})
		mustFail(t, s, "create_folder", map[string]any{"path": dir + "/Sub"})
		mustFail(t, s, "create_note", map[string]any{"path": dir + "/New", "content": "x"})
		mustFail(t, s, "rename_note", map[string]any{"path": "Public", "new_path": dir + "/Public"})
		mustFail(t, s, "rename_folder", map[string]any{"path": "Mixed", "new_path": dir + "/Mixed"})
	}
	mustFail(t, s, "read_note", map[string]any{"path": ".git/config"})
	// A folder holding only a sealed picture is protected too.
	os.MkdirAll(filepath.Join(vault, "Pics"), 0o755)
	os.WriteFile(filepath.Join(vault, "Pics", "a.png.enc"), []byte("sealed"), 0o644)
	mustFail(t, s, "delete_folder", map[string]any{"path": "Pics"})

	if _, err := os.Stat(sealedImage); err != nil {
		t.Errorf("the sealed picture moved: %v", err)
	}
	if entries, _ := os.ReadDir(trash); len(entries) != 0 {
		t.Errorf("something went to the Trash: %v", entries)
	}
}

func TestNoteLockedSinceTheLastScanIsHidden(t *testing.T) {
	vault, _ := protectedVault(t)
	s, _ := testServer(t, vault)
	mustCall(t, s, "list_notes", nil) // the server's index now has Public unlocked

	st, err := storage.Open(vault, filepath.Join(t.TempDir(), "window-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Unlock("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := st.LockNote("Public"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Well inside refreshGap: the index still says Public is unlocked.
	if out := mustCall(t, s, "list_notes", nil); strings.Contains(out, "Public") {
		t.Errorf("a note locked since the last scan is listed:\n%s", out)
	}
	if out := mustCall(t, s, "search_notes", map[string]any{"query": "public"}); strings.Contains(out, "Public") {
		t.Errorf("a note locked since the last scan is found:\n%s", out)
	}
	mustFail(t, s, "read_note", map[string]any{"path": "Public"})
}

func TestListTagsRollsNestedTagsUp(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	mustCall(t, s, "create_note", map[string]any{"path": "A", "content": "#proj #proj/x"})
	mustCall(t, s, "create_note", map[string]any{"path": "B", "content": "#proj/x"})
	out := mustCall(t, s, "list_tags", nil)
	if !strings.Contains(out, "#proj  (2)") || !strings.Contains(out, "#proj/x  (2)") {
		t.Errorf("tags: %s", out)
	}
}

// note makes a note through the tool, as the model would.
func note(t *testing.T, s *server, rel, content string) {
	t.Helper()
	mustCall(t, s, "create_note", map[string]any{"path": rel, "content": content})
}

func TestEditNoteBatchIsAllOrNothing(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	const start = "# T\nalpha\nbeta\ngamma\n"
	note(t, s, "N", start)

	// The second edit sees the text the first left.
	out := mustCall(t, s, "edit_note", map[string]any{"path": "N", "edits": []map[string]any{
		{"old_text": "alpha", "new_text": "one"},
		{"old_text": "one\nbeta", "new_text": "two"},
	}})
	if got, _ := s.store.ReadNote("N"); got != "# T\ntwo\ngamma\n" {
		t.Errorf("after the batch: %q", got)
	}
	if !strings.Contains(out, "Applied 2 edits") || !strings.Contains(out, "   2| two") {
		t.Errorf("result: %s", out)
	}

	note(t, s, "M", start)
	msg := mustFail(t, s, "edit_note", map[string]any{"path": "M", "edits": []map[string]any{
		{"old_text": "alpha", "new_text": "one"},
		{"old_text": "nowhere", "new_text": "x"},
	}})
	if !strings.Contains(msg, "edit 2 of 2") {
		t.Errorf("the error does not name the failed edit: %s", msg)
	}
	if got, _ := s.store.ReadNote("M"); got != start {
		t.Errorf("a failed batch wrote something: %q", got)
	}
}

func TestEditNoteNeedsOneForm(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "abc\n")
	for name, args := range map[string]map[string]any{
		"neither": {"path": "N"},
		"both": {"path": "N", "old_text": "a", "new_text": "b",
			"edits": []map[string]any{{"old_text": "b", "new_text": "c"}}},
	} {
		if msg := mustFail(t, s, "edit_note", args); !strings.Contains(msg, "edits") {
			t.Errorf("%s: %s", name, msg)
		}
	}
}

func TestEditNoteShowsTheResult(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}

	for i, tc := range []struct {
		name  string
		edits []map[string]any
		want  []string
		not   []string
	}{
		{"one change with context", []map[string]any{{"old_text": "line 10\n", "new_text": "ten\n"}},
			[]string{"   9| line 9", "  10| ten", "  11| line 11"}, []string{"line 8", "line 12"}},
		{"a deletion shows its surroundings", []map[string]any{{"old_text": "line 20\n", "new_text": ""}},
			[]string{"  19| line 19", "  20| line 21"}, nil},
		{"far apart regions are separated", []map[string]any{
			{"old_text": "line 3\n", "new_text": "three\n"}, {"old_text": "line 25\n", "new_text": "twenty-five\n"}},
			[]string{"   3| three", "  25| twenty-five", "..."}, []string{"line 15"}},
		{"close regions merge", []map[string]any{
			{"old_text": "line 3\n", "new_text": "three\n"}, {"old_text": "line 5\n", "new_text": "five\n"}},
			[]string{"   3| three", "   4| line 4", "   5| five"}, []string{"..."}},
	} {
		rel := fmt.Sprint("N", i) // a fresh note for each, so line numbers hold
		note(t, s, rel, sb.String())
		out := mustCall(t, s, "edit_note", map[string]any{"path": rel, "edits": tc.edits})
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: %q missing from\n%s", tc.name, w, out)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(out, n) {
				t.Errorf("%s: %q should not be in\n%s", tc.name, n, out)
			}
		}
	}

	// A large change is cut to about 40 lines.
	for i := 31; i <= 90; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	note(t, s, "Big", sb.String())
	out := mustCall(t, s, "edit_note", map[string]any{"path": "Big", "old_text": "line", "new_text": "row", "replace_all": true})
	if n := strings.Count(out, "\n"); n > 45 || !strings.Contains(out, "more changed lines)") {
		t.Errorf("%d lines in the view:\n%s", n, out)
	}
}

func TestEditNoteExplainsAMiss(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# T\nfoo\nbar\nfoo\n\n    indented   text  \nthe  plan:  go\nmore\n")

	msg := mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "foo", "new_text": "x"})
	if !strings.Contains(msg, "2 times") || !strings.Contains(msg, "lines 2, 4") {
		t.Errorf("multiple matches: %s", msg)
	}

	// Spacing differs: the lines are shown as stored, and nothing is applied.
	msg = mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "the plan: go", "new_text": "x"})
	if !strings.Contains(msg, "line 7") || !strings.Contains(msg, "   7| the  plan:  go") {
		t.Errorf("whitespace hint: %s", msg)
	}
	msg = mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "indented text\r\n", "new_text": "x"})
	if !strings.Contains(msg, "line 6") {
		t.Errorf("trailing space and line ends: %s", msg)
	}
	if got, _ := s.store.ReadNote("N"); !strings.Contains(got, "the  plan:  go") {
		t.Errorf("a fuzzy match was applied: %q", got)
	}

	// Only the first line is there.
	msg = mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "bar\nbaz", "new_text": "x"})
	if !strings.Contains(msg, "at line 3") || !strings.Contains(msg, "   3| bar") {
		t.Errorf("first line hint: %s", msg)
	}
	msg = mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "zzz", "new_text": "x"})
	if !strings.Contains(msg, "read_note") {
		t.Errorf("plain miss: %s", msg)
	}
}

func TestReadNoteOutlineAndHeading(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# Title\nintro\n## Plan\nstep\n```sh\n# not a heading\n```\n### Detail\nfine\n## Other\nend\n")

	out := mustCall(t, s, "read_note", map[string]any{"path": "N", "outline": true})
	for _, w := range []string{"   1| # Title", "   3| ## Plan", "   8| ### Detail", "  10| ## Other", "11 lines"} {
		if !strings.Contains(out, w) {
			t.Errorf("outline lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "not a heading") {
		t.Errorf("outline listed a line in a code block:\n%s", out)
	}

	out = mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "  ## plan "})
	if !strings.HasPrefix(out, "## Plan\nstep\n") || !strings.Contains(out, "fine") ||
		strings.Contains(out, "end") || !strings.Contains(out, "[lines 3-9 of 11]") {
		t.Errorf("section: %q", out)
	}
	out = mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "Detail"})
	if !strings.HasPrefix(out, "### Detail\nfine\n") || strings.Contains(out, "Other") {
		t.Errorf("sub-section: %q", out)
	}
	msg := mustFail(t, s, "read_note", map[string]any{"path": "N", "heading": "Nope"})
	if !strings.Contains(msg, "## Plan") || !strings.Contains(msg, "Nope") {
		t.Errorf("missing heading: %s", msg)
	}
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "start_line": 2, "line_count": 2}); !strings.HasSuffix(out, "[lines 2-3 of 11]") {
		t.Errorf("part: %q", out)
	}
}

func TestReadNoteBatch(t *testing.T) {
	vault, _ := protectedVault(t)
	s, _ := testServer(t, vault)
	out := mustCall(t, s, "read_note", map[string]any{"paths": []string{"Public", "Nothing", "Secret", "Mixed/Open"}})
	if !strings.Contains(out, "=== Public (2 lines) ===") || !strings.Contains(out, "Nothing to see.") ||
		!strings.Contains(out, "=== Mixed/Open") || !strings.Contains(out, "=== Nothing ===\nthere is no note called") {
		t.Errorf("batch:\n%s", out)
	}
	if strings.Contains(out, "7781") || strings.Contains(out, "zebra-") {
		t.Errorf("a protected note was read:\n%s", out)
	}
	// A protected note's slot reads as a missing note's does.
	if !strings.Contains(out, "=== Secret ===\nthere is no note called \"Secret\"") {
		t.Errorf("protected slot:\n%s", out)
	}
	mustFail(t, s, "read_note", map[string]any{})
	mustFail(t, s, "read_note", map[string]any{"path": "Public", "paths": []string{"Public"}})
	mustFail(t, s, "read_note", map[string]any{"paths": make([]string, 11)})

	// The budget is shared.
	line := strings.Repeat("x", 99) + "\n"
	note(t, s, "A", strings.Repeat(line, 500))
	note(t, s, "B", strings.Repeat(line, 500))
	out = mustCall(t, s, "read_note", map[string]any{"paths": []string{"A", "B"}})
	if len(out) > readChunk+500 || !strings.Contains(out, `path="B" start_line=`) {
		t.Errorf("shared budget: %d bytes, ends %q", len(out), out[len(out)-120:])
	}
}

func TestAppendUnderHeading(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# T\n## Tasks\n- [ ] a\n- [ ] b\n\n## Done\n- [x] c\n")
	out := mustCall(t, s, "append_to_note", map[string]any{"path": "N", "heading": "tasks", "text": "- [ ] new"})
	if got, _ := s.store.ReadNote("N"); got != "# T\n## Tasks\n- [ ] a\n- [ ] b\n- [ ] new\n\n## Done\n- [x] c\n" {
		t.Errorf("note: %q", got)
	}
	if !strings.Contains(out, "   5| - [ ] new") || !strings.Contains(out, "   4| - [ ] b") {
		t.Errorf("view: %s", out)
	}
	// The last section runs to the end of the note.
	mustCall(t, s, "append_to_note", map[string]any{"path": "N", "heading": "## Done", "text": "- [x] d"})
	if got, _ := s.store.ReadNote("N"); !strings.HasSuffix(got, "- [x] c\n- [x] d\n") {
		t.Errorf("last section: %q", got)
	}
	msg := mustFail(t, s, "append_to_note", map[string]any{"path": "N", "heading": "Nope", "text": "x"})
	if !strings.Contains(msg, "## Tasks") {
		t.Errorf("missing heading: %s", msg)
	}
	out = mustCall(t, s, "append_to_note", map[string]any{"path": "N", "text": "end"})
	if !strings.Contains(out, "end") {
		t.Errorf("plain append: %s", out)
	}
}

func TestSearchShowsLinesAndFilters(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "Work/A", "# A\nnothing\nthe widget is here\nand a widget again\n")
	note(t, s, "Home/B", "widget at home\n")
	note(t, s, "Work/Deep/C", "widget "+strings.Repeat("long ", 60)+"\n")

	out := mustCall(t, s, "search_notes", map[string]any{"query": "widget"})
	if !strings.Contains(out, "3 notes match") || !strings.Contains(out, "   3: the widget is here") ||
		!strings.Contains(out, "   4: and a widget again") {
		t.Errorf("search:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("a long line was not cut:\n%s", out)
	}
	out = mustCall(t, s, "search_notes", map[string]any{"query": "widget", "folder": "Work"})
	if strings.Contains(out, "Home/B") || !strings.Contains(out, "2 notes match") || !strings.Contains(out, "Work/Deep/C") {
		t.Errorf("folder filter:\n%s", out)
	}
	out = mustCall(t, s, "search_notes", map[string]any{"query": "widget", "limit": 1})
	if !strings.Contains(out, "3 notes match") || !strings.Contains(out, "raise limit") {
		t.Errorf("truncated:\n%s", out)
	}
	mustFail(t, s, "search_notes", map[string]any{"query": "widget", "folder": "Nowhere"})
}

func TestMissingNoteSuggestsSimilarNames(t *testing.T) {
	vault, _ := protectedVault(t)
	s, _ := testServer(t, vault)
	note(t, s, "Work/Roadmap", "x")
	note(t, s, "Spec", "x")

	msg := mustFail(t, s, "read_note", map[string]any{"path": "Old/Roadmap"})
	if !strings.Contains(msg, `Did you mean "Work/Roadmap"`) || !strings.Contains(msg, "list_notes or search_notes") {
		t.Errorf("same name: %s", msg)
	}
	if msg := mustFail(t, s, "read_note", map[string]any{"path": "Specc"}); !strings.Contains(msg, `"Spec"`) {
		t.Errorf("typo: %s", msg)
	}
	if msg := mustFail(t, s, "edit_note", map[string]any{"path": "Road", "old_text": "a", "new_text": "b"}); !strings.Contains(msg, `"Work/Roadmap"`) {
		t.Errorf("contained: %s", msg)
	}
	// Close to a protected note's name, but nothing of it shows, and it is
	// answered as a missing note is.
	for _, ask := range []string{"Secret", "Secrets", "Other/Secret", "Plan", "Hidden", "Mixed/Hiden"} {
		msg := mustFail(t, s, "read_note", map[string]any{"path": ask})
		for _, leak := range []string{"Secret\"", "Private/Plan", "Mixed/Hidden", "Did you mean \"Secret"} {
			if strings.Contains(strings.Replace(msg, `"`+ask+`"`, "", 1), leak) {
				t.Errorf("%q leaks %q: %s", ask, leak, msg)
			}
		}
	}
}

func TestInstructionsAndDescriptionsFitClaudeCode(t *testing.T) {
	// Claude Code cuts both at 2048 characters, without saying so.
	if n := len([]rune(instructions)); n >= 1900 {
		t.Errorf("instructions are %d characters; keep them under 1900", n)
	}
	for _, tl := range tools {
		if n := len([]rune(tl.description)); n >= 600 {
			t.Errorf("%s: description is %d characters; keep it under 600", tl.name, n)
		}
		if tl.description == "" || !strings.Contains(tl.description, ".") {
			t.Errorf("%s: description should start with a sentence", tl.name)
		}
	}
}

func TestSearchLimitsAndFolderBeyondTheFirstThousand(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	for i := 0; i < 60; i++ {
		note(t, s, fmt.Sprintf("Many/n%02d", i), "gadget "+strings.Repeat("filler ", 20)+"\n")
	}
	out := mustCall(t, s, "search_notes", map[string]any{"query": "gadget", "limit": 500})
	if n := strings.Count(out, "Many/n"); n != maxSearchShown || !strings.Contains(out, "60 notes match") {
		t.Errorf("limit not capped at %d: %d listed\n%s", maxSearchShown, n, out[:200])
	}
	// Notes with long names make the output big; it stops near the cap and says so.
	long := strings.Repeat("g", 200)
	for i := 0; i < 50; i++ {
		note(t, s, fmt.Sprintf("Wide/%s%02d", long, i), "x "+strings.Repeat("word ", 5)+"\n")
	}
	out = mustCall(t, s, "search_notes", map[string]any{"query": "gggg", "limit": 50})
	if len(out) > maxSearchOutput+2000 {
		t.Errorf("output is %d bytes", len(out))
	}
	// A folder search asks the index for more than the usual cap.
	if out := mustCall(t, s, "search_notes", map[string]any{"query": "gadget", "folder": "Many"}); !strings.Contains(out, "60 notes match") {
		t.Errorf("folder search:\n%s", out[:200])
	}
}

func TestLongLinesAreClipped(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	long := strings.Repeat("é", 1000)
	note(t, s, "N", "# "+long+"\nshort\n")
	out := mustCall(t, s, "read_note", map[string]any{"path": "N", "outline": true})
	if !strings.Contains(out, "…") || strings.Count(out, "é") > maxLineRunes {
		t.Errorf("outline line not clipped: %d runes", len([]rune(out)))
	}
	out = mustCall(t, s, "edit_note", map[string]any{"path": "N", "old_text": "short", "new_text": "brief"})
	if strings.Count(out, "é") > maxLineRunes {
		t.Errorf("view line not clipped")
	}
	// One line bigger than the whole budget is cut, and says so.
	note(t, s, "Wide", strings.Repeat("x", readChunk+5000)+"\nnext\n")
	out = mustCall(t, s, "read_note", map[string]any{"path": "Wide"})
	if len(out) > readChunk+300 || !strings.Contains(out, "[line 1 is longer than shown]") {
		t.Errorf("long line: %d bytes, ends %q", len(out), out[len(out)-100:])
	}
}

func TestBatchReadCountsEverythingAgainstTheBudget(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	var heads strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&heads, "## Heading number %d %s\n", i, strings.Repeat("w", 250))
	}
	note(t, s, "H", heads.String())
	paths := []string{"H", "H", "H", "H"}
	out := mustCall(t, s, "read_note", map[string]any{"paths": paths, "outline": true})
	if len(out) > readChunk+500 || !strings.Contains(out, "[not shown") {
		t.Errorf("outlines: %d bytes", len(out))
	}
	// Errors are bounded too.
	names := make([]string, 10)
	for i := range names {
		names[i] = strings.Repeat("n", 20000)
	}
	out = mustCall(t, s, "read_note", map[string]any{"paths": names})
	if len(out) > readChunk+1500 {
		t.Errorf("errors: %d bytes", len(out))
	}
}

func TestEditNoteNewTextAndReplaceAll(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "a b a\n")
	if msg := mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "b"}); !strings.Contains(msg, "new_text is needed") {
		t.Errorf("missing new_text: %s", msg)
	}
	if msg := mustFail(t, s, "edit_note", map[string]any{"path": "N", "edits": []map[string]any{{"old_text": "b"}}}); !strings.Contains(msg, "edit 1 of 1") || !strings.Contains(msg, "new_text is needed") {
		t.Errorf("missing new_text in edits: %s", msg)
	}
	mustCall(t, s, "edit_note", map[string]any{"path": "N", "old_text": " b", "new_text": ""})
	if got, _ := s.store.ReadNote("N"); got != "a a\n" {
		t.Errorf("an empty new_text should delete: %q", got)
	}
	if msg := mustFail(t, s, "edit_note", map[string]any{"path": "N", "replace_all": true,
		"edits": []map[string]any{{"old_text": "a", "new_text": "b"}}}); !strings.Contains(msg, "replace_all") {
		t.Errorf("replace_all beside edits: %s", msg)
	}
	// A big note with very many hits is replaced at once.
	note(t, s, "Big", strings.Repeat("ab\n", 300000))
	start := time.Now()
	out := mustCall(t, s, "edit_note", map[string]any{"path": "Big", "old_text": "a", "new_text": "xyz", "replace_all": true})
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("replace_all took %v", d)
	}
	if got, _ := s.store.ReadNote("Big"); !strings.HasPrefix(got, "xyzb\nxyzb\n") || strings.Count(got, "xyz") != 300000 {
		t.Errorf("replace_all result wrong")
	}
	if !strings.Contains(out, "300000 occurrences") {
		t.Errorf("result: %.200s", out)
	}
}

func TestCRLFNotes(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	// The store hands back a note's line ends as written.
	mustCall(t, s, "list_notes", nil) // opens the vault
	os.WriteFile(filepath.Join(vault, "Win.md"), []byte("# T\r\none\r\ntwo\r\n"), 0o644)
	s.lastRefresh = s.lastRefresh.Add(-refreshGap)
	if got, err := s.store.ReadNote("Win"); err != nil || got != "# T\r\none\r\ntwo\r\n" {
		t.Fatalf("ReadNote gave %q, %v", got, err)
	}
	mustCall(t, s, "edit_note", map[string]any{"path": "Win", "old_text": "one\ntwo", "new_text": "uno\ndos\ntres"})
	if got, _ := s.store.ReadNote("Win"); got != "# T\r\nuno\r\ndos\r\ntres\r\n" {
		t.Errorf("edit: %q", got)
	}
	mustCall(t, s, "append_to_note", map[string]any{"path": "Win", "text": "four\nfive"})
	if got, _ := s.store.ReadNote("Win"); got != "# T\r\nuno\r\ndos\r\ntres\r\nfour\r\nfive\r\n" {
		t.Errorf("append: %q", got)
	}
	mustCall(t, s, "append_to_note", map[string]any{"path": "Win", "heading": "T", "text": "six"})
	if got, _ := s.store.ReadNote("Win"); strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("mixed line ends after a heading append: %q", got)
	}
}

func TestHeadingClosingHashesAndSectionEnd(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# T\n## Plan ##\nstep\n## C#\nx\n")
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "Plan"}); !strings.HasPrefix(out, "## Plan ##\nstep\n") {
		t.Errorf("closing hashes: %q", out)
	}
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "C#"}); !strings.HasPrefix(out, "## C#\n") {
		t.Errorf("a # that belongs to the title: %q", out)
	}
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "Plan", "start_line": 50}); out != "(the section ends at line 3)" {
		t.Errorf("past the section: %q", out)
	}
}

func TestNearMatchShowsAtMostAView(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# T\n"+strings.Repeat("a  b\n", 100))
	msg := mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": strings.Repeat("a b\n", 100), "new_text": "x"})
	if n := strings.Count(msg, "| a  b"); n != maxViewLines || !strings.Contains(msg, "(… 60 more lines)") || !strings.Contains(msg, "lines 2-101") {
		t.Errorf("%d lines shown:\n%.300s", n, msg)
	}
}

func TestBatchReadKeepsTheHintWhole(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	// One-character lines leave no slack for the hint to hide in.
	note(t, s, "A", strings.Repeat(strings.Repeat("x", 99)+"\n", 500))
	note(t, s, "B", strings.Repeat("y\n", 20000))
	out := mustCall(t, s, "read_note", map[string]any{"paths": []string{"A", "B"}})
	if !strings.HasSuffix(out, "continues]\n") || !strings.Contains(out, `path="B" start_line=`) || strings.Contains(out, "[output limit reached]") || len(out) > readChunk {
		t.Errorf("%d bytes, ends %q", len(out), out[max(len(out)-160, 0):])
	}
	// So little left that B waits for the next call rather than show a scrap.
	note(t, s, "C", strings.Repeat(strings.Repeat("x", 99)+"\n", (readChunk-1000)/100))
	out = mustCall(t, s, "read_note", map[string]any{"paths": []string{"C", "B"}})
	if strings.Contains(out, "=== B") || !strings.Contains(out, `paths ["B"]`) {
		t.Errorf("ends %q", out[max(len(out)-200, 0):])
	}
}

func TestCappedLimitDoesNotSayRaiseIt(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	for i := 0; i <= maxListNotes; i++ {
		note(t, s, fmt.Sprintf("Many/n%03d", i), "gadget\n")
	}
	out := mustCall(t, s, "list_notes", map[string]any{"limit": 5000})
	if strings.Contains(out, "raise limit") || !strings.Contains(out, "the most one call lists") {
		t.Errorf("list_notes: %.200s", out)
	}
	if out := mustCall(t, s, "list_notes", map[string]any{"limit": 3}); !strings.Contains(out, "raise limit") {
		t.Errorf("list_notes under the cap: %.200s", out)
	}
	out = mustCall(t, s, "search_notes", map[string]any{"query": "gadget", "limit": 500})
	if strings.Contains(out, "raise limit") || !strings.Contains(out, "the most one search lists") {
		t.Errorf("search_notes: %.200s", out)
	}
}

func TestMixedLineEnds(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	mustCall(t, s, "list_notes", nil) // opens the vault
	// A block pasted with LF into a CRLF note.
	os.WriteFile(filepath.Join(vault, "Mix.md"), []byte("# T\r\none\r\ntwo\r\nthree\nfour\n"), 0o644)
	s.lastRefresh = s.lastRefresh.Add(-refreshGap)
	mustCall(t, s, "edit_note", map[string]any{"path": "Mix", "old_text": "three\nfour", "new_text": "3\n4"})
	if got, _ := s.store.ReadNote("Mix"); got != "# T\r\none\r\ntwo\r\n3\n4\n" {
		t.Errorf("LF stretch: %q", got)
	}
	// Across the two kinds, the new text takes the CRLF ends it meets.
	mustCall(t, s, "edit_note", map[string]any{"path": "Mix", "old_text": "two\n3", "new_text": "2\nIII"})
	if got, _ := s.store.ReadNote("Mix"); got != "# T\r\none\r\n2\r\nIII\n4\n" {
		t.Errorf("mixed stretch: %q", got)
	}
	// Several stretches, each kept to its own ends.
	os.WriteFile(filepath.Join(vault, "Mix2.md"), []byte("a\r\nb\r\na\nb\n"), 0o644)
	s.lastRefresh = s.lastRefresh.Add(-refreshGap)
	out := mustCall(t, s, "edit_note", map[string]any{"path": "Mix2", "old_text": "a\nb", "new_text": "c\nd\ne", "replace_all": true})
	if got, _ := s.store.ReadNote("Mix2"); got != "c\r\nd\r\ne\r\nc\nd\ne\n" {
		t.Errorf("replace_all: %q", got)
	}
	if !strings.Contains(out, "   6| e") {
		t.Errorf("change view: %s", out)
	}
}

func TestHeadingQueryAsTheOutlineShowsIt(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "# T\n## Plan ##\nstep\n## C#\nx\n")
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "## Plan ##"}); !strings.HasPrefix(out, "## Plan ##\nstep\n") {
		t.Errorf("read: %q", out)
	}
	out := mustCall(t, s, "append_to_note", map[string]any{"path": "N", "heading": "## Plan ##", "text": "more"})
	if !strings.Contains(out, `under "Plan"`) {
		t.Errorf("append: %s", out)
	}
	if got, _ := s.store.ReadNote("N"); got != "# T\n## Plan ##\nstep\nmore\n## C#\nx\n" {
		t.Errorf("append: %q", got)
	}
	if out := mustCall(t, s, "read_note", map[string]any{"path": "N", "heading": "## C#"}); !strings.HasPrefix(out, "## C#\n") {
		t.Errorf("C#: %q", out)
	}
}

func TestFindTextLineEnds(t *testing.T) {
	text := "a\r\nb\na\nb\r\n\r\nx"
	for _, c := range []struct {
		old  string
		want []span
	}{
		{"a\nb", []span{{0, 4}, {5, 8}}},
		{"a\r\nb", []span{{0, 4}, {5, 8}}},
		{"b\n", []span{{3, 5}, {7, 10}}},
		{"\n\nx", []span{{8, 13}}},
		{"b\nc", nil},
	} {
		if got := findText(text, c.old); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%q: %v, want %v", c.old, got, c.want)
		}
	}
	// A very large old_text is scanned, not compiled.
	big := strings.Repeat("line\n", 400000)
	if got := findText(big+"end", big+"end"); len(got) != 1 {
		t.Errorf("big: %v", got)
	}
}

func TestEditOnlyLineEndsIsNoChange(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "N", "a\nb\n")
	mustFail(t, s, "edit_note", map[string]any{"path": "N", "old_text": "a\r\nb", "new_text": "a\nb"})
}

func TestBatchLeavesAHugeLineToAOneNoteCall(t *testing.T) {
	vault := testEnv(t, everything)
	s, _ := testServer(t, vault)
	note(t, s, "Wide", strings.Repeat("z", readChunk+5000)+"\nend\n")
	out := mustCall(t, s, "read_note", map[string]any{"paths": []string{"Wide"}})
	if strings.Contains(out, "zzz") || !strings.Contains(out, `path="Wide" start_line=1 continues]`) {
		t.Errorf("batch: %.200s", out)
	}
	out = mustCall(t, s, "read_note", map[string]any{"path": "Wide"})
	if !strings.Contains(out, "[line 1 is longer than shown]") || !strings.Contains(out, "start_line=2") || len(out) > readChunk {
		t.Errorf("one note: %d bytes, ends %q", len(out), out[len(out)-120:])
	}
}
