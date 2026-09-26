//go:build !windows

package app

import (
	"os"
	"syscall"
)

// Unix halves of the two things the application asks of the operating system
// directly: what it has spent, and how to become the new version of itself.

// processCPU is how much processor time this process has used, in
// milliseconds, split between user and system. Used only by the measurement
// harness.
func processCPU() (userMs, sysMs float64) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0, 0
	}
	return float64(ru.Utime.Sec)*1000 + float64(ru.Utime.Usec)/1000,
		float64(ru.Stime.Sec)*1000 + float64(ru.Stime.Usec)/1000
}

// restartInto replaces this process with the freshly installed binary. On
// success it never returns: the same process id carries on as the new version,
// so the window manager and the launcher see one continuous application.
func restartInto(exe string) error {
	return syscall.Exec(exe, []string{exe}, os.Environ())
}
