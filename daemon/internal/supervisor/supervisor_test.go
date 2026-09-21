package supervisor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
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
	runner.Ports = nil // INFO: started processes never bind their port
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
	// INFO: The swap is in flight: whether it is still stopping the old process or
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

func TestAProcessThatExitsDuringStartupFailsAtOnceWithItsOwnError(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	s.StartTimeout = time.Minute
	log := filepath.Join(t.TempDir(), "nginx.log")
	// INFO: An earlier run's complaint must not be blamed for this one.
	os.WriteFile(log, []byte("2026/09/11 10:00:00 [emerg] 1#0: an old problem\n"), 0o644)
	runner.Refuse["webserver"] = "2026/09/11 10:13:20 [emerg] 71176#0: \"server\" directive is not allowed here in /w/test.nginx.conf:17\n"

	sp := spec("webserver", "nginx", 80)
	sp.LogPath = log
	began := time.Now()
	err := s.Start(context.Background(), sp)
	if err == nil {
		t.Fatal("a process that exited should not be reported as running")
	}
	if waited := time.Since(began); waited > 5*time.Second {
		t.Fatalf("waited %s for a process that had already exited", waited)
	}
	want := `nginx did not start: "server" directive is not allowed here in /w/test.nginx.conf:17`
	if err.Error() != want {
		t.Fatalf("error = %q\n want %q", err, want)
	}
	if st, _ := s.Status("webserver"); st.State != StateFailed || st.Error != want {
		t.Fatalf("status = %+v", st)
	}
}

func TestApacheSplitsItsStartupErrorOverTwoLines(t *testing.T) {
	log := filepath.Join(t.TempDir(), "apache.log")
	os.WriteFile(log, []byte("AH00526: Syntax error on line 17 of /w/test.apache.conf:\nInvalid command 'server'\n"), 0o644)
	want := "AH00526: Syntax error on line 17 of /w/test.apache.conf: Invalid command 'server'"
	if got := startupOutput(log, 0); got != want {
		t.Fatalf("got %q, want %q", got, want)
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

// Windows cannot deliver a stop signal. Waiting out the grace period there
// only delayed the kill, and every webserver switch took five seconds.
func TestStopKillsAtOnceWhenTheSignalCannotBeSent(t *testing.T) {
	ports := NewFakePorts()
	runner := &unsignallableRunner{FakeRunner: NewFakeRunner(ports)}
	s := New(runner, ports)
	s.Poll = time.Millisecond

	sp := spec("webserver", "nginx", 80)
	sp.StopGrace = time.Minute
	if err := s.Start(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if err := s.Stop(context.Background(), "webserver"); err != nil {
		t.Fatal(err)
	}
	if !runner.Handle("webserver").Killed {
		t.Fatal("the process was never killed")
	}
	if took := time.Since(begin); took > time.Second {
		t.Fatalf("stop took %s, waiting on a signal that was never delivered", took)
	}
}

type unsignallableRunner struct{ *FakeRunner }

func (r *unsignallableRunner) Start(s Spec) (Handle, error) {
	h, err := r.FakeRunner.Start(s)
	if err != nil {
		return nil, err
	}
	return &unsignallableHandle{FakeHandle: h.(*FakeHandle)}, nil
}

type unsignallableHandle struct{ *FakeHandle }

func (h *unsignallableHandle) Signal(os.Signal) error { return errors.New("not supported by windows") }

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

// features/single-application.feature — "Nothing starts once Wharf is quitting"
func TestNothingStartsOnceShutdownHasBegun(t *testing.T) {
	s, runner, _ := newTestSupervisor()
	if err := s.Start(context.Background(), spec("webserver", "nginx", 80)); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.AnyRunning() {
		t.Fatal("Shutdown left a process running")
	}
	if err := s.Start(context.Background(), spec("php:8.3", "PHP 8.3", 9083)); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Start after Shutdown = %v, want ErrShuttingDown", err)
	}
	if got := runner.StartedIDs(); len(got) != 1 {
		t.Fatalf("started %v, want only the webserver from before Shutdown", got)
	}
}

// startWaitingForPort starts a spec whose port another program holds, and
// returns once the start is waiting for it.
func startWaitingForPort(t *testing.T, s *Supervisor, ports *FakePorts) <-chan error {
	t.Helper()
	ports.Bind(80)
	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background(), spec("webserver", "nginx", 80)) }()
	deadline := time.Now().Add(time.Second)
	for {
		if st, ok := s.Status("webserver"); ok && st.State == StateStarting {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("the start never began waiting for its port")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopCallsOffAStartStillWaitingForItsPort(t *testing.T) {
	s, runner, ports := newTestSupervisor()
	done := startWaitingForPort(t, s, ports)

	if err := s.Stop(context.Background(), "webserver"); err != nil {
		t.Fatal(err)
	}
	ports.Release(80)
	if err := <-done; err != nil {
		t.Fatalf("a start called off by Stop = %v, want nil", err)
	}
	if got := runner.StartedIDs(); len(got) != 0 {
		t.Fatalf("started %v after Stop called the start off", got)
	}
	if st, _ := s.Status("webserver"); st.State != StateStopped {
		t.Fatalf("state = %s, want stopped", st.State)
	}
}

// features/single-application.feature — "Nothing starts once Wharf is quitting"
func TestShutdownCallsOffAStartStillWaitingForItsPort(t *testing.T) {
	s, runner, ports := newTestSupervisor()
	done := startWaitingForPort(t, s, ports)

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ports.Release(80)
	if err := <-done; !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("a start overtaken by Shutdown = %v, want ErrShuttingDown", err)
	}
	if got := runner.StartedIDs(); len(got) != 0 {
		t.Fatalf("started %v after Shutdown", got)
	}
}

// features/project-logs.feature — "A log that has grown large starts afresh"
func TestALogThatHasGrownLargeStartsAfresh(t *testing.T) {
	s, _, _ := newTestSupervisor()
	dir := t.TempDir()
	big := filepath.Join(dir, "nginx.log")
	access := filepath.Join(dir, "access.log")
	small := filepath.Join(dir, "error.log")
	for path, size := range map[string]int{big: MaxLogSize + 1, access: MaxLogSize + 1, small: 10} {
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(access+".1", []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}

	sp := spec("webserver", "nginx", 80)
	sp.LogPath, sp.Logs = big, []string{access, small}
	if err := s.Start(context.Background(), sp); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{big, access} {
		if logSize(path) > 0 {
			t.Fatalf("%s was not started afresh", filepath.Base(path))
		}
		if logSize(path+".1") != MaxLogSize+1 {
			t.Fatalf("%s was not moved aside to .1", filepath.Base(path))
		}
	}
	if logSize(small) != 10 {
		t.Fatal("a small log was moved aside")
	}
}
