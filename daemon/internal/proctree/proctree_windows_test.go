package proctree

import (
	"os/exec"
	"syscall"
	"testing"
)

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
	if !Alive(worker) {
		t.Fatal("the owner's service never started")
	}
	owner.Process.Kill()
	owner.Wait()
	waitGone(t, worker)
}

// features/single-application.feature — "The application exits without
// shutting its daemon down"
//
// The app's launcher — flutter run, a debugger — keeps a handle to it after it
// exits, and an exited process stays openable while one is held. A watchdog
// that only asked "can it be opened?" kept the daemon waiting for a parent
// that was long gone.
func TestAnExitedProcessIsGoneWhileAHandleToItIsHeld(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	cmd.Wait()

	if Alive(pid) {
		t.Fatal("an exited process counts as alive while a handle to it is open")
	}
}
