package core

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
)

// features/single-application.feature — "Nothing starts once Wharf is quitting"
func TestNothingStartsOnceWharfIsQuitting(t *testing.T) {
	// INFO: The added project's start waits for mu, and so does Shutdown;
	// whichever gets it first, nothing may be left running. Several rounds
	// give both orders a chance.
	for round := 0; round < 10; round++ {
		h := newHarness(t)
		if err := os.MkdirAll(h.root.ProjectDir("site"), 0o755); err != nil {
			t.Fatal(err)
		}

		h.d.mu.Lock()
		_, err := h.d.addedLocked(h.ctx(), "site", "", "", "", true)
		h.d.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := h.d.Shutdown(h.ctx()); err != nil {
			t.Fatal(err)
		}
		if err := waitFor(func() bool { return h.d.busy.Load() == 0 }); err != nil {
			t.Fatal("the added project's start never finished")
		}
		if h.sup.AnyRunning() {
			t.Fatalf("round %d: a service is running after Shutdown: %+v", round, h.sup.Statuses())
		}

		// INFO: And a project started after the quit began is refused.
		if err := h.d.StartProject(h.ctx(), "site"); err == nil {
			t.Fatal("a project started after Shutdown")
		}
		if h.sup.AnyRunning() {
			t.Fatal("a refused start left a service running")
		}
	}
}

// features/single-application.feature — "A download does not hold up other
// actions"
func TestADownloadDoesNotHoldUpOtherActions(t *testing.T) {
	release := make(chan struct{})
	h := newHarness(t)
	h.php.fn = func(ctx context.Context, version, dest string) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return (&fakeInstaller{}).Install(ctx, version, dest)
	}
	if err := os.MkdirAll(h.root.ProjectDir("site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.AddProject(h.ctx(), "site"); err != nil {
		t.Fatal(err)
	}

	dir, err := os.MkdirTemp("/tmp", "wh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	srv := ipc.NewServer(filepath.Join(dir, "w.sock"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.d.Register(srv)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	t.Cleanup(func() { cancel(); srv.Close() })

	c, err := ipc.DialEndpoint(srv.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	downloaded := make(chan error, 1)
	go func() {
		downloaded <- c.Call(context.Background(), ipc.MethodInstallPHP, map[string]string{"version": "8.4"}, nil)
	}()
	if err := waitFor(func() bool { return len(h.d.downloadingVersions()) == 1 }); err != nil {
		t.Fatal("the download never began")
	}

	// INFO: When the user starts a project, then that is carried out at once.
	callCtx, cancelCall := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCall()
	if err := c.Call(callCtx, ipc.MethodProjectStart, map[string]string{"name": "site"}, nil); err != nil {
		t.Fatalf("starting a project during a download: %v", err)
	}
	if st := h.project("site").State; st != "running" {
		t.Fatalf("state = %q during the download, want running", st)
	}

	// INFO: And the download goes on and reports when it is done.
	close(release)
	select {
	case err := <-downloaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the download was never answered")
	}
	if !contains(h.d.Config().Services.PHP.Available, "8.4") {
		t.Fatalf("PHP 8.4 not available after its download: %v", h.d.Config().Services.PHP.Available)
	}
}
