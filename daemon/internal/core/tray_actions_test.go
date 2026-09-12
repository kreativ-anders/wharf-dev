package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/ipc"
	"github.com/manuel-steinberg/wharf/daemon/internal/runtime"
	"github.com/manuel-steinberg/wharf/daemon/internal/supervisor"
)

// features/tray-actions.feature — "Starting a project from the tray"
func TestStartingAProjectFromTheTray(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("other-site")
	if got := h.project("my-kirby-site").State; got != string(supervisor.StateStopped) {
		t.Fatalf("project starts as %q, want stopped", got)
	}

	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("start from tray: %v", err)
	}

	// Then the daemon starts that project's required services
	if !h.sup.Running(runtime.WebserverID) {
		t.Fatal("the webserver was not started")
	}
	if !h.sup.Running(runtime.PHPServiceID("8.3")) {
		t.Fatal("the PHP backend was not started")
	}

	// And the tray menu updates the project's status to "running"
	if got := h.project("my-kirby-site").State; got != string(supervisor.StateRunning) {
		t.Fatalf("project status = %q, want running", got)
	}

	// And no other project is started
	if got := h.project("other-site").State; got != string(supervisor.StateStopped) {
		t.Fatalf("other-site = %q, want stopped", got)
	}
	if strings.Contains(h.readGenerated("nginx.conf"), "other-site.localhost") {
		t.Fatal("the webserver serves a project nobody started")
	}
}

// features/tray-actions.feature — "Stopping a project"
func TestStoppingAProject(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"my-kirby-site", "other-site"} {
		h.mustAdd(name)
		if err := h.d.StartProject(h.ctx(), name); err != nil {
			t.Fatal(err)
		}
	}

	if err := h.d.StopProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Then the daemon stops serving that project
	if strings.Contains(h.readGenerated("nginx.conf"), "my-kirby-site.localhost") {
		t.Fatal("the webserver still serves the stopped project")
	}
	// And the project's status updates to "stopped"
	if got := h.project("my-kirby-site").State; got != string(supervisor.StateStopped) {
		t.Fatalf("project status = %q, want stopped", got)
	}
	// And every other started project keeps running
	if got := h.project("other-site").State; got != string(supervisor.StateRunning) || !h.sup.Running(runtime.WebserverID) {
		t.Fatalf("other-site = %q, want it still running", got)
	}

	// Stopping the last one stops the webserver itself.
	if err := h.d.StopProject(h.ctx(), "other-site"); err != nil {
		t.Fatal(err)
	}
	if h.sup.Running(runtime.WebserverID) {
		t.Fatal("the webserver runs with no project started")
	}
}

// features/tray-actions.feature — "Restarting a project"
func TestRestartingAProject(t *testing.T) {
	for _, own := range []bool{false, true} {
		name := map[bool]string{false: "on the shared webserver", true: "on its own instance"}[own]
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.mustAdd("my-kirby-site")
			id := runtime.WebserverID
			if own {
				apache := "apache"
				if _, err := h.d.UpdateSettings(h.ctx(), "my-kirby-site", Settings{Webserver: &apache}); err != nil {
					t.Fatal(err)
				}
				id = runtime.ProjectServiceID("my-kirby-site")
			}
			if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
				t.Fatal(err)
			}
			// The PHP backend has died since, and the generated config is gone.
			php := runtime.PHPServiceID(h.d.Config().Services.PHP.Version)
			if err := h.sup.Stop(h.ctx(), php); err != nil {
				t.Fatal(err)
			}
			vhost := h.d.res.VhostPath(h.d.Config().WebserverFor(h.d.Config().Projects[0]), "my-kirby-site")
			if err := os.Remove(vhost); err != nil {
				t.Fatal(err)
			}
			before := h.countStarts(id)

			if err := h.d.RestartProject(h.ctx(), "my-kirby-site"); err != nil {
				t.Fatalf("restart: %v", err)
			}

			// Then its webserver config is generated again
			if _, err := os.Stat(vhost); err != nil {
				t.Fatalf("config not generated again: %v", err)
			}
			// And the process serving it is restarted with that config
			if got := h.countStarts(id); got != before+1 || !h.sup.Running(id) {
				t.Fatalf("%s started %d times (want %d), running=%v", id, got, before+1, h.sup.Running(id))
			}
			// And its PHP backend is started if it is not running
			if !h.sup.Running(php) {
				t.Fatal("the PHP backend was not started")
			}
		})
	}

	// A project that failed to start is retried by the same action.
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.runner.Refuse[runtime.WebserverID] = "bad config\n"
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err == nil {
		t.Fatal("the refused start succeeded")
	}
	delete(h.runner.Refuse, runtime.WebserverID)
	if err := h.d.RestartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatalf("restart after a failure: %v", err)
	}
	if got := h.project("my-kirby-site").State; got != string(supervisor.StateRunning) {
		t.Fatalf("project status = %q, want running", got)
	}
}

