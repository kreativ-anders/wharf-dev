package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/kreativ-anders/wharf-dev/daemon/internal/ipc"
)

// buildDaemon compiles this command so it can be run as the app runs it.
func buildDaemon(t *testing.T) string {
	t.Helper()
	name := "wharfd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build wharfd: %v\n%s", err, out)
	}
	return bin
}

// features/single-application.feature — "The application exits without
// shutting its daemon down"
//
// When the app dies it takes both halves of the situation with it: the parent
// pid disappears, and so do the read ends of the daemon's stdout and stderr
// pipes. The daemon must survive the second long enough to act on the first —
// a clean shutdown, which is what stops any webservers it started.
func TestDaemonShutsDownCleanlyWhenTheAppDies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1) as a stand-in parent")
	}
	bin := buildDaemon(t)

	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stand-in for the app: alive while the daemon starts, then gone.
	app := exec.Command("sleep", "1")
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	go app.Wait()

	daemon := exec.Command(bin,
		"--root", root, "--hosts", hosts, "--elevator", "direct",
		"--parent-pid", strconv.Itoa(app.Process.Pid))
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}

	endpoint := filepath.Join(root, "data", "wharf.endpoint")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(endpoint); err == nil {
			break
		}
		if time.Now().After(deadline) {
			daemon.Process.Kill()
			t.Fatal("daemon never published its endpoint")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The app held these; when it dies they close with it.
	stdout.Close()
	stderr.Close()

	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()

	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("daemon did not exit cleanly after the app died: %v\n"+
				"    a daemon killed mid-shutdown leaves its webservers running", err)
		}
	case <-time.After(15 * time.Second):
		daemon.Process.Kill()
		t.Fatal("daemon kept running after the app died")
	}

	if _, err := os.Stat(endpoint); !os.IsNotExist(err) {
		t.Fatal("endpoint file left behind: the daemon skipped its shutdown")
	}
}

// features/single-application.feature — "Quitting an application that
// started its own daemon"
//
// The app asks rather than signals, so the daemon runs its own shutdown on
// every OS — including Windows, where a signal cannot be caught.
func TestDaemonShutsDownCleanlyWhenAsked(t *testing.T) {
	bin := buildDaemon(t)

	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	daemon := exec.Command(bin, "--root", root, "--hosts", hosts, "--elevator", "direct")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()

	endpoint := filepath.Join(root, "data", ipc.EndpointFileName)
	c, err := ipc.DialFileWait(endpoint, 10*time.Second)
	if err != nil {
		daemon.Process.Kill()
		t.Fatalf("daemon never accepted a connection: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The reply can be lost as the daemon drops its connections; exiting is
	// the answer that counts.
	_ = c.Call(ctx, ipc.MethodShutdown, nil, nil)

	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("daemon did not exit cleanly when asked: %v", err)
		}
	case <-time.After(15 * time.Second):
		daemon.Process.Kill()
		t.Fatal("daemon kept running after it was asked to shut down")
	}

	if _, err := os.Stat(endpoint); !os.IsNotExist(err) {
		t.Fatal("endpoint file left behind: the daemon skipped its shutdown")
	}
}
