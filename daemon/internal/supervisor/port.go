package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

// Prober answers whether a TCP port is currently bindable. It is an interface
// so that port-contention behaviour can be tested without binding real ports.
type Prober interface {
	Free(port int) bool
}

// NetProber answers whether something holds a port, the way it matters for
// the services Wharf runs. Two checks, because either alone is wrong:
//
//   - A loopback connection that succeeds means a listener, whatever address
//     it bound — php-fpm binds 127.0.0.1 only.
//   - A wildcard bind that fails means a listener on all interfaces, which is
//     what the webservers use. The bind cannot be on loopback: macOS lets an
//     unprivileged process bind a port below 1024 only on the wildcard
//     address, so a loopback bind reports port 80 as taken forever.
//
// A bind refused for lack of permission (port 80 on Linux) says nothing about
// whether the port is in use; the webserver reports that problem itself.
type NetProber struct{}

func (NetProber) Free(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return errors.Is(err, syscall.EACCES)
	}
	ln.Close()
	return true
}

// WaitPortFree blocks until the port is bindable or ctx is done. A webserver
// that has just been asked to stop may hold its listening socket for a moment
// after the process exits; starting its replacement before then fails with an
// "address already in use" the user did nothing to deserve
// (service-management.feature, "Port conflict on switch").
func WaitPortFree(ctx context.Context, p Prober, port int, poll time.Duration) error {
	if port <= 0 {
		return nil
	}
	if poll <= 0 {
		poll = 50 * time.Millisecond
	}
	for {
		if p.Free(port) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("port %d is still in use by another program; quit it, then start again: %w", port, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// WaitPortBound blocks until something is listening on the port, which is how
// a started service is confirmed running rather than merely spawned.
func WaitPortBound(ctx context.Context, p Prober, port int, poll time.Duration) error {
	if port <= 0 {
		return nil
	}
	if poll <= 0 {
		poll = 50 * time.Millisecond
	}
	for {
		if !p.Free(port) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("nothing listening on port %d: %w", port, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// FreePort asks the OS for an unused port. Used to assign a project its own
// port when it is served by a per-project webserver override.
func FreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
