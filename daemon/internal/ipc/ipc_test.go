package ipc

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newServer(t *testing.T, transport Transport) (*Server, Endpoint) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	srv := NewServer(filepath.Join(dir, "w.sock"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetTransport(transport)
	srv.Handle("echo", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Say string `json:"say"`
		}
		if len(raw) > 0 {
			json.Unmarshal(raw, &p)
		}
		return map[string]string{"heard": p.Say}, nil
	})
	srv.Handle("boom", func(context.Context, json.RawMessage) (any, error) {
		return nil, Errorf(CodeConflict, "not now")
	})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	t.Cleanup(func() { cancel(); srv.Close() })
	return srv, srv.Endpoint()
}

// Both transports must behave identically above the wire: same methods, same
// events, same errors. Only Windows uses TCP, but it is tested everywhere.
func TestBothTransportsBehaveTheSame(t *testing.T) {
	for _, transport := range []Transport{TransportUnix, TransportTCP} {
		t.Run(string(transport), func(t *testing.T) {
			srv, ep := newServer(t, transport)

			c, err := DialEndpoint(ep)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()

			var out struct {
				Heard string `json:"heard"`
			}
			if err := c.Call(context.Background(), "echo", map[string]string{"say": "hello"}, &out); err != nil {
				t.Fatal(err)
			}
			if out.Heard != "hello" {
				t.Fatalf("heard %q", out.Heard)
			}

			err = c.Call(context.Background(), "boom", nil, nil)
			var ipcErr *Error
			if err == nil {
				t.Fatal("want an error")
			}
			if e, ok := err.(*Error); !ok || e.Code != CodeConflict {
				t.Fatalf("error = %v, want a conflict code", err)
			}
			_ = ipcErr

			srv.Broadcast(EventState, map[string]string{"hello": "world"})
			select {
			case ev := <-c.Events():
				if ev.Event != EventState {
					t.Fatalf("event = %q", ev.Event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no event arrived")
			}
		})
	}
}

func TestUnixEndpointNeedsNoToken(t *testing.T) {
	_, ep := newServer(t, TransportUnix)
	if ep.Token != "" {
		t.Fatal("a unix socket should not mint a token; file permissions authorise it")
	}
	if ep.Path == "" {
		t.Fatal("no socket path published")
	}
}

// On TCP any local process can connect, so the token is the authorisation.
func TestTCPRejectsAConnectionWithoutTheToken(t *testing.T) {
	_, ep := newServer(t, TransportTCP)
	if ep.Token == "" || ep.Address == "" {
		t.Fatalf("endpoint = %+v, want an address and a token", ep)
	}

	// A client that skips the handshake gets nowhere.
	bare, err := DialEndpoint(Endpoint{Transport: TransportTCP, Address: ep.Address})
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()

	err = bare.Call(context.Background(), "echo", map[string]string{"say": "hi"}, nil)
	if e, ok := err.(*Error); !ok || e.Code != CodeUnauthorized {
		t.Fatalf("error = %v, want unauthorized", err)
	}

	// So does one with the wrong token.
	wrong, err := DialEndpoint(Endpoint{Transport: TransportTCP, Address: ep.Address, Token: "nope"})
	if err == nil {
		wrong.Close()
		t.Fatal("a wrong token should be refused at connect time")
	}
}

func TestEndpointFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EndpointFileName)
	want := Endpoint{Transport: TransportTCP, Address: "127.0.0.1:5000", Token: "abc"}
	if err := WriteEndpoint(path, want); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file carries the token, so no other user may read it. Windows has
	// no mode bits to check: there the file inherits the ACL of the root
	// folder, which under the user's profile admits only that user.
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Fatalf("endpoint file mode = %v, want 0600", perm)
	}

	got, err := ReadEndpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	if _, err := ReadEndpoint(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("reading a missing endpoint should fail")
	}
}

func TestDialFileConnects(t *testing.T) {
	_, ep := newServer(t, TransportTCP)
	dir := t.TempDir()
	path := filepath.Join(dir, EndpointFileName)
	if err := WriteEndpoint(path, ep); err != nil {
		t.Fatal(err)
	}

	c, err := DialFileWait(path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(context.Background(), "echo", map[string]string{"say": "x"}, nil); err != nil {
		t.Fatalf("a client that read the endpoint file could not call: %v", err)
	}
}

func TestSecondDaemonCannotStealTheSocket(t *testing.T) {
	srv, ep := newServer(t, TransportUnix)
	_ = srv

	other := NewServer(ep.Path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	other.SetTransport(TransportUnix)
	if err := other.Listen(); err == nil {
		other.Close()
		t.Fatal("a second daemon bound the socket of a running one")
	}
}

// A root folder nested deeply enough cannot host a unix socket at all; the
// daemon must keep working rather than fail with "bind: invalid argument".
func TestALongSocketPathFallsBackToTCP(t *testing.T) {
	deep := filepath.Join(t.TempDir(), strings.Repeat("nested-directory/", 8))
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(deep, "wharf.sock")
	if !SocketPathTooLong(socket) {
		t.Fatalf("test path is only %d bytes; it should exceed the limit", len(socket))
	}

	srv := NewServer(socket, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetTransport(TransportUnix)
	if err := srv.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()

	ep := srv.Endpoint()
	if ep.Transport != TransportTCP || ep.Token == "" {
		t.Fatalf("endpoint = %+v, want a tokenised TCP fallback", ep)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.Handle("ping", func(context.Context, json.RawMessage) (any, error) {
		return map[string]string{"pong": "wharf"}, nil
	})
	go srv.Serve(ctx)

	c, err := DialEndpoint(ep)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call(ctx, "ping", nil, nil); err != nil {
		t.Fatalf("the fallback endpoint does not work: %v", err)
	}
}

func TestShortSocketPathsAreNotFlagged(t *testing.T) {
	if SocketPathTooLong("/Users/someone/Wharf/data/wharf.sock") {
		t.Fatal("an ordinary root folder was rejected")
	}
}

// A client disconnecting while an event is broadcast must not crash the
// daemon: that crash takes down the process that owns every running service,
// leaving them orphaned. Found by a wharfctl call ending during a config
// reload.
func TestBroadcastSurvivesClientsDisconnecting(t *testing.T) {
	srv, ep := newServer(t, TransportUnix)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				srv.Broadcast(EventState, map[string]int{"n": 1})
			}
		}
	}()
	for i := 0; i < 200; i++ {
		c, err := DialEndpoint(ep)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]string
		c.Call(context.Background(), "echo", map[string]string{"say": "hi"}, &out)
		c.Close()
	}
	close(stop)
	<-done
}
