package elevate

import (
	"os"
	"sync"
)

// Fake is an in-memory Elevator for tests and for the daemon's --dry-run mode.
// It records every write instead of touching the real filesystem.
type Fake struct {
	mu      sync.Mutex
	Decline bool
	Err     error
	Writes  map[string]string
	// Apply also performs the write, so a test pointing at a temp file sees
	// the same read-modify-write behaviour the real adapters produce.
	Apply bool
	// Runs records every privileged program run, as program then args.
	Runs [][]string
	// OnRun is called for an approved run, so a test can play the part of
	// the program.
	OnRun func(program string, args, env []string) error
}

// NewFake returns a Fake that approves every request.
func NewFake() *Fake { return &Fake{Writes: map[string]string{}} }

func (f *Fake) RequestElevatedWrite(path string, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Decline {
		return ErrDeclined
	}
	if f.Err != nil {
		return f.Err
	}
	if f.Writes == nil {
		f.Writes = map[string]string{}
	}
	f.Writes[path] = content
	if f.Apply {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	return nil
}

// Content returns what was last written to path.
func (f *Fake) Content(path string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Writes[path]
	return c, ok
}

func (f *Fake) RequestElevatedRun(program string, args []string, env []string) error {
	f.mu.Lock()
	if f.Decline {
		f.mu.Unlock()
		return ErrDeclined
	}
	if f.Err != nil {
		f.mu.Unlock()
		return f.Err
	}
	f.Runs = append(f.Runs, append([]string{program}, args...))
	onRun := f.OnRun
	f.mu.Unlock()
	if onRun != nil {
		return onRun(program, args, env)
	}
	return nil
}

// RunCount reports how many privileged runs were approved.
func (f *Fake) RunCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Runs)
}
