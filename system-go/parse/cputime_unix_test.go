//go:build unix

package parse

import (
	"syscall"
	"time"
)

// cpuTime is the CPU time this process has used, user and system, across all
// its threads. Unlike wall time it barely moves when other processes compete
// for the machine, which is what a scaling test on a shared runner needs.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return time.Duration(time.Now().UnixNano())
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
