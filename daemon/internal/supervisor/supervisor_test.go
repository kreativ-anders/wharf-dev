package supervisor

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func newTestSupervisor() (*Supervisor, *FakeRunner, *FakePorts) {
	ports := NewFakePorts()
	runner := NewFakeRunner(ports)
	s := New(runner, ports)
	s.StartTimeout = 2 * time.Second
	s.StopTimeout = 2 * time.Second
	s.Poll = time.Millisecond
	return s, runner, ports
}

func spec(id, label string, port int) Spec {
	return Spec{ID: id, Label: label, Path: "/bin/" + label, Port: port, StopGrace: 50 * time.Millisecond}
}

func TestStartConfirmsTheProcessIsListening(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	if !s.Running("webserver") {
		t.Fatal("not running after Start")
	}
	if got := runner.StartedIDs(); len(got) != 1 {
		t.Fatalf("started %v, want one process", got)
	}
	st, _ := s.Status("webserver")
	if st.PID == 0 || st.Label != "nginx" || st.Port != 80 {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	sp := spec("webserver", "nginx", 80)
	for i := 0; i < 3; i++ {
		if err := s.Start(context.Background(), sp); err != nil {
			t.Fatal(err)
		}
	}
	if got := runner.StartedIDs(); len(got) != 1 {
		t.Fatalf("started %d processes, want 1", len(got))
	}
}

func TestStartFailsWhenTheProcessNeverBinds(t *testing.T) {
	ports := NewFakePorts()
	runner := NewFakeRunner(ports)
	runner.Ports = nil // started processes never bind their port
	s := New(runner, ports)
	s.StartTimeout = 100 * time.Millisecond
	s.Poll = time.Millisecond

	err := s.Start(context.Background(), spec("webserver", "nginx", 80))
	if err == nil {
		t.Fatal("a process that never listens should not be reported as running")
	}
	st, _ := s.Status("webserver")
	if st.State != StateFailed {
		t.Fatalf("state = %q, want failed", st.State)
	}
	if st.Error == "" {
		t.Fatal("failed state carries no explanation")
	}
}

func TestStopReleasesThePortBeforeReturning(t *testing.T) {
	s, runner, ports := newTestSupervisor()
	sp := spec("webserver", "nginx", 80)
	if err := s.Start(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if ports.Free(80) {
		t.Fatal("port should be bound while running")
	}
	if err := s.Stop(context.Background(), "webserver"); err != nil {
		t.Fatal(err)
	}
	if !ports.Free(80) {
		t.Fatal("Stop returned while the port was still bound")
	}
	if h := runner.Handle("webserver"); len(h.Signals) == 0 {
		t.Fatal("the process was never asked to stop gracefully")
	}
	st, _ := s.Status("webserver")
	if st.State != StateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
}

func TestStopKillsAProcessThatIgnoresTheSignal(t *testing.T) {
	ports := NewFakePorts()
	runner := &stubbornRunner{FakeRunner: NewFakeRunner(ports)}
	s := New(runner, ports)
	s.StopTimeout = time.Second
	s.Poll = time.Millisecond

	sp := spec("webserver", "nginx", 80)
	sp.StopGrace = 20 * time.Millisecond
	if err := s.Start(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background(), "webserver"); err != nil {
		t.Fatal(err)
	}
	if h := runner.Handle("webserver"); !h.Killed {
		t.Fatal("a process that ignored the signal was never killed")
	}
}

func TestRestartSwapsOneServiceForAnotherOnTheSamePort(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(context.Background(), spec("webserver", "apache", 80)); err != nil {
		t.Fatal(err)
	}
	started := runner.Started
	if len(started) != 2 || started[1].Label != "apache" {
		t.Fatalf("started = %+v, want nginx then apache", started)
	}
	st, _ := s.Status("webserver")
	if st.State != StateRunning || st.Label != "apache" {
		t.Fatalf("status = %+v, want apache running", st)
	}
}

func TestRestartWaitsForTheOldProcessToReleaseThePort(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	old := runner.Handle("webserver")
	old.HoldPort = true

	done := make(chan error, 1)
	go func() { done <- s.Restart(context.Background(), spec("webserver", "apache", 80)) }()

	select {
	case err := <-done:
		t.Fatalf("restart completed while port 80 was held (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	// The swap is in flight: whether it is still stopping the old process or
	// already waiting to start the new one, the GUI shows "switching".
	if st, _ := s.Status("webserver"); st.State != StateStopping && st.State != StateStarting {
		t.Fatalf("state while waiting = %q, want stopping or starting", st.State)
	}

	old.ReleasePort()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !s.Running("webserver") {
		t.Fatal("apache did not start after the port was released")
	}
}

func TestUnexpectedExitIsReportedAsFailed(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	runner.Handle("webserver").Exit()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := s.Status("webserver"); st.State == StateFailed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	st, _ := s.Status("webserver")
	t.Fatalf("state = %q, want failed after a crash", st.State)
}

func TestStopAllStopsEverything(t *testing.T) {
	s, _, _ := newTestSupervisor()
	for _, sp := range []Spec{spec("webserver", "nginx", 80), spec("php:8.3", "php", 9000), spec("project:legacy", "apache", 8080)} {
		if err := s.Start(context.Background(), sp); err != nil {
			t.Fatal(err)
		}
	}
	if !s.AnyRunning() {
		t.Fatal("nothing running before StopAll")
	}
	if err := s.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.AnyRunning() {
		t.Fatal("something is still running after StopAll")
	}
	if got := len(s.Statuses()); got != 3 {
		t.Fatalf("Statuses returned %d, want 3", got)
	}
}

func TestStateChangesAreBroadcast(t *testing.T) {
	s, _, _ := newTestSupervisor()
	var seen []State
	done := make(chan struct{}, 8)
	unsubscribe := s.OnChange(func(st Status) {
		seen = append(seen, st.State)
		select {
		case done <- struct{}{}:
		default:
		}
	})
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if len(seen) < 2 || seen[0] != StateStarting || seen[len(seen)-1] != StateRunning {
		t.Fatalf("observed transitions %v, want starting then running", seen)
	}

	before := len(seen)
	if err := s.Stop(context.Background(), "webserver"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != before {
		t.Fatal("unsubscribed listener still received events")
	}
}

func TestStartFailureFromTheRunnerIsSurfaced(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	runner.StartErr["webserver"] = errors.New("exec format error")
	err := s.Start(context.Background(), spec("webserver", "nginx", 80))
	if err == nil {
		t.Fatal("want the exec failure")
	}
	if st, _ := s.Status("webserver"); st.State != StateFailed {
		t.Fatalf("state = %q, want failed", st.State)
	}
}

func TestWaitPortFreeRespectsContext(t *testing.T) {
	ports := NewFakePorts()
	ports.Bind(80)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := WaitPortFree(ctx, ports, 80, time.Millisecond); err == nil {
		t.Fatal("want a timeout while the port stays bound")
	}
}

func TestSpecsWithNoPortSkipProbing(t *testing.T) {
	s, _, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("worker", "worker", 0)); err != nil {
		t.Fatal(err)
	}
	if !s.Running("worker") {
		t.Fatal("a portless process should be considered running once spawned")
	}
}

// stubbornRunner returns handles that ignore the graceful stop signal.
type stubbornRunner struct{ *FakeRunner }

func (r *stubbornRunner) Start(s Spec) (Handle, error) {
	h, err := r.FakeRunner.Start(s)
	if err != nil {
		return nil, err
	}
	return &stubbornHandle{FakeHandle: h.(*FakeHandle)}, nil
}

type stubbornHandle struct{ *FakeHandle }

func (h *stubbornHandle) Signal(os.Signal) error { return nil }

// php-fpm listens on loopback only, the webservers on every interface; the
// real prober must see both as taken.
func TestNetProberSeesLoopbackAndWildcardListeners(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", ":0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if (NetProber{}).Free(port) {
			t.Errorf("%s listener on %d reported free", addr, port)
		}
		ln.Close()
		if !(NetProber{}).Free(port) {
			t.Errorf("port %d still reported taken after close", port)
		}
	}
}
