package app

import (
	"reflect"
	"testing"
)

func TestMovedPath(t *testing.T) {
	cases := []struct {
		rel, from, to, want string
		ok                  bool
	}{
		{"a.md", "a.md", "b.md", "b.md", true},
		{"dir/a.md", "dir", "other/dir", "other/dir/a.md", true},
		{"dir/sub/a.md", "dir", "x", "x/sub/a.md", true},
		{"dir2/a.md", "dir", "x", "dir2/a.md", false},
		{"a.md", "b.md", "c.md", "a.md", false},
		{"", "a.md", "b.md", "", false},
	}
	for _, c := range cases {
		got, ok := movedPath(c.rel, c.from, c.to)
		if got != c.want || ok != c.ok {
			t.Errorf("movedPath(%q, %q, %q) = %q, %v; want %q, %v", c.rel, c.from, c.to, got, ok, c.want, c.ok)
		}
	}
}

func TestPathAffected(t *testing.T) {
	if ok, f := pathAffected("dir/a.md", "dir"); !ok || !f {
		t.Error("a note in a deleted folder is affected, by a folder")
	}
	if ok, f := pathAffected("a.md", "a.md"); !ok || f {
		t.Error("the deleted note itself is affected, not as a folder")
	}
	if ok, _ := pathAffected("dir2/a.md", "dir"); ok {
		t.Error("a sibling with the same prefix is not affected")
	}
}

func TestClaudeConnectCommand(t *testing.T) {
	cases := []struct {
		exe     string
		flatpak bool
		want    string
	}{
		{"/usr/bin/atlas-notes", false, "claude mcp add --scope user atlas-notes -- /usr/bin/atlas-notes mcp"},
		{"/home/a b/atlas-notes", false, "claude mcp add --scope user atlas-notes -- '/home/a b/atlas-notes' mcp"},
		{"/home/o'x/atlas-notes", false, `claude mcp add --scope user atlas-notes -- '/home/o'\''x/atlas-notes' mcp`},
		{"/app/bin/atlas-notes", true, "claude mcp add --scope user atlas-notes -- flatpak run --command=atlas-notes io.github.atlasnotes mcp"},
	}
	for _, c := range cases {
		if got := claudeConnectCommand(c.exe, c.flatpak); got != c.want {
			t.Errorf("claudeConnectCommand(%q, %v) = %q; want %q", c.exe, c.flatpak, got, c.want)
		}
	}
}

func TestChangedRuns(t *testing.T) {
	cases := []struct {
		name, before, after string
		want                [][2]int
	}{
		{"same", "a\nb\n", "a\nb\n", nil},
		{"one line", "a\nb\nc\n", "a\nB\nc\n", [][2]int{{1, 1}}},
		{"appended", "a\nb\n", "a\nb\nc\nd\n", [][2]int{{2, 3}}},
		{"two places", "a\nb\nc\nd\ne\nf\n", "a\nB\nc\nd\nE\nf\n", [][2]int{{1, 1}, {4, 4}}},
		{"removed", "a\nb\nc\n", "a\nc\n", [][2]int{{1, 1}}},
		{"removed at end", "a\nb\nc", "a\nb", [][2]int{{1, 1}}},
		{"inserted between", "a\nc\n", "a\nb\nc\n", [][2]int{{1, 1}}},
		{"from empty", "", "# T\n", [][2]int{{0, 0}}},
	}
	for _, c := range cases {
		if got := changedRuns(c.before, c.after); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: changedRuns = %v, want %v", c.name, got, c.want)
		}
	}
}
