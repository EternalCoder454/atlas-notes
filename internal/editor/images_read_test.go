package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPicture(t *testing.T) {
	cases := []struct {
		name    string
		texture bool
		mimes   []string
		want    bool
	}{
		{"a picture alone", false, []string{"image/png"}, true},
		{"a texture alone", true, nil, true},
		{"picture and plain text", false, []string{"image/png", "text/plain"}, false},
		{"picture and utf-8 text", false, []string{"image/png", "text/plain;charset=utf-8"}, false},
		{"texture and text", true, []string{"text/plain;charset=utf-8"}, false},
		{"text alone", false, []string{"text/plain"}, false},
		{"html is not plain text", false, []string{"image/png", "text/html"}, true},
		{"nothing", false, nil, false},
	}
	for _, c := range cases {
		if got := isPicture(c.texture, c.mimes); got != c.want {
			t.Errorf("%s: isPicture = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadImageFile(t *testing.T) {
	dir := t.TempDir()

	small := filepath.Join(dir, "a.png")
	if err := os.WriteFile(small, []byte("pixels"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readImageFile(small); err != nil || string(data) != "pixels" {
		t.Errorf("readImageFile(small) = %q, %v", data, err)
	}

	if _, err := readImageFile(dir); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Errorf("a directory: err = %v, want a not-a-regular-file error", err)
	}
	if _, err := readImageFile(filepath.Join(dir, "missing.png")); err == nil {
		t.Error("a missing file: no error")
	}

	// A sparse file is cheap to make and is refused on its size alone.
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, maxImageBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readImageFile(big); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("an oversized file: err = %v, want a too-large error", err)
	}
}
