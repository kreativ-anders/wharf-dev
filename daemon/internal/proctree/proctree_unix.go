//go:build unix

package proctree

import (
	"errors"
	"os/exec"
	"syscall"
)

// Tree is a started process and everything it starts.
type Tree struct {
	pgid int
}

// Start starts cmd as the leader of a new process group. Its workers inherit
// the group, which is what lets Kill reach them.
func Start(cmd *exec.Cmd) (*Tree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{pgid: cmd.Process.Pid}, nil
}

// Kill terminates every process in the tree at once.
func (t *Tree) Kill() error {
	err := syscall.Kill(-t.pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Release is called once the root process has exited. Whatever it left behind
// is killed: a master that crashed must not leave workers holding its port.
func (t *Tree) Release() { _ = t.Kill() }
