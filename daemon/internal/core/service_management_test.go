package core

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/config"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/runtime"
	"github.com/kreativ-anders/wharf-dev/daemon/internal/supervisor"
)

// features/service-management.feature — "A project's own instance never takes
// a port another program holds"
func TestAProjectsOwnInstanceNeverTakesAPortAnotherProgramHolds(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	port := func(name string) int {
		p, _ := h.d.Config().Project(name)
		return p.Port
	}
	recorded := port("legacy-app")
	// INFO: And another program listens on the loopback port recorded for it
	h.ports.Bind(recorded)

	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatalf("start: %v", err)
	}

	// INFO: Then its own instance listens on the next free port instead, and the
	// config records it
	moved := port("legacy-app")
	if moved == 0 || moved == recorded {
		t.Fatalf("port = %d, want it moved off %d", moved, recorded)
	}
	own := runtime.ProjectServiceID("legacy-app")
	if spec, _ := h.runner.LastSpec(own); spec.Port != moved || !h.sup.Running(own) {
		t.Fatalf("own instance on port %d (running=%v), want %d", spec.Port, h.sup.Running(own), moved)
	}
	// INFO: And the global instance forwards "legacy-app.localhost" to that port
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(conf, "127.0.0.1:"+strconv.Itoa(moved)) || strings.Contains(conf, "127.0.0.1:"+strconv.Itoa(recorded)) {
		t.Fatalf("the front door does not forward to port %d:\n%s", moved, conf)
	}

	// INFO: And a project added while a port is taken is not given that port
	h.mustAdd("new-site")
	if got := port("new-site"); got == recorded || got == moved {
		t.Fatalf("new-site was given port %d, which is already held", got)
	}
}

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

	// INFO: Then the daemon stops the running "nginx" process
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("nginx was not stopped")
	default:
	}
	if err := waitFor(func() bool { return len(nginx.Signals) > 0 }); err != nil {
		t.Fatal("nginx did not receive a stop signal")
	}

	// INFO: And the daemon starts the "apache" process
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

	// INFO: And the config file's "services.webserver.active" value is updated
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

	// INFO: Given "nginx" is bound to port 80 and does not release it on exit.
	nginx := h.runner.Handle(runtime.WebserverID)
	nginx.HoldPort = true

	done := make(chan error, 1)
	go func() { done <- h.d.SetWebserver(h.ctx(), "apache") }()

	// INFO: Then the GUI shows a "switching webserver" status until the new process
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

	// INFO: And the daemon waits for the port to be released before starting apache.
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
	port := strconv.Itoa(legacy.Port)

	// INFO: Then "legacy-app" is served by its own "apache" instance, listening
	// only on loopback
	spec := findSpec(h, runtime.ProjectServiceID("legacy-app"))
	if spec == nil || !strings.Contains(spec.Path, "httpd") {
		t.Fatalf("legacy-app has no apache instance of its own: %+v", spec)
	}
	own := h.readGenerated("project-legacy-app-apache.conf")
	if !strings.Contains(own, "Listen 127.0.0.1:"+port+"\n") || strings.Contains(own, "Listen 80") {
		t.Fatalf("legacy-app's apache does not listen on loopback only:\n%s", own)
	}

	// INFO: And the global "nginx" instance forwards "legacy-app.localhost" to it
	front := h.readGenerated("nginx.conf")
	if !strings.Contains(vhostBlock(t, front, "legacy-app.localhost"), "proxy_pass http://127.0.0.1:"+port+";") {
		t.Fatalf("nginx does not forward legacy-app to its instance:\n%s", front)
	}

	// INFO: And "legacy-app" is reachable at "http://legacy-app.localhost", without
	// a port
	if got := h.project("legacy-app"); got.URL != "http://legacy-app.localhost" || got.State != string(supervisor.StateRunning) {
		t.Fatalf("legacy-app = %q %q, want running at http://legacy-app.localhost", got.State, got.URL)
	}

	// INFO: And PHP in "legacy-app" sees port 80, so Kirby builds its links without
	// a port: the front door sends the port the browser used, and the
	// instance hands that to PHP.
	if !strings.Contains(front, "proxy_set_header X-Forwarded-Port $server_port;") ||
		!strings.Contains(own, `ProxyFCGISetEnvIf "-n %{HTTP:X-Forwarded-Port}" SERVER_PORT "%{HTTP:X-Forwarded-Port}"`) {
		t.Fatalf("the browser's port does not reach PHP:\n%s\n%s", front, own)
	}

	// INFO: The global instance still serves everybody else itself.
	global := findSpec(h, runtime.WebserverID)
	if global == nil || global.Label != "nginx" || !h.sup.Running(runtime.WebserverID) {
		t.Fatalf("global webserver = %+v, want nginx running", global)
	}
	if !strings.Contains(vhostBlock(t, front, "my-kirby-site.localhost"), "fastcgi_pass") {
		t.Fatal("the shared project is not served by the global nginx itself")
	}
}

