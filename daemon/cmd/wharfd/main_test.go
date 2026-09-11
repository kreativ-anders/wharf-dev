package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// buildDaemon compiles this command so it can be run as the app runs it.
func buildDaemon(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "wharfd")
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
