// Package supervisor manages the lifetime of the native processes the tool
// runs — webservers and per-project PHP servers. There is no container
// runtime underneath: these are ordinary host processes started with os/exec.
package supervisor

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"syscall"
	"time"
)

// State is where a managed process is in its lifecycle. The transitional
// states exist so the GUI can show "switching…" rather than flickering
// between stopped and running (service-management.feature).
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

// Status is a snapshot of one managed process.
type Status struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State State  `json:"state"`
	PID   int    `json:"pid,omitempty"`
	Port  int    `json:"port,omitempty"`
	Error string `json:"error,omitempty"`
}

type managed struct {
	spec   Spec
	handle Handle
	state  State
	err    error
	// gen guards against a watcher for an old process run marking a newer
	// run as exited.
	gen uint64
}

// Supervisor owns every managed process. One instance per daemon.
type Supervisor struct {
	runner Runner
	prober Prober

	// StartTimeout bounds waiting for a started process to bind its port.
	StartTimeout time.Duration
	// StopTimeout bounds waiting for a stopped process to exit and release
	// its port before it is killed.
	StopTimeout time.Duration
	// Poll is the interval between port probes.
	Poll time.Duration

	mu    sync.Mutex
	procs map[string]*managed
	gen   uint64

	listenersMu sync.RWMutex
	listeners   map[int]func(Status)
	nextID      int
}

// New returns a Supervisor using the given runner and port prober.
func New(r Runner, p Prober) *Supervisor {
	return &Supervisor{
		runner:       r,
		prober:       p,
		StartTimeout: 15 * time.Second,
		StopTimeout:  10 * time.Second,
		Poll:         50 * time.Millisecond,
		procs:        map[string]*managed{},
		listeners:    map[int]func(Status){},
	}
}

// NewSystem returns a Supervisor that starts real processes.
func NewSystem() *Supervisor { return New(ExecRunner{}, NetProber{}) }

// OnChange registers a callback invoked on every state transition. The
// returned function unregisters it. Callbacks must not block; the IPC layer
// fans them out to connected clients over buffered channels.
func (s *Supervisor) OnChange(fn func(Status)) func() {
	s.listenersMu.Lock()
	id := s.nextID
	s.nextID++
	s.listeners[id] = fn
	s.listenersMu.Unlock()
	return func() {
		s.listenersMu.Lock()
		delete(s.listeners, id)
		s.listenersMu.Unlock()
	}
}

// Start launches the spec's process unless it is already running. It returns
// only once the process is confirmed running: a spec with a port must be
// listening on it, which is what distinguishes "spawned" from "serving".
func (s *Supervisor) Start(ctx context.Context, spec Spec) error {
	s.mu.Lock()
	if m, ok := s.procs[spec.ID]; ok {
		switch m.state {
		case StateRunning, StateStarting:
			s.mu.Unlock()
			return nil
		}
	}
	s.gen++
	gen := s.gen
	m := &managed{spec: spec, state: StateStarting, gen: gen}
	s.procs[spec.ID] = m
	status := statusOf(m)
	s.mu.Unlock()
	s.emit(status)

	// INFO: The previous occupant of this port may not have released it yet.
	waitCtx, cancel := context.WithTimeout(ctx, s.StopTimeout)
	err := WaitPortFree(waitCtx, s.prober, spec.Port, s.Poll)
	cancel()
	if err != nil {
		return s.fail(spec.ID, gen, err)
	}

	// INFO: Only what this run writes explains a failed start; earlier runs' lines
	// in the same log would name problems long since fixed.
	logFrom := logSize(spec.LogPath)
	h, err := s.runner.Start(spec)
	if err != nil {
		return s.fail(spec.ID, gen, err)
	}

	s.mu.Lock()
	if s.procs[spec.ID] != m || m.gen != gen {
		// INFO: A concurrent stop replaced this entry while we were starting.
		s.mu.Unlock()
		terminate(ctx, h, spec)
		return nil
	}
	m.handle = h
	s.mu.Unlock()

	go s.watch(spec.ID, gen, h)

	// INFO: A process that exits before it binds — a config it refuses, most often
	// — ends the wait at once rather than after StartTimeout, and its own last
	// words become the error (app-configuration.feature, "A custom config the
	// webserver refuses names the problem").
	bindCtx, cancelBind := context.WithTimeout(ctx, s.StartTimeout)
	exited := make(chan struct{})
	go func() {
		h.Wait()
		close(exited)
		cancelBind()
	}()
	err = WaitPortBound(bindCtx, s.prober, spec.Port, s.Poll)
	cancelBind()
	if err != nil {
		select {
		case <-exited:
			err = fmt.Errorf("%s exited during startup", spec.Label)
		default:
			terminate(context.WithoutCancel(ctx), h, spec)
		}
		if out := startupOutput(spec.LogPath, logFrom); out != "" {
			return s.fail(spec.ID, gen, fmt.Errorf("%s did not start: %s", spec.Label, out))
		}
		return s.fail(spec.ID, gen, fmt.Errorf("%s did not start: %w", spec.Label, err))
	}

	s.mu.Lock()
	if s.procs[spec.ID] != m || m.gen != gen {
		s.mu.Unlock()
		return nil
	}
	// WARNING: A process that already exited during the bind wait must not be
	// reported as running.
	if m.state == StateFailed || m.state == StateStopped {
		err := m.err
		s.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("%s exited during startup", spec.Label)
		}
		return err
	}
	m.state = StateRunning
	m.err = nil
	status = statusOf(m)
	s.mu.Unlock()
	s.emit(status)
	return nil
}

