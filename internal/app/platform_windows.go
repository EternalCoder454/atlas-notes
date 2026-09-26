//go:build windows

package app

import (
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// Windows halves of the two things the application asks of the operating
// system directly.

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	getProcessTime = kernel32.NewProc("GetProcessTimes")
)

// processCPU reports processor time in milliseconds. Windows counts it in
// 100-nanosecond units from process creation, which is the same quantity
// Getrusage reports elsewhere, in different units.
func processCPU() (userMs, sysMs float64) {
	var creation, exit, kernel, user syscall.Filetime
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, 0
	}
	r, _, _ := getProcessTime.Call(uintptr(h),
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0, 0
	}
	const perMs = 10000 // 100ns units in a millisecond
	toMs := func(f syscall.Filetime) float64 {
		return float64(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) / perMs
	}
	return toMs(user), toMs(kernel)
}

// restartInto starts the freshly installed binary and lets this process end.
//
// Windows has no exec that replaces a running process, and it will not let a
// running executable be overwritten either, so the installer writes the new
// binary beside the old one and this hands over to it. The pause gives this
// process time to close its files first: the new one opens the same vault and
// the same index.
func restartInto(exe string) error {
	cmd := exec.Command(exe)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// canSelfUpdate: the Windows build arrives as a packaged binary rather than as
// source, and Windows will not let a running executable be replaced, so the
// update is handed to the person instead of attempted.
const canSelfUpdate = false

// openDownloadPage opens the releases page in whatever the system uses for
// links. rundll32 is the way to do that without assuming a browser.
func openDownloadPage() error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", releasesURL).Start()
}
