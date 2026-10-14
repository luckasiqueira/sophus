//go:build windows

package monitor

import (
	"syscall"
	"time"
)

func processCPUTime() (time.Duration, bool) {
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	// Filetime counts 100 ns intervals.
	ticks := int64(kernel.HighDateTime)<<32 | int64(kernel.LowDateTime)
	ticks += int64(user.HighDateTime)<<32 | int64(user.LowDateTime)
	return time.Duration(ticks * 100), true
}
