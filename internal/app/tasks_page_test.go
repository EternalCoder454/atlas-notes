package app

import (
	"testing"

	"atlas-notes/internal/storage"
)

func TestTaskGroup(t *testing.T) {
	const today, weekEnd = "2026-07-10", "2026-07-17"
	for due, want := range map[string]int{
		"":           taskSomeday,
		"2026-07-09": taskOverdue,
		"2026-07-10": taskToday,
		"2026-07-11": taskWeek,
		"2026-07-17": taskWeek,
		"2026-07-18": taskLater,
	} {
		if got := taskGroup(due, today, weekEnd); got != want {
			t.Errorf("taskGroup(%q) = %d, want %d", due, got, want)
		}
	}
	g := groupTasks([]storage.DueTask{{Text: "a", Due: "2026-07-01"}, {Text: "b"}, {Text: "c", Due: "2026-07-02"}}, today, weekEnd)
	if len(g[taskOverdue]) != 2 || g[taskOverdue][1].Text != "c" || len(g[taskSomeday]) != 1 {
		t.Errorf("groups = %+v", g)
	}
}
