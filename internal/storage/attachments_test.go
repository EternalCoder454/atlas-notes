package storage

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"atlas-notes/internal/imagefit"
	"atlas-notes/internal/vaultlock"
)

// testPNG is a small image that stays a PNG through imagefit: it has see-through
// pixels, and a photo-like opaque one might come back as a JPEG.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 16), G: uint8(y * 16), B: 90, A: 200})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stored is what SaveAttachment keeps of an image: imagefit's version of it.
func stored(t *testing.T, data []byte) ([]byte, string) {
	t.Helper()
	out, ext, err := imagefit.Fit(data)
	if err != nil {
		t.Fatal(err)
	}
	return out, ext
}

// fixedClock makes every image saved in the test carry the same time in its
// name, so the names can be compared exactly.
func fixedClock(t *testing.T) {
	t.Helper()
	old := attachmentNow
	attachmentNow = func() time.Time { return time.Date(2026, 9, 29, 10, 15, 0, 0, time.Local) }
	t.Cleanup(func() { attachmentNow = old })
}

const clockStamp = "20260929-101500"

// attachmentFiles lists the names in the vault's attachments folder.
func attachmentFiles(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(s.attachmentsPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// mustSave saves an image and fails the test if it cannot.
func mustSave(t *testing.T, s *Store, note string, data []byte) string {
	t.Helper()
	md, err := s.SaveAttachment(note, data)
	if err != nil {
		t.Fatalf("SaveAttachment(%q): %v", note, err)
	}
	return md
}

func TestSaveAndReadAttachment(t *testing.T) {
	fixedClock(t)
	s := testStore(t)
	data := testPNG(t)
	want, ext := stored(t, data)

	cases := []struct{ note, wantPath string }{
		{"Ideas", "attachments/Ideas-" + clockStamp + ext},
		{"Work/Plan", "../attachments/Plan-" + clockStamp + ext},
		{"Work/Q3/Notes", "../../attachments/Notes-" + clockStamp + ext},
	}
	for _, c := range cases {
		md := mustSave(t, s, c.note, data)
		if md != c.wantPath {
			t.Errorf("%s: path %q, want %q", c.note, md, c.wantPath)
		}
		if strings.ContainsAny(md, " \\") {
			t.Errorf("%s: path %q would need escaping", c.note, md)
		}
		got, err := s.ReadAttachment(c.note, md)
		if err != nil {
			t.Fatalf("%s: ReadAttachment: %v", c.note, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: read back %d bytes, saved %d", c.note, len(got), len(want))
		}
		// The file is where the path says it is, in the clear, for other apps.
		onDisk := filepath.Join(s.VaultPath, "attachments", path.Base(md))
		if b, err := os.ReadFile(onDisk); err != nil || !bytes.Equal(b, want) {
			t.Errorf("%s: %s is not the saved image (err %v)", c.note, onDisk, err)
		}
	}
}

func TestSaveAttachmentRefusesWhatIsNotAnImage(t *testing.T) {
	s := testStore(t)
	if _, err := s.SaveAttachment("Ideas", []byte("this is not a picture")); !errors.Is(err, imagefit.ErrNotImage) {
		t.Errorf("err = %v, want ErrNotImage", err)
	}
	if _, err := s.SaveAttachment("", testPNG(t)); err == nil {
		t.Error("an image was saved for a note with no name")
	}
	if names := attachmentFiles(t, s); len(names) != 0 {
		t.Errorf("a refused image left %v behind", names)
	}
}

func TestAttachmentNamesAreUnique(t *testing.T) {
	fixedClock(t)
	s := testStore(t)
	data := testPNG(t)
	_, ext := stored(t, data)
	stem := "attachments/Ideas-" + clockStamp

	want := []string{stem + ext, stem + "-2" + ext, stem + "-3" + ext}
	for i, w := range want {
		if md := mustSave(t, s, "Ideas", data); md != w {
			t.Errorf("save %d: %q, want %q", i+1, md, w)
		}
	}

	// A sealed image holds its name too, so it is not written over by a plain
	// one that happens to be given the same time.
	if err := os.WriteFile(filepath.Join(s.VaultPath, filepath.FromSlash(stem+"-4"+ext+".enc")), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if md := mustSave(t, s, "Ideas", data); md != stem+"-5"+ext {
		t.Errorf("after a sealed -4: %q, want -5", md)
	}
}

func TestAttachmentNameIsSanitised(t *testing.T) {
	fixedClock(t)
	s := testStore(t)
	data := testPNG(t)
	_, ext := stored(t, data)

	cases := []struct{ note, base string }{
		{"Plan (v2) #1: [draft]? 100%", "Plan-v2-1-draft-100"},
		{`Say "hi" <now> | a*b`, "Say-hi-now-a-b"},
		{"  spaced   out  ", "spaced-out"},
		{"a - b -- c", "a-b-c"},
		{"Work/Plan (old)", "Plan-old"}, // only the note's own name is used
		{"???", "note"},                 // nothing usable is left
		{".hidden", "hidden"},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)},
		{strings.Repeat("\u00e9", 50), strings.Repeat("\u00e9", 40)}, // cut by characters, not bytes
		{strings.Repeat("ab ", 20), strings.Repeat("ab-", 13) + "a"}, // the cut counts the dashes
	}
	for _, c := range cases {
		md := mustSave(t, s, c.note, data)
		want := "-" + clockStamp + ext
		got := path.Base(md)
		if !strings.HasSuffix(got, want) || strings.TrimSuffix(got, want) != c.base {
			t.Errorf("note %q: name %q, want %q", c.note, got, c.base+want)
		}
		if strings.ContainsAny(got, " ()[]<>#?%*:|\"\\") {
			t.Errorf("note %q: name %q holds a character that needs escaping", c.note, got)
		}
	}
}

func TestReadAttachmentRefusals(t *testing.T) {
	s := testStore(t)
	data, _ := stored(t, testPNG(t))
	outside := filepath.Dir(s.VaultPath) // the folder the vault sits in
	for _, f := range []string{
		filepath.Join(outside, "secret.png"),
		filepath.Join(outside, "abs.png"),
		filepath.Join(s.VaultPath, "notes.txt"),
	} {
		if err := os.WriteFile(f, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		note, ref string
		wantErr   error
	}{
		{"Note", "../secret.png", errOutsideVault},
		{"Note", "../../etc/passwd.png", errOutsideVault},
		{"A/B/Note", "../../../secret.png", errOutsideVault},
		{"Note", `..\secret.png`, errOutsideVault},
		{"Note", "https://x/y.png", errBadImagePath},
		{"Note", "file:///etc/passwd.png", errBadImagePath},
		{"Note", "/abs.png", errBadImagePath},
		{"Note", filepath.ToSlash(filepath.Join(outside, "abs.png")), errBadImagePath},
		{"Note", `C:\abs.png`, errBadImagePath},
		{"Note", "notes.txt", errBadImagePath},
		{"Note", "attachments/notes.txt", errBadImagePath},
		{"Note", "", errBadImagePath},
	}
	for _, c := range cases {
		got, err := s.ReadAttachment(c.note, c.ref)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("ReadAttachment(%q, %q) error = %v, want %v", c.note, c.ref, err, c.wantErr)
		}
		if got != nil {
			t.Errorf("ReadAttachment(%q, %q) returned %d bytes", c.note, c.ref, len(got))
		}
	}

	// A well-formed reference to nothing is missing, not refused.
	if _, err := s.ReadAttachment("Note", "attachments/none.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing image gave %v, want not-exist", err)
	}
}

func TestReadAttachmentTooLarge(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(s.attachmentsPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(s.attachmentsPath(), "big.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxAttachmentBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	if _, err := s.ReadAttachment("Note", "attachments/big.png"); !errors.Is(err, errAttachmentTooLarge) {
		t.Errorf("err = %v, want errAttachmentTooLarge", err)
	}
}

func TestReadAttachmentFallsBackAfterAMove(t *testing.T) {
	s := testStore(t)
	data := testPNG(t)
	want, _ := stored(t, data)

	md := mustSave(t, s, "Inbox/Idea", data) // "../attachments/Idea-....png"
	if err := s.WriteNote("Inbox/Idea", "![a picture]("+md+")"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameNote("Inbox/Idea", "Projects/Deep/Idea"); err != nil {
		t.Fatal(err)
	}

	// Relative to its new folder the link points at nothing, but the image is
	// still found by its name.
	got, err := s.ReadAttachment("Projects/Deep/Idea", md)
	if err != nil {
		t.Fatalf("ReadAttachment after the move: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the image read after the move is not the one saved")
	}

	// A picture that is not in attachments either is simply missing.
	if _, err := s.ReadAttachment("Projects/Deep/Idea", "../gone.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want not-exist", err)
	}
}

func TestSaveAttachmentIntoLockedNote(t *testing.T) {
	fixedClock(t)
	s := lockedStore(t)
	data := testPNG(t)
	want, ext := stored(t, data)

	if err := s.WriteNote("Work/Secret", "hush"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Work/Secret"); err != nil {
		t.Fatal(err)
	}
	md := mustSave(t, s, "Work/Secret", data)
	if md != "../attachments/Secret-"+clockStamp+ext {
		t.Errorf("path %q is not the plain name", md)
	}
	name := "Secret-" + clockStamp + ext
	if got := attachmentFiles(t, s); !reflect.DeepEqual(got, []string{name + ".enc"}) {
		t.Fatalf("attachments = %v, want only %s.enc", got, name)
	}
	sealed, err := os.ReadFile(filepath.Join(s.attachmentsPath(), name+".enc"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, want[:8]) {
		t.Error("the sealed image still holds the image's signature")
	}
	got, err := s.ReadAttachment("Work/Secret", md)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("read back: %d bytes, err %v; want the saved image", len(got), err)
	}

	// A note that is not locked yet, but is in a folder that is, is locked when
	// it is saved, so its image is sealed before the note exists.
	if err := s.CreateFolder("Vault"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockFolder("Vault"); err != nil {
		t.Fatal(err)
	}
	md = mustSave(t, s, "Vault/New", data)
	name = path.Base(md)
	files := attachmentFiles(t, s)
	if !contains(files, name+".enc") || contains(files, name) {
		t.Errorf("attachments = %v, want %s.enc and no plain %s", files, name, name)
	}
}

func TestLockingANoteSealsItsImages(t *testing.T) {
	s := lockedStore(t)
	data := testPNG(t)
	want, _ := stored(t, data)

	inNote := path.Base(mustSave(t, s, "Trip", data))
	viaName := path.Base(mustSave(t, s, "Trip", data)) // referenced by its bare name
	other := path.Base(mustSave(t, s, "Other", data))  // belongs to another note
	if err := os.MkdirAll(filepath.Join(s.VaultPath, "photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(s.VaultPath, "photos", "mine.png")
	if err := os.WriteFile(mine, want, 0o644); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{
		"![shot](attachments/" + inNote + ")",
		"![[" + viaName + "]]",
		"![mine](photos/mine.png)",
		"![web](https://example.com/w.png)",
		"![gone](attachments/gone.png)",
	}, "\n")
	// The first link is found where it says. The second is a bare name, which
	// is found the way a note that was moved finds its images: in attachments.
	if err := s.WriteNote("Trip", text); err != nil {
		t.Fatal(err)
	}

	if err := s.LockNote("Trip"); err != nil {
		t.Fatalf("LockNote: %v", err)
	}
	files := attachmentFiles(t, s)
	for _, n := range []string{inNote, viaName} {
		if !contains(files, n+".enc") || contains(files, n) {
			t.Errorf("%s was not sealed: %v", n, files)
		}
	}
	if !contains(files, other) || contains(files, other+".enc") {
		t.Errorf("%s belongs to another note and was touched: %v", other, files)
	}
	if b, err := os.ReadFile(mine); err != nil || !bytes.Equal(b, want) {
		t.Errorf("an image outside attachments was changed (err %v)", err)
	}
	if _, err := os.Stat(mine + ".enc"); err == nil {
		t.Error("an image outside attachments was sealed")
	}
	// Still shown, through the sealed form, while the vault is unlocked.
	if got, err := s.ReadAttachment("Trip", "attachments/"+inNote); err != nil || !bytes.Equal(got, want) {
		t.Errorf("sealed image read: %d bytes, err %v", len(got), err)
	}

	// Locking it again changes nothing.
	if err := s.LockNote("Trip"); err != nil {
		t.Fatalf("second LockNote: %v", err)
	}

	if err := s.UnlockNote("Trip"); err != nil {
		t.Fatalf("UnlockNote: %v", err)
	}
	files = attachmentFiles(t, s)
	for _, n := range []string{inNote, viaName} {
		if !contains(files, n) || contains(files, n+".enc") {
			t.Errorf("%s was not restored: %v", n, files)
		}
		if b, err := os.ReadFile(filepath.Join(s.attachmentsPath(), n)); err != nil || !bytes.Equal(b, want) {
			t.Errorf("%s restored wrongly (err %v)", n, err)
		}
	}
}

func TestLockingAFolderSealsItsNotesImages(t *testing.T) {
	s := lockedStore(t)
	data := testPNG(t)
	md := mustSave(t, s, "Diary/Day", data)
	name := path.Base(md)
	if err := s.WriteNote("Diary/Day", "![]("+md+")"); err != nil {
		t.Fatal(err)
	}

	if err := s.LockFolder("Diary"); err != nil {
		t.Fatal(err)
	}
	if files := attachmentFiles(t, s); !reflect.DeepEqual(files, []string{name + ".enc"}) {
		t.Fatalf("after LockFolder: %v", files)
	}
	if err := s.UnlockFolder("Diary"); err != nil {
		t.Fatal(err)
	}
	if files := attachmentFiles(t, s); !reflect.DeepEqual(files, []string{name}) {
		t.Fatalf("after UnlockFolder: %v", files)
	}
}

// A lock that cannot seal an image says so and leaves the note locked, and
// locking again, once the cause is gone, finishes the job.
func TestLockingReportsAnImageItCannotSeal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder that refuses writes")
	}
	s := lockedStore(t)
	md := mustSave(t, s, "Trip", testPNG(t))
	name := path.Base(md)
	if err := s.WriteNote("Trip", "![]("+md+")"); err != nil {
		t.Fatal(err)
	}

	dir := s.attachmentsPath()
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	err := s.LockNote("Trip")
	if err == nil || !strings.Contains(err.Error(), "still unencrypted") {
		t.Fatalf("LockNote error = %v, want one naming the unencrypted image", err)
	}
	if !s.IsNoteLocked("Trip") {
		t.Error("the note is not locked after the image failed")
	}
	if !contains(attachmentFiles(t, s), name) {
		t.Error("the plain image is gone although it was not sealed")
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Trip"); err != nil {
		t.Fatalf("second LockNote: %v", err)
	}
	if files := attachmentFiles(t, s); !reflect.DeepEqual(files, []string{name + ".enc"}) {
		t.Errorf("after the retry: %v", files)
	}
}

func TestReadAttachmentWhenTheVaultIsLocked(t *testing.T) {
	s := lockedStore(t)
	data := testPNG(t)
	secret := mustSave(t, s, "Secret", data)
	open := mustSave(t, s, "Open", data)
	if err := s.WriteNote("Secret", "![]("+secret+")"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}

	s.Lock()
	if _, err := s.ReadAttachment("Secret", secret); !errors.Is(err, ErrLocked) {
		t.Errorf("sealed image, vault locked: err = %v, want ErrLocked", err)
	}
	// An image in the clear does not need the password.
	if _, err := s.ReadAttachment("Open", open); err != nil {
		t.Errorf("plain image, vault locked: %v", err)
	}
	// And a locked note's image cannot be saved without it.
	if _, err := s.SaveAttachment("Secret", data); !errors.Is(err, ErrLocked) {
		t.Errorf("SaveAttachment, vault locked: err = %v, want ErrLocked", err)
	}

	if err := s.Unlock(testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadAttachment("Secret", secret); err != nil {
		t.Errorf("after unlocking: %v", err)
	}
}

// Sealed images are under the vault key, so changing the password has to move
// them with the notes or they could never be opened again.
func TestChangePasswordKeepsSealedImages(t *testing.T) {
	s := lockedStore(t)
	data := testPNG(t)
	want, _ := stored(t, data)
	md := mustSave(t, s, "Secret", data)
	if err := s.WriteNote("Secret", "![]("+md+")"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}

	oldKey, err := s.key()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(testPassword, "another password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	newKey, err := s.key()
	if err != nil {
		t.Fatal(err)
	}

	// The file itself is under the new key and no longer under the old one.
	sealed, err := os.ReadFile(filepath.Join(s.attachmentsPath(), path.Base(md)+".enc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vaultlock.Open(oldKey, sealed); err == nil {
		t.Error("the image still opens with the old key")
	}
	if _, err := vaultlock.Open(newKey, sealed); err != nil {
		t.Errorf("the image does not open with the new key: %v", err)
	}

	// And through the store: the new password reads it, the old one does not.
	s.Lock()
	if err := s.Unlock(testPassword); err == nil {
		t.Error("the old password still unlocks the vault")
	}
	if _, err := s.ReadAttachment("Secret", md); !errors.Is(err, ErrLocked) {
		t.Errorf("read with the wrong password: err = %v, want ErrLocked", err)
	}
	if err := s.Unlock("another password"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadAttachment("Secret", md)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("after changing the password: %d bytes, err %v", len(got), err)
	}
}

func TestListFoldersHidesTheAttachmentsFolder(t *testing.T) {
	s := testStore(t)
	mustSave(t, s, "Ideas", testPNG(t)) // makes the real one
	for _, f := range []string{"Attachments Old", "x/attachments", "x/y"} {
		if err := s.CreateFolder(f); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListFolders()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Attachments Old", "x", "x/attachments", "x/y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListFolders = %v, want %v", got, want)
	}
}

func TestUnlockingOneNoteKeepsAPictureAnotherLockedNoteShows(t *testing.T) {
	s := lockedStore(t)
	s.WriteNote("A", "# A\n")
	md, err := s.SaveAttachment("A", testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	s.WriteNote("A", "# A\n\n![]("+md+")\n")
	s.WriteNote("B", "# B\n\n![]("+md+")\n") // the same picture, copied over
	if err := s.LockNote("A"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("B"); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlockNote("A"); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(s.VaultPath, filepath.FromSlash(md))
	if isFile(plain) {
		t.Fatal("unlocking A put the picture B still locks in the clear")
	}
	if _, err := s.ReadAttachment("A", md); err != nil {
		t.Fatalf("A can no longer show its picture: %v", err)
	}
}

func TestASymlinkedPictureIsRefused(t *testing.T) {
	s := testStore(t)
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.VaultPath, "attachments")
	os.MkdirAll(dir, 0o755)
	if err := os.Symlink(outside, filepath.Join(dir, "link.png")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := s.ReadAttachment("Note", "attachments/link.png"); err == nil {
		t.Fatal("a symlink out of the vault was read")
	}
}

func TestALinkedAttachmentsFolderIsRefused(t *testing.T) {
	s := testStore(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.VaultPath, "attachments")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := s.ReadAttachment("Note", "attachments/secret.png"); err == nil {
		t.Fatal("a picture behind a linked folder outside the vault was read")
	}
}