// features/tray-actions.feature — "Stopping all services from the tray"
// features/tray-actions.feature — "Stopping all from the main window"
func TestStoppingAllServicesFromTheTray(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	h.mustAdd("legacy-app")
	apache := "apache"
	if _, err := h.d.UpdateSettings(h.ctx(), "legacy-app", Settings{Webserver: &apache}); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}
	if err := h.d.StartProject(h.ctx(), "legacy-app"); err != nil {
		t.Fatal(err)
	}
	if !h.sup.AnyRunning() {
		t.Fatal("nothing is running before the stop-all")
	}

	if err := h.d.StopAll(h.ctx()); err != nil {
		t.Fatalf("stop all: %v", err)
	}

	// Then the daemon stops every running service and project
	for _, s := range h.sup.Statuses() {
		if s.State != supervisor.StateStopped {
			t.Fatalf("%s is %q after stop-all, want stopped", s.ID, s.State)
		}
	}
	for _, p := range h.d.State().Projects {
		if p.State != string(supervisor.StateStopped) {
			t.Fatalf("project %s is %q after stop-all", p.Name, p.State)
		}
	}

	// And the tray icon reflects an idle state
	if h.sup.AnyRunning() {
		t.Fatal("the tray would still show an active state")
	}
}

// features/tray-actions.feature — "Adding a project via the tray"
func TestAddingAProjectViaTheTray(t *testing.T) {
	h := newHarness(t)
	h.mkProject("dropped-in-folder")

	// The folder picker's result is a path; only folders under www/ qualify.
	picked := filepath.Join(h.root.WWW(), "dropped-in-folder")
	name := filepath.Base(picked)
	if !contains(h.d.State().Unregistered, name) {
		t.Fatalf("a folder in www/ is not offered for adding: %v", h.d.State().Unregistered)
	}

	if _, err := h.d.AddProject(h.ctx(), name); err != nil {
		t.Fatalf("add from tray: %v", err)
	}

	// Then selecting a folder under "www/" adds it as a project — no window
	// is involved, because the daemon holds the state, not the GUI.
	if _, ok := h.d.Config().Project(name); !ok {
		t.Fatal("project was not registered")
	}
	if contains(h.d.State().Unregistered, name) {
		t.Fatal("project is still listed as unregistered")
	}
}

func TestAddingAFolderOutsideWWWIsRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.AddProject(h.ctx(), "not-in-www"); err == nil {
		t.Fatal("a name with no folder under www/ should be rejected")
	}
}

// features/tray-actions.feature — "Opening the main window": the window
// reflects the same state as the tray menu. Both read the one snapshot the
// daemon publishes, so this checks that what arrives over IPC — what the
// Flutter GUI actually renders — matches the daemon's own view.
func TestMainWindowSeesTheSameStateAsTheTray(t *testing.T) {
	h := newHarness(t)
	h.mustAdd("my-kirby-site")
	if err := h.d.StartProject(h.ctx(), "my-kirby-site"); err != nil {
		t.Fatal(err)
	}

	socket := shortSocket(t)
	srv := ipc.NewServer(socket, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.d.Register(srv)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	defer srv.Close()

	c, err := ipc.DialWait(socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var overIPC State
	if err := c.Call(ctx, ipc.MethodState, nil, &overIPC); err != nil {
		t.Fatalf("state.get: %v", err)
	}
	if !reflect.DeepEqual(overIPC, h.d.State()) {
		t.Fatalf("the window's state differs from the daemon's:\n over ipc: %+v\n daemon:   %+v", overIPC, h.d.State())
	}

	// And a change made anywhere reaches the window without it asking.
	go func() { _ = h.d.StopAll(context.Background()) }()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no state event arrived after stop-all")
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Event != ipc.EventState {
				continue
			}
			var pushed State
			if err := json.Unmarshal(ev.Data, &pushed); err != nil {
				t.Fatal(err)
			}
			if pushed.Services.Webserver.State == string(supervisor.StateStopped) {
				return
			}
		}
	}
}

func TestUnknownMethodIsReportedNotFatal(t *testing.T) {
	h := newHarness(t)
	socket := shortSocket(t)
	srv := ipc.NewServer(socket, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.d.Register(srv)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	defer srv.Close()

	c, err := ipc.DialWait(socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	err = c.Call(ctx, "does.not.exist", nil, nil)
	var ipcErr *ipc.Error
	if err == nil || !asIPC(err, &ipcErr) || ipcErr.Code != ipc.CodeUnknownMethod {
		t.Fatalf("error = %v, want an unknown_method code", err)
	}
	// The connection must survive it.
	if err := c.Call(ctx, ipc.MethodPing, nil, nil); err != nil {
		t.Fatalf("connection did not survive an unknown method: %v", err)
	}
}

func asIPC(err error, target **ipc.Error) bool {
	e, ok := err.(*ipc.Error)
	if ok {
		*target = e
	}
	return ok
}