// Stop terminates a managed process and waits for its port to be released, so
// that a caller may start a replacement immediately afterwards. Stopping
// something that is not running is a no-op.
func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	m, ok := s.procs[id]
	if !ok || m.handle == nil || m.state == StateStopped || m.state == StateStopping {
		s.mu.Unlock()
		return nil
	}
	m.state = StateStopping
	h := m.handle
	spec := m.spec
	gen := m.gen
	status := statusOf(m)
	s.mu.Unlock()
	s.emit(status)

	terminate(ctx, h, spec)

	// INFO: os/exec has reaped the process, but the kernel may hold its listening
	// socket a moment longer.
	relCtx, cancel := context.WithTimeout(ctx, s.StopTimeout)
	relErr := WaitPortFree(relCtx, s.prober, spec.Port, s.Poll)
	cancel()

	s.mu.Lock()
	if cur, ok := s.procs[id]; ok && cur == m && cur.gen == gen {
		m.state = StateStopped
		m.handle = nil
		m.err = nil
		status = statusOf(m)
		s.mu.Unlock()
		s.emit(status)
	} else {
		s.mu.Unlock()
	}
	return relErr
}

// terminate asks a process to exit and kills it only if it will not. Asking
// first lets a webserver finish what it is serving; the kill reaches the whole
// process tree, so even the fallback leaves no worker holding the port
// (service-management.feature, "Stopping a webserver stops its worker
// processes too").
func terminate(ctx context.Context, h Handle, spec Spec) {
	sig := spec.StopSignal
	if sig == nil {
		sig = syscall.SIGTERM
	}
	grace := spec.StopGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}

	exited := make(chan struct{})
	go func() { h.Wait(); close(exited) }()

	// INFO: Windows cannot deliver a signal at all; waiting out the grace period
	// there would only delay a kill that is coming anyway.
	if err := h.Signal(sig); err != nil {
		_ = h.Kill()
		<-exited
		return
	}

	select {
	case <-exited:
	case <-time.After(grace):
		_ = h.Kill()
		<-exited
	case <-ctx.Done():
		_ = h.Kill()
		<-exited
	}
}

// Restart stops whatever runs under spec.ID and starts spec in its place.
// This is the single mechanism behind every service swap, so the webserver
// toggle and any later database toggle behave identically
// (roadmap-services.feature relies on this).
func (s *Supervisor) Restart(ctx context.Context, spec Spec) error {
	if err := s.Stop(ctx, spec.ID); err != nil {
		return err
	}
	return s.Start(ctx, spec)
}

// StopAll stops every managed process (tray-actions.feature, "Stop all").
func (s *Supervisor) StopAll(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Strings(ids)

	var firstErr error
	for _, id := range ids {
		if err := s.Stop(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Status returns one process's status.
func (s *Supervisor) Status(id string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.procs[id]
	if !ok {
		return Status{}, false
	}
	return statusOf(m), true
}

// Statuses returns every known process, ordered by ID for a stable GUI list.
func (s *Supervisor) Statuses() []Status {
	s.mu.Lock()
	out := make([]Status, 0, len(s.procs))
	for _, m := range s.procs {
		out = append(out, statusOf(m))
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PortFree reports whether nothing listens on a port, judged by the prober the
// supervisor itself waits on, so a test sees the ports it faked.
func (s *Supervisor) PortFree(port int) bool { return s.prober.Free(port) }

// Running reports whether the given ID is currently running.
func (s *Supervisor) Running(id string) bool {
	st, ok := s.Status(id)
	return ok && st.State == StateRunning
}

// AnyRunning reports whether any managed process is running or transitioning,
// which is what the tray icon's idle-vs-active state reflects.
func (s *Supervisor) AnyRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.procs {
		switch m.state {
		case StateRunning, StateStarting, StateStopping:
			return true
		}
	}
	return false
}

// watch marks a process failed if it exits without having been asked to.
func (s *Supervisor) watch(id string, gen uint64, h Handle) {
	waitErr := h.Wait()

	s.mu.Lock()
	m, ok := s.procs[id]
	if !ok || m.gen != gen {
		s.mu.Unlock()
		return
	}
	if m.state == StateStopping || m.state == StateStopped {
		// INFO: Expected exit; Stop owns the transition.
		s.mu.Unlock()
		return
	}
	// WARNING: A start that already failed keeps its own error. Both run when a
	// service exits during startup, and this one knows only that it exited:
	// letting it win replaced nginx's own words with "nginx exited
	// unexpectedly", whichever way the race fell (app-configuration.feature,
	// "A custom config the webserver refuses names the problem").
	if m.state == StateFailed && m.err != nil {
		m.handle = nil
		s.mu.Unlock()
		return
	}
	m.state = StateFailed
	m.handle = nil
	if waitErr != nil {
		m.err = fmt.Errorf("%s exited: %w", m.spec.Label, waitErr)
	} else {
		m.err = fmt.Errorf("%s exited unexpectedly", m.spec.Label)
	}
	status := statusOf(m)
	s.mu.Unlock()
	s.emit(status)
}

func (s *Supervisor) fail(id string, gen uint64, cause error) error {
	s.mu.Lock()
	m, ok := s.procs[id]
	if !ok || m.gen != gen {
		s.mu.Unlock()
		return cause
	}
	m.state = StateFailed
	m.err = cause
	m.handle = nil
	status := statusOf(m)
	s.mu.Unlock()
	s.emit(status)
	return cause
}

func (s *Supervisor) emit(st Status) {
	s.listenersMu.RLock()
	defer s.listenersMu.RUnlock()
	for _, fn := range s.listeners {
		fn(st)
	}
}

func statusOf(m *managed) Status {
	st := Status{ID: m.spec.ID, Label: m.spec.Label, State: m.state, Port: m.spec.Port}
	if m.handle != nil {
		st.PID = m.handle.PID()
	}
	if m.err != nil {
		st.Error = m.err.Error()
	}
	return st
}
