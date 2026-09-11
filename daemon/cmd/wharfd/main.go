// Command wharfd is the background daemon: it owns every managed process and
// serves the GUI over a unix socket. It is a single cross-compiled binary with
// no runtime dependencies of its own (dev/architecture.md §3).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/manuel-steinberg/wharf/daemon/internal/core"
	"github.com/manuel-steinberg/wharf/daemon/internal/elevate"
	"github.com/manuel-steinberg/wharf/daemon/internal/ipc"
	"github.com/manuel-steinberg/wharf/daemon/internal/layout"
	"github.com/manuel-steinberg/wharf/daemon/internal/watchdog"
)

// version is stamped at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wharfd:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		rootFlag    = flag.String("root", "", "wharf root folder (default $WHARF_ROOT or ~/Wharf)")
		socketFlag  = flag.String("socket", "", "IPC socket path (default <root>/data/wharf.sock)")
		hostsFlag   = flag.String("hosts", "", "hosts file path (default the OS hosts file)")
		elevateFlag = flag.String("elevator", "system", "system, or direct to write the hosts file without prompting (development only)")
		transFlag   = flag.String("transport", "", "unix or tcp (default: unix, tcp on Windows where dart:io has no unix sockets)")
		parentFlag  = flag.Int("parent-pid", 0, "exit when this process is gone; the GUI passes its own pid so a crash never leaves the daemon behind")
		levelFlag   = flag.String("log-level", "info", "debug, info, warn or error")
		versionFlag = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Println("wharfd", version)
		return nil
	}

	// Started by the app, stdout and stderr are pipes the app holds. If the
	// app dies, the next log line hits a closed pipe — and Go's default is to
	// kill a process with SIGPIPE on a broken write to fd 1 or 2. That struck
	// mid-shutdown, before the daemon could stop its webservers. Ignored, a
	// broken pipe is an ordinary write error the logger shrugs off.
	signal.Ignore(syscall.SIGPIPE)

	root, err := layout.Resolve(*rootFlag)
	if err != nil {
		return err
	}
	if err := root.Ensure(); err != nil {
		return err
	}

	log, closeLog := newLogger(*levelFlag, root.LogDir())
	defer closeLog()
	slog.SetDefault(log)

	opts := core.Options{Root: root, HostsPath: *hostsFlag, Log: log}
	switch *elevateFlag {
	case "system", "":
	case "direct":
		log.Warn("elevation disabled: hosts file will be written directly")
		opts.Elevator = elevate.Direct{}
	default:
		return fmt.Errorf("unknown -elevator %q, want system or direct", *elevateFlag)
	}

	d, err := core.New(opts)
	if err != nil {
		return err
	}

	socket := *socketFlag
	if socket == "" {
		socket = root.Socket()
	}
	srv := ipc.NewServer(socket, log)
	switch ipc.Transport(*transFlag) {
	case "":
	case ipc.TransportUnix, ipc.TransportTCP:
		srv.SetTransport(ipc.Transport(*transFlag))
	default:
		return fmt.Errorf("unknown -transport %q, want unix or tcp", *transFlag)
	}
	d.Register(srv)
	if err := srv.Listen(); err != nil {
		return err
	}

	// Clients find the daemon through this file rather than by guessing a
	// socket path or a port.
	endpointPath := filepath.Join(root.Data(), ipc.EndpointFileName)
	if err := ipc.WriteEndpoint(endpointPath, srv.Endpoint()); err != nil {
		return err
	}
	defer os.Remove(endpointPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go d.WatchConfig(ctx)

	// Started by the GUI: if the GUI disappears without stopping us — a
	// crash, a force quit — shut down rather than keep services running with
	// nobody left to see them.
	go watchdog.WatchParent(ctx, *parentFlag, watchdog.DefaultPoll, func() {
		log.Info("parent application is gone; shutting down", "pid", *parentFlag)
		stop()
	})

	ep := srv.Endpoint()
	log.Info("wharfd started", "version", version, "root", root.Dir,
		"transport", ep.Transport, "address", ep.Path+ep.Address, "endpoint", endpointPath)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-serveErr:
		if err != nil {
			return err
		}
	}

	// Managed processes must not outlive the daemon: an orphaned webserver
	// still holding port 80 is the worst thing this tool could leave behind.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := d.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
	return srv.Close()
}

// newLogger writes to data/log/wharfd.log as well as stderr. When the app runs
// the daemon, stderr is a pipe nobody reads, so the file is the only place its
// log survives. The file comes first: io.MultiWriter stops at the first
// failing writer, and after the app is gone that is stderr.
func newLogger(level, logDir string) (*slog.Logger, func()) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}

	f, err := os.OpenFile(filepath.Join(logDir, "wharfd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), func() {}
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(f, os.Stderr), opts)), func() { f.Close() }
}
