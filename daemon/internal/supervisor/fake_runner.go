package supervisor

import (
	"os"
	"sync"
)

// FakeRunner stands in for real service binaries in tests: it records what was
// started and lets a test decide when each process "exits" and whether its
// port appears bound.
type FakeRunner struct {
	mu      sync.Mutex
	Started []Spec
	// StartErr, if set for a spec ID, makes Start fail for it.
	StartErr map[string]error
	handles  map[string]*FakeHandle
	Ports    *FakePorts
	nextPID  int
}

// NewFakeRunner returns a runner sharing a port map with a FakePorts prober,
// so that a started process is seen as bound and a stopped one as released.
func NewFakeRunner(ports *FakePorts) *FakeRunner {
	return &FakeRunner{StartErr: map[string]error{}, handles: map[string]*FakeHandle{}, Ports: ports, nextPID: 1000}
}

func (r *FakeRunner) Start(s Spec) (Handle, error) {
	r.mu.Lock()
	if err := r.StartErr[s.ID]; err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.nextPID++
	h := &FakeHandle{pid: r.nextPID, done: make(chan struct{}), ports: r.Ports, port: s.Port}
	r.Started = append(r.Started, s)
	r.handles[s.ID] = h
	r.mu.Unlock()

	if r.Ports != nil && s.Port > 0 {
		r.Ports.Bind(s.Port)
	}
	return h, nil
}

// Handle returns the most recent handle started for an ID.
func (r *FakeRunner) Handle(id string) *FakeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[id]
}

// StartedIDs lists started spec IDs in order.
func (r *FakeRunner) StartedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.Started))
	for i, s := range r.Started {
		out[i] = s.ID
	}
	return out
}

// FakeHandle is a process that exits when told to.
type FakeHandle struct {
	pid     int
	done    chan struct{}
	once    sync.Once
	ports   *FakePorts
	port    int
	Signals []os.Signal
	Killed  bool
	// HoldPort keeps the port bound after exit, reproducing a webserver that
	// has not yet released its listening socket.
	HoldPort bool
	mu       sync.Mutex
}

func (h *FakeHandle) Wait() error { <-h.done; return nil }

func (h *FakeHandle) Signal(sig os.Signal) error {
	h.mu.Lock()
	h.Signals = append(h.Signals, sig)
	hold := h.HoldPort
	h.mu.Unlock()
	h.exit(hold)
	return nil
}

func (h *FakeHandle) Kill() error {
	h.mu.Lock()
	h.Killed = true
	h.mu.Unlock()
	h.exit(false)
	return nil
}

func (h *FakeHandle) PID() int { return h.pid }

// Exit makes the process terminate on its own, as a crashed service would.
func (h *FakeHandle) Exit() { h.exit(false) }

func (h *FakeHandle) exit(holdPort bool) {
	h.once.Do(func() {
		if h.ports != nil && h.port > 0 && !holdPort {
			h.ports.Release(h.port)
		}
		close(h.done)
	})
}

// ReleasePort frees a port a FakeHandle held past its exit.
func (h *FakeHandle) ReleasePort() {
	if h.ports != nil && h.port > 0 {
		h.ports.Release(h.port)
	}
}

// FakePorts is an in-memory Prober.
type FakePorts struct {
	mu    sync.Mutex
	bound map[int]bool
}

func NewFakePorts() *FakePorts { return &FakePorts{bound: map[int]bool{}} }

func (p *FakePorts) Free(port int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.bound[port]
}

func (p *FakePorts) Bind(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bound[port] = true
}

func (p *FakePorts) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bound, port)
}
