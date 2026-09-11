package core

import (
	"strings"
	"testing"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/config"
	"github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
)

// features/service-management.feature — "Switching the active webserver"
func TestSwitchingTheActiveWebserver(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start project: %v", err)
	}
	if got := h.d.State().Services.Webserver; got.Active != "nginx" || got.State != string(supervisor.StateRunning) {
		t.Fatalf("expected nginx running, got %+v", got)
	}
	nginx := h.runner.Handle(runtime.WebserverID)

	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatalf("switch webserver: %v", err)
	}

	// Then the daemon stops the running "nginx" process
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("nginx was not stopped")
	default:
	}
	if err := waitFor(func() bool { return len(nginx.Signals) > 0 }); err != nil {
		t.Fatal("nginx did not receive a stop signal")
	}

	// And the daemon starts the "apache" process
	st := h.d.State()
	if st.Services.Webserver.State != string(supervisor.StateRunning) {
		t.Fatalf("webserver state = %q, want running", st.Services.Webserver.State)
	}
	started := h.runner.Started
	last := started[len(started)-1]
	if last.Label != "apache" {
		t.Fatalf("last started service = %q, want apache", last.Label)
	}
	if last.Port != runtime.HTTPPort {
		t.Fatalf("apache started on port %d, want %d", last.Port, runtime.HTTPPort)
	}

	// And the config file's "services.webserver.active" value is updated
	if got := h.d.Config().Services.Webserver.Active; got != "apache" {
		t.Fatalf("config active webserver = %q, want apache", got)
	}
	reread, err := config.Load(h.root.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if got := reread.Get().Services.Webserver.Active; got != "apache" {
		t.Fatalf("persisted active webserver = %q, want apache", got)
	}
}

// features/service-management.feature — "Port conflict on switch"
func TestPortConflictOnSwitchWaitsForRelease(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start project: %v", err)
	}

	// Given "nginx" is bound to port 80 and does not release it on exit.
	nginx := h.runner.Handle(runtime.WebserverID)
	nginx.HoldPort = true

	done := make(chan error, 1)
	go func() { done <- h.d.SetWebserver(h.ctx(), "apache") }()

	// Then the GUI shows a "switching webserver" status until the new process
	// is confirmed running.
	if err := waitFor(func() bool { return h.d.State().Services.Webserver.Switching }); err != nil {
		t.Fatal("webserver never entered the switching state")
	}
	if apacheStarted(h) {
		t.Fatal("apache was started while port 80 was still held")
	}

	select {
	case err := <-done:
		t.Fatalf("switch completed before the port was released (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}

	// And the daemon waits for the port to be released before starting apache.
	nginx.ReleasePort()

	if err := <-done; err != nil {
		t.Fatalf("switch after port release: %v", err)
	}
	if !apacheStarted(h) {
		t.Fatal("apache did not start after the port was released")
	}
	if st := h.d.State().Services.Webserver; st.Switching || st.State != string(supervisor.StateRunning) {
		t.Fatalf("after switch: %+v, want running and not switching", st)
	}
}

// features/service-management.feature — "Per-project override takes precedence
// over the global default"
func TestPerProjectOverrideTakesPrecedence(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	legacy := h.mustAdd("legacy-app")

	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatalf("set override: %v", err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start shared project: %v", err)
	}
	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatalf("start overridden project: %v", err)
	}

	// Then "legacy-app" is served by "apache" on its own port
	spec := findSpec(h, runtime.ProjectServiceID("legacy-app"))
	if spec == nil {
		t.Fatal("legacy-app has no webserver instance of its own")
	}
	if !strings.Contains(spec.Path, "httpd") {
		t.Fatalf("legacy-app served by %q, want apache", spec.Path)
	}
	if spec.Port != legacy.Port || spec.Port == runtime.HTTPPort {
		t.Fatalf("legacy-app on port %d, want its own port %d", spec.Port, legacy.Port)
	}

	// And the global "nginx" instance is unaffected
	global := findSpec(h, runtime.WebserverID)
	if global == nil || global.Label != "nginx" {
		t.Fatalf("global webserver = %+v, want nginx", global)
	}
	if !h.sup.Running(runtime.WebserverID) {
		t.Fatal("global nginx is not running")
	}
	// The shared instance must not have been given the overridden project.
	conf := h.readGenerated("nginx.conf")
	if strings.Contains(conf, "legacy-app.wharf") {
		t.Fatal("overridden project still appears in the shared nginx config")
	}
	if !strings.Contains(conf, "my-kirby-site.wharf") {
		t.Fatal("shared project is missing from the nginx config")
	}
}

func apacheStarted(h *harness) bool {
	for _, s := range h.runner.Started {
		if s.Label == "apache" {
			return true
		}
	}
	return false
}

// findSpec returns the most recent spec started under an ID.
func findSpec(h *harness, id string) *supervisor.Spec {
	var out *supervisor.Spec
	for i := range h.runner.Started {
		if h.runner.Started[i].ID == id {
			s := h.runner.Started[i]
			out = &s
		}
	}
	return out
}

// waitFor polls a condition, which is how these tests observe asynchronous
// state transitions without sleeping for a fixed time.
func waitFor(cond func() bool) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errTimeout
}

var errTimeout = timeoutError{}

type timeoutError struct{}

func (timeoutError) Error() string { return "timed out waiting for condition" }
