// Package watchdog lets the daemon stop itself when the application that
// started it is gone.
//
// The GUI starts the daemon as a child process and stops it on quit, so this
// only matters when the GUI never gets to run that code: a crash, a force
// quit, a kill -9. A webserver left holding port 80 with no way to reach it is
// the worst thing this tool could leave behind
// (features/single-application.feature).
package watchdog

import (
	"context"
	"os"
	"runtime"
	"syscall"
	"time"
)

// DefaultPoll is how often the parent is checked. The cost is one syscall, and
// a few seconds of overlap after a crash is harmless.
const DefaultPoll = 2 * time.Second

// WatchParent calls onGone once the given process no longer exists, then
// returns. It also returns if ctx is cancelled, without calling onGone.
//
// A pid of zero or the daemon's own pid disables the watch: being told to
// watch nothing must not shut the daemon down.
func WatchParent(ctx context.Context, pid int, poll time.Duration, onGone func()) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	if poll <= 0 {
		poll = DefaultPoll
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !Alive(pid) {
				onGone()
				return
			}
		}
	}
}

// Alive reports whether a process exists. On Unix that is signal 0, the
// standard existence check; on Windows, FindProcess itself fails for a process
// that is gone.
func Alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
