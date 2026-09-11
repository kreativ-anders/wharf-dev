package watchdog

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// features/single-application.feature — "The application exits without
// shutting its daemon down"
func TestTheDaemonNoticesWhenItsParentIsGone(t *testing.T) {
	// A short-lived process stands in for the GUI: it starts, then dies
	// without saying anything, as a crashed application would.
	parent := exec.Command("sleep", "0.2")
	if err := parent.Start(); err != nil {
		t.Skipf("cannot start a stand-in parent: %v", err)
	}
	pid := parent.Process.Pid
	go parent.Wait() // reap it, so the pid really disappears

	gone := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go WatchParent(ctx, pid, 20*time.Millisecond, func() { close(gone) })

	select {
	case <-gone:
	case <-ctx.Done():
		t.Fatal("the watchdog never noticed its parent exit")
	}
}

func TestALiveParentIsLeftAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	called := false
	// The test process's own parent is alive for the duration of the test.
	WatchParent(ctx, os.Getppid(), 10*time.Millisecond, func() { called = true })
	if called {
		t.Fatal("onGone fired for a parent that is still running")
	}
}

// Being told to watch nothing must never shut the daemon down.
func TestNoParentDisablesTheWatch(t *testing.T) {
	for _, pid := range []int{0, -1, os.Getpid()} {
		called := false
		done := make(chan struct{})
		go func() {
			WatchParent(context.Background(), pid, time.Millisecond, func() { called = true })
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("pid %d: WatchParent should return immediately", pid)
		}
		if called {
			t.Fatalf("pid %d: onGone fired", pid)
		}
	}
}

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("the test process reports itself as gone")
	}
}
