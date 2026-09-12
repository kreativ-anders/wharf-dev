package proctree

import (
	"syscall"
	"testing"
)

func alive(pid int) bool {
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	ev, _ := syscall.WaitForSingleObject(h, 0)
	return ev == syscall.WAIT_TIMEOUT
}

// Windows alone can promise this: the job dies with the last handle to it, and
// the daemon holds the only one. A daemon killed outright — no shutdown, no
// deferred cleanup — still takes its services with it. macOS and Linux have
// no equivalent (dev/architecture.md §4, "Deliberately not unified").
func TestTheTreeDiesWithTheDaemonThatStartedIt(t *testing.T) {
	owner := helper("owner")
	out, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	worker, err := readPID(out)
	if err != nil {
		owner.Process.Kill()
		owner.Wait()
		t.Fatal(err)
	}
	if !alive(worker) {
		t.Fatal("the owner's service never started")
	}
	owner.Process.Kill()
	owner.Wait()
	waitGone(t, worker)
}
