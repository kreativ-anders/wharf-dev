package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/proctree"
)

// Spec describes one managed process.
type Spec struct {
	// ID is the supervisor's key: "webserver", "php:8.3",
	// "project:my-kirby-site". Starting an ID that is already running is a
	// no-op, so IDs must be stable across calls.
	ID string
	// Label is what the GUI shows, e.g. "nginx".
	Label string
	Path  string
	Args  []string
	Dir   string
	Env   []string
	// Port is the TCP port the process listens on; zero if it listens on
	// none. Used to wait for release before a replacement starts and to
	// confirm a start actually succeeded.
	Port int
	// LogPath receives combined stdout/stderr. Empty discards output.
	LogPath string
	// Logs are the other logs the process writes itself — a webserver's
	// access and error logs. Like LogPath, each is moved aside on start once
	// it has grown past MaxLogSize.
	Logs []string
	// StopSignal is sent first on stop; SIGTERM if unset. Windows cannot
	// deliver it, so there the process tree is killed at once.
	StopSignal os.Signal
	// StopGrace is how long to wait after StopSignal before killing.
	StopGrace time.Duration
}

// Handle is a started process.
type Handle interface {
	// Wait blocks until the process exits and returns its exit error, if any.
	Wait() error
	// Signal asks the process to terminate gracefully.
	Signal(os.Signal) error
	// Kill terminates the process immediately.
	Kill() error
	PID() int
}

// Runner starts processes. The daemon uses ExecRunner; tests substitute a fake
// so that service orchestration is exercised without shipping real binaries.
type Runner interface {
	Start(Spec) (Handle, error)
}

// ExecRunner starts real OS processes, each as the root of a process tree so
// that stopping it reaches the workers it starts. How a tree is held together
// is the one thing that differs per OS, and it lives in proctree
// (dev/architecture.md §4).
type ExecRunner struct{}

func (ExecRunner) Start(s Spec) (Handle, error) {
	if s.Path == "" {
		return nil, errors.New("supervisor: spec has no executable path")
	}
	cmd := exec.Command(s.Path, s.Args...)
	cmd.Dir = s.Dir
	if len(s.Env) > 0 {
		cmd.Env = append(os.Environ(), s.Env...)
	}

	var logFile *os.File
	if s.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open log %s: %w", s.LogPath, err)
		}
		logFile = f
		cmd.Stdout, cmd.Stderr = f, f
	}

	tree, err := proctree.Start(cmd)
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("start %s: %w", s.Label, err)
	}
	h := &execHandle{cmd: cmd, tree: tree, done: make(chan struct{})}
	// WARNING: Reap in the background so Wait is safe to call from several goroutines
	// and from none at all: os/exec requires exactly one Wait per process.
	go func() {
		h.err = cmd.Wait()
		tree.Release()
		if logFile != nil {
			logFile.Close()
		}
		close(h.done)
	}()
	return h, nil
}

type execHandle struct {
	cmd  *exec.Cmd
	tree *proctree.Tree
	done chan struct{}
	err  error
}

func (h *execHandle) Wait() error {
	<-h.done
	return h.err
}

func (h *execHandle) Signal(sig os.Signal) error {
	if h.cmd.Process == nil {
		return errors.New("supervisor: process not started")
	}
	err := h.cmd.Process.Signal(sig)
	// INFO: A process that already exited is the outcome the caller wanted.
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Kill terminates the process and every worker it started: killing only a
// master leaves its workers holding the port.
func (h *execHandle) Kill() error { return h.tree.Kill() }

func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}
