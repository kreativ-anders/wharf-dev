package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
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
	// StopSignal is sent first on stop; SIGTERM if unset. Ignored on Windows,
	// where the process is killed directly.
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

// ExecRunner starts real OS processes. os/exec behaves identically on all
// three operating systems, so there is no platform branch here
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

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("start %s: %w", s.Label, err)
	}
	h := &execHandle{cmd: cmd, done: make(chan struct{})}
	// Reap in the background so Wait is safe to call from several goroutines
	// and from none at all: os/exec requires exactly one Wait per process.
	go func() {
		h.err = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		close(h.done)
	}()
	return h, nil
}

type execHandle struct {
	cmd  *exec.Cmd
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
	// A process that already exited is the outcome the caller wanted.
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (h *execHandle) Kill() error {
	if h.cmd.Process == nil {
		return errors.New("supervisor: process not started")
	}
	err := h.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}
