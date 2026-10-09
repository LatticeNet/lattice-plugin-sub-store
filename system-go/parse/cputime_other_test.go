//go:build !unix

package parse

import "time"

// cpuTime falls back to wall time where getrusage does not exist.
func cpuTime() time.Duration { return time.Duration(time.Now().UnixNano()) }
