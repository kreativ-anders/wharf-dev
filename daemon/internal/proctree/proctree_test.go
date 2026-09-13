package proctree

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the processes under test, so the suite needs no
// real webserver: a "master" starts a "worker" and reports its pid, the way
// nginx starts its workers.
const helperEnv = "WHARF_PROCTREE_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "worker":
		time.Sleep(time.Minute)
	case "master", "master-exits":
		worker := helper("worker")
		if err := worker.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(worker.Process.Pid)
		if os.Getenv(helperEnv) == "master" {
			time.Sleep(time.Minute)
		}
	case "owner":
		// INFO: A stand-in for the daemon: it starts a tree of its own and reports
		// that tree's worker.
		_, worker, err := startMaster("master")
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(worker)
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func helper(role string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+role)
	return cmd
}

// startMaster starts a helper through Start and returns the tree and the
// worker pid the helper reports. The pid is read before Wait runs, because
// Wait closes the pipe it arrives on.
func startMaster(role string) (*Tree, int, error) {
	cmd := helper(role)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	tree, err := Start(cmd)
	if err != nil {
		return nil, 0, err
	}
	pid, err := readPID(out)
	go func() {
		cmd.Wait()
		tree.Release()
	}()
	if err != nil {
		tree.Kill()
		return nil, 0, err
	}
	return tree, pid, nil
}

func readPID(r io.Reader) (int, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("helper printed no pid: %w", err)
	}
	line = strings.TrimSpace(line)
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("helper said %q", line)
	}
	return pid, nil
}

// waitGone fails the test unless pid exits within a few seconds.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// features/service-management.feature — "Stopping a webserver stops its
// worker processes too"
func TestKillStopsTheWholeTree(t *testing.T) {
	tree, worker, err := startMaster("master")
	if err != nil {
		t.Fatal(err)
	}
	if !Alive(worker) {
		t.Fatal("the worker never started")
	}
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, worker)
}

// A master that dies on its own must not leave its workers holding the port.
func TestReleaseStopsWhatTheRootLeftBehind(t *testing.T) {
	_, worker, err := startMaster("master-exits")
	if err != nil {
		t.Fatal(err)
	}
	waitGone(t, worker)
}