// features/service-management.feature — "Choosing the active webserver for a
// project starts no second instance"
func TestChoosingTheActiveWebserverForAProjectStartsNoSecondInstance(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	nginx := "nginx"
	if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &nginx}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then "my-kirby-site" is served by the global "nginx" instance
	if !h.sup.Running(runtime.WebserverID) {
		t.Fatal("the global nginx is not running")
	}
	if !strings.Contains(vhostBlock(t, h.readGenerated("nginx.conf"), "my-kirby-site.localhost"), "fastcgi_pass") {
		t.Fatal("the global nginx does not serve the project")
	}
	// INFO: And no second webserver process is started for it
	if spec := findSpec(h, runtime.ProjectServiceID("my-kirby-site")); spec != nil {
		t.Fatalf("a second instance was started: %+v", spec)
	}
	if got := h.project("my-kirby-site"); got.State != string(supervisor.StateRunning) || got.URL != "http://my-kirby-site.localhost" {
		t.Fatalf("project = %q %q", got.State, got.URL)
	}
}

// features/service-management.feature — "The front door answers on port 80
// without projects of its own"
func TestTheFrontDoorAnswersOnPort80WithoutProjectsOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}

	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatal(err)
	}

	// INFO: Then the global webserver starts on port 80 and forwards to it
	front := findSpec(h, runtime.WebserverID)
	if front == nil || front.Port != runtime.HTTPPort || !h.sup.Running(runtime.WebserverID) {
		t.Fatalf("front door = %+v, want running on port 80", front)
	}
	conf := h.readGenerated("nginx.conf")
	if !strings.Contains(vhostBlock(t, conf, "legacy-app.localhost"), "proxy_pass") {
		t.Fatalf("the front door does not forward legacy-app:\n%s", conf)
	}
	// INFO: And a request for a name no project has is refused, not answered by
	// another project
	if !strings.Contains(conf, "listen 80 default_server;") || !strings.Contains(conf, "return 404;") {
		t.Fatalf("no default server refuses unknown names:\n%s", conf)
	}

	// INFO: The project is only as reachable as its front door.
	if got := h.project("legacy-app").State; got != string(supervisor.StateRunning) {
		t.Fatalf("legacy-app = %q, want running", got)
	}
	if err := h.sup.Stop(h.ctx(), runtime.WebserverID); err != nil {
		t.Fatal(err)
	}
	if got := h.project("legacy-app").State; got == string(supervisor.StateRunning) {
		t.Fatal("legacy-app is shown running while its front door is down")
	}
}

// features/service-management.feature — "The front door answers this machine
// only". That nginx and Apache then refuse another address was checked
// against the real binaries; this pins the rule that makes them.
func TestTheFrontDoorAnswersThisMachineOnly(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"my-kirby-site", "legacy-app"} {
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	// INFO: Then the front door refuses it — before any server block, so every
	// project the front door serves or forwards inherits the rule. And a
	// request from this machine is served as before.
	front := h.readGenerated("nginx.conf")
	rule := strings.Index(front, "allow 127.0.0.0/8;\n  allow ::1;\n  deny all;")
	if rule < 0 || rule > strings.Index(front, "server {") {
		t.Fatalf("the front door does not refuse other machines ahead of every server:\n%s", front)
	}

	// INFO: And a project behind the front door is reached through it alone
	if own := h.readGenerated("project-legacy-app-apache.conf"); strings.Contains(own, "Listen 80") {
		t.Fatalf("legacy-app's own instance listens beyond loopback:\n%s", own)
	}

	// INFO: The same holds with Apache at the front door.
	if err := h.d.SetWebserver(h.ctx(), "apache"); err != nil {
		t.Fatal(err)
	}
	if conf := h.readGenerated("apache.conf"); !strings.Contains(conf,
		`<If "! -R '127.0.0.0/8' && ! -R '::1/128'">`+"\n  Require all denied\n</If>") {
		t.Fatalf("Apache at the front door does not refuse other machines:\n%s", conf)
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

// features/service-management.feature — "Linux asks once before the front
// door first takes port 80". That the setting survives a reboot is the file
// in /etc/sysctl.d/ the real prompt writes; the fake stands in for both.
func TestLinuxAsksOnceBeforeTheFrontDoorFirstTakesPort80(t *testing.T) {
	h := newHarness(t)
	h.el.LowPortsBlocked = true
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")

	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if got := h.el.LowPortsPrompts(); got != 1 {
		t.Fatalf("prompts = %d, want 1", got)
	}
	if front := findSpec(h, runtime.WebserverID); front == nil || front.Port != runtime.HTTPPort {
		t.Fatalf("front door = %+v, want started on port 80", front)
	}

	if err := h.d.StartProject(h.ctx(), "other-site"); err != nil {
		t.Fatal(err)
	}
	if got := h.el.LowPortsPrompts(); got != 1 {
		t.Fatalf("prompts = %d after a second start, want still 1", got)
	}
}

// features/service-management.feature — "Declining the port prompt"
func TestDecliningThePortPrompt(t *testing.T) {
	h := newHarness(t)
	h.el.LowPortsBlocked = true
	h.el.Decline = true
	h.mustAdd("my-kirby-site")

	err := h.d.StartProject(h.ctx(), "my-kirby-site")
	if err == nil || !strings.Contains(err.Error(), "ports 80 and 443") {
		t.Fatalf("err = %v, want one naming the ports", err)
	}
	if h.sup.Running(runtime.WebserverID) {
		t.Fatal("the front door started without permission for port 80")
	}
	got := h.project("my-kirby-site")
	if got.State == string(supervisor.StateRunning) || !strings.Contains(got.Error, "ip_unprivileged_port_start") {
		t.Fatalf("project = %+v, want not running and saying how to allow the ports", got)
	}
}
