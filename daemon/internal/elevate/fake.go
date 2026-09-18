package elevate

import "sync"

// Fake is an in-memory Elevator for tests. It records every privileged run
// instead of running it.
type Fake struct {
	mu      sync.Mutex
	Decline bool
	Err     error
	// Runs records every privileged program run, as program then args.
	Runs [][]string
	// OnRun is called for an approved run, so a test can play the part of
	// the program.
	OnRun func(program string, args, env []string) error
	// LowPortsBlocked plays a Linux that keeps ports below 1024 for
	// administrators; an approved AllowLowPorts clears it, as the setting it
	// writes would. LowPortsAsked counts the prompts that raised.
	LowPortsBlocked bool
	LowPortsAsked   int
}

// NewFake returns a Fake that approves every request.
func NewFake() *Fake { return &Fake{} }

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

// AllowLowPorts prompts only while LowPortsBlocked is set.
func (f *Fake) AllowLowPorts() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.LowPortsBlocked {
		return nil
	}
	f.LowPortsAsked++
	if f.Decline {
		return ErrDeclined
	}
	if f.Err != nil {
		return f.Err
	}
	f.LowPortsBlocked = false
	return nil
}

// LowPortsPrompts reports how often AllowLowPorts prompted.
func (f *Fake) LowPortsPrompts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.LowPortsAsked
}
