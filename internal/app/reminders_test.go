package app

import (
	"fmt"
	"strings"
	"testing"

	"atlas-notes/internal/storage"
)

func task(path string, line int, text, due string) storage.DueTask {
	return storage.DueTask{Path: path, Line: line, Text: text, Due: due}
}

// TestAnnouncerSaysEachTaskOncePerDay: the check runs every half hour, and a
// notification that repeats itself that often is one people switch off.
func TestAnnouncerSaysEachTaskOncePerDay(t *testing.T) {
	an := &announcer{}
	a := task("Trip", 3, "Renew insurance", "2026-09-29")
	b := task("Work/Todo", 1, "Send invoice", "2026-09-27")

	if got := an.fresh("2026-09-29", []storage.DueTask{a}); len(got) != 1 {
		t.Fatalf("first sighting: got %d tasks, want 1", len(got))
	}
	if got := an.fresh("2026-09-29", []storage.DueTask{a}); len(got) != 0 {
		t.Errorf("the same task again: got %d tasks, want none", len(got))
	}
	// A task that turns up later is announced on its own, not with the old one.
	got := an.fresh("2026-09-29", []storage.DueTask{a, b})
	if len(got) != 1 || got[0].Text != "Send invoice" {
		t.Errorf("new task among old: got %v, want only the invoice", got)
	}
	// The next day everything still undone is news again.
	if got := an.fresh("2026-09-30", []storage.DueTask{a, b}); len(got) != 2 {
		t.Errorf("a new day: got %d tasks, want 2", len(got))
	}
}

// TestAnnouncerKeyCoversWhatMakesATask: the same words on another line, in
// another note or with another date are another task.
func TestAnnouncerKeyCoversWhatMakesATask(t *testing.T) {
	an := &announcer{}
	base := task("Trip", 3, "Pack", "2026-09-29")
	an.fresh("2026-09-29", []storage.DueTask{base})
	for name, changed := range map[string]storage.DueTask{
		"line": task("Trip", 4, "Pack", "2026-09-29"),
		"note": task("Home", 3, "Pack", "2026-09-29"),
		"text": task("Trip", 3, "Pack bags", "2026-09-29"),
		"date": task("Trip", 3, "Pack", "2026-09-28"),
	} {
		if got := an.fresh("2026-09-29", []storage.DueTask{changed}); len(got) != 1 {
			t.Errorf("changed %s was taken for the task already announced", name)
		}
	}
	// Priority is not part of the key: raising it is not a new task.
	same := base
	same.Priority = "high"
	if got := an.fresh("2026-09-29", []storage.DueTask{same}); len(got) != 0 {
		t.Errorf("a priority change re-announced the task")
	}
}

func TestReminderMessageTitle(t *testing.T) {
	today := "2026-09-29"
	tests := []struct {
		name  string
		tasks []storage.DueTask
		want  string
	}{
		{"one today", []storage.DueTask{task("A", 0, "x", today)}, "1 task due today"},
		{"one overdue", []storage.DueTask{task("A", 0, "x", "2026-09-20")}, "1 task overdue"},
		{"several", []storage.DueTask{task("A", 0, "x", today), task("B", 0, "y", "2026-09-20")}, "2 tasks due"},
	}
	for _, tc := range tests {
		if got, _ := reminderMessage(today, tc.tasks); got != tc.want {
			t.Errorf("%s: title %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestReminderMessageBody(t *testing.T) {
	today := "2026-09-29"
	tasks := []storage.DueTask{
		task("Trip", 0, "Renew insurance", today),
		task("Work/Todo", 1, "Send invoice", today),
		task("Home", 2, "Water plants", today),
	}
	_, body := reminderMessage(today, tasks)
	want := "Renew insurance · Trip\nSend invoice · Todo\nWater plants · Home"
	if body != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}

	// Past three, the rest are counted rather than listed.
	tasks = append(tasks, task("A", 0, "Fourth", today), task("B", 0, "Fifth", today))
	title, body := reminderMessage(today, tasks)
	if title != "5 tasks due" {
		t.Errorf("title %q, want %q", title, "5 tasks due")
	}
	lines := strings.Split(body, "\n")
	if len(lines) != 4 || lines[3] != "and 2 more" {
		t.Errorf("body lists %d lines, last %q; want three tasks and \"and 2 more\"", len(lines), lines[len(lines)-1])
	}
	if strings.Contains(body, "Fourth") {
		t.Errorf("body names a fourth task: %q", body)
	}
}

// TestReminderMessageKeepsTextShort: a task is a line the user typed, and a
// notification is not the place for a paragraph of it.
func TestReminderMessageKeepsTextShort(t *testing.T) {
	long := strings.Repeat("word ", 60)
	_, body := reminderMessage("2026-09-29", []storage.DueTask{task("N", 0, long, "2026-09-29")})
	if len(body) > 120 {
		t.Errorf("body is %d bytes for one task", len(body))
	}
}

func TestDueGroup(t *testing.T) {
	today := "2026-09-29"
	for due, want := range map[string]int{
		"2026-09-01": dueOverdue,
		"2026-09-28": dueOverdue,
		"2026-09-29": dueToday,
		"2026-09-30": dueSoon,
		"2026-10-06": dueSoon,
		// Ordering is by the string, so a new month or year must still sort.
		"2025-12-31": dueOverdue,
		"2027-01-01": dueSoon,
	} {
		if got := dueGroup(due, today); got != want {
			t.Errorf("dueGroup(%s) = %d, want %d", due, got, want)
		}
	}
}

func TestPlanDueLimit(t *testing.T) {
	var tasks []storage.DueTask
	for i := 0; i < 11; i++ {
		tasks = append(tasks, task("N", i, fmt.Sprint("t", i), "2026-09-29"))
	}
	shown, more := planDue(tasks, dueSlots)
	if len(shown) != dueSlots || more != 3 {
		t.Errorf("11 tasks: showing %d with %d more, want %d and 3", len(shown), more, dueSlots)
	}
	if shown[0].Text != "t0" || shown[dueSlots-1].Text != "t7" {
		t.Errorf("the tasks shown are not the first %d in order", dueSlots)
	}
	if shown, more := planDue(tasks[:dueSlots], dueSlots); len(shown) != dueSlots || more != 0 {
		t.Errorf("exactly %d tasks: showing %d with %d more", dueSlots, len(shown), more)
	}
	if shown, more := planDue(nil, dueSlots); len(shown) != 0 || more != 0 {
		t.Errorf("no tasks: showing %d with %d more", len(shown), more)
	}
}

func TestTopTags(t *testing.T) {
	// The index lists tags by name.
	tag := func(name string, n int) storage.TagCount { return storage.TagCount{Tag: name, Notes: n} }
	tags := []storage.TagCount{tag("alpha", 1), tag("beta", 3), tag("gamma", 3), tag("idea", 2), tag("zeta", 1)}
	got := topTags(tags, 4)
	want := []string{"beta", "gamma", "idea", "alpha"}
	if len(got) != len(want) {
		t.Fatalf("got %d tags, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Tag != w {
			t.Errorf("tag %d is %q, want %q (most notes first, ties by name)", i, got[i].Tag, w)
		}
	}
	// The index's own list is not reordered underneath its owner.
	if tags[0].Tag != "alpha" || tags[1].Tag != "beta" {
		t.Errorf("topTags reordered its input: %v", tags)
	}
	if got := topTags(nil, 24); len(got) != 0 {
		t.Errorf("no tags: got %v", got)
	}
}
