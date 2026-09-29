package app

import (
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"atlas-notes/internal/storage"
)

const (
	// reminderPoll is how often the wait for the first background pass looks
	// at whether it has finished.
	reminderPoll = 500 // ms
	// reminderEvery is how often due tasks are looked for once the app is up.
	// Due dates move by the day, and a reminder is not an alarm.
	reminderEvery = 30 * 60 // seconds
	// reminderBodyTasks is how many tasks a notification names before it stops
	// listing them and says how many more there are.
	reminderBodyTasks = 3
)

// reminderKey is what makes a task the same task for the purpose of announcing
// it once. Editing its text or moving its date makes it a new one, which is
// what someone who just changed a due date would want told about.
type reminderKey struct {
	path string
	line int
	text string
	due  string
}

// announcer remembers which tasks have been announced today. It is in memory
// only: restarting the app is the one way to hear about a task twice in a day.
type announcer struct {
	day  string
	seen map[reminderKey]bool
}

// fresh returns the tasks not announced yet on the day today (yyyy-mm-dd) and
// marks them announced. A new day forgets everything, so a task still undone
// is mentioned again tomorrow.
func (an *announcer) fresh(today string, tasks []storage.DueTask) []storage.DueTask {
	if an.day != today || an.seen == nil {
		an.day, an.seen = today, make(map[reminderKey]bool, len(tasks))
	}
	var out []storage.DueTask
	for _, t := range tasks {
		k := reminderKey{t.Path, t.Line, t.Text, t.Due}
		if an.seen[k] {
			continue
		}
		an.seen[k] = true
		out = append(out, t)
	}
	return out
}

// reminderMessage words the notification for tasks: a headline that says how
// many, and a body naming the first few with the note each is in. today is
// yyyy-mm-dd; it tells a single task due today from a single overdue one.
func reminderMessage(today string, tasks []storage.DueTask) (title, body string) {
	switch {
	case len(tasks) == 1 && tasks[0].Due == today:
		title = "1 task due today"
	case len(tasks) == 1:
		title = "1 task overdue"
	default:
		title = fmt.Sprintf("%d tasks due", len(tasks))
	}
	var lines []string
	for i, t := range tasks {
		if i == reminderBodyTasks {
			lines = append(lines, fmt.Sprintf("and %d more", len(tasks)-i))
			break
		}
		lines = append(lines, truncate(t.Text, 80)+" · "+path.Base(t.Path))
	}
	return title, strings.Join(lines, "\n")
}

// remindersOn stops a second call from starting a second set of timers.
var remindersOn bool

// startReminders sends a desktop notification for checklist items that are due
// or overdue. It waits until the first background pass has read the vault, so
// what it announces is what is really there, then looks at once and every half
// hour after, while the setting is on. It does nothing in a benchmark or a
// screenshot run, which have no business notifying whoever ran them.
func (a *App) startReminders() {
	if a.store == nil || devRun() || remindersOn {
		return
	}
	remindersOn = true
	an := &announcer{}
	coreglib.TimeoutAdd(reminderPoll, func() bool {
		if a.closing {
			return false
		}
		if !a.backgroundDone {
			return true
		}
		a.checkReminders(an)
		coreglib.TimeoutSecondsAdd(reminderEvery, func() bool {
			a.checkReminders(an)
			return !a.closing
		})
		return false
	})
}

// checkReminders looks for due tasks off the main thread and, back on it,
// announces the ones not announced yet.
func (a *App) checkReminders(an *announcer) {
	if a.closing || !a.cfg.DueReminders {
		return
	}
	today := time.Now().Format(dateLayout)
	go func() {
		tasks, err := a.store.DueTasks(today)
		coreglib.IdleAdd(func() bool {
			// The setting is read again: it may have been switched off while
			// the query ran.
			if a.closing || !a.cfg.DueReminders {
				return false
			}
			if err != nil {
				log.Printf("atlas-notes: due reminders: %v", err)
				return false
			}
			fresh := an.fresh(today, tasks)
			if len(fresh) == 0 {
				return false
			}
			title, body := reminderMessage(today, fresh)
			n := gio.NewNotification(title)
			n.SetBody(body)
			// No default action: clicking it brings the window forward, as
			// activating the app does. Sending it home would leave the note
			// being typed in.
			a.adw.SendNotification("due-tasks", n)
			return false
		})
	}()
}
