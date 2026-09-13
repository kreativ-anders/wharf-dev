package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Transport is how a client reaches the daemon.
type Transport string

const (
	// TransportUnix is a unix domain socket: the default everywhere it works.
	// Filesystem permissions are the whole authorisation model.
	TransportUnix Transport = "unix"
	// TransportTCP is loopback TCP with a shared token, used on Windows.
	//
	// dev/architecture.md §4 assumed AF_UNIX everywhere because Windows 10
	// 1803+ supports it. The OS does; Dart does not — dart:io offers unix
	// sockets on Linux, macOS and Android only, so a Flutter GUI cannot open
	// one on Windows. The daemon therefore listens on loopback there instead.
	// Everything above the transport is unchanged: same protocol, same
	// methods, same events.
	TransportTCP Transport = "tcp"
)

// Endpoint tells a client where the daemon is listening and, on TCP, how to
// authenticate. It is written to <root>/data/wharf.endpoint on start, so no
// client has to guess a port or hard-code a socket path.
type Endpoint struct {
	Transport Transport `json:"transport"`
	// Path is the socket path for the unix transport.
	Path string `json:"path,omitempty"`
	// Address is host:port for the TCP transport.
	Address string `json:"address,omitempty"`
	// Token authorises a TCP connection. Empty for unix, where the socket's
	// own file permissions do the job.
	Token string `json:"token,omitempty"`
}

// EndpointFileName is the file a client reads to find the daemon.
const EndpointFileName = "wharf.endpoint"

// DefaultTransport is the transport for the OS the binary was built for.
func DefaultTransport() Transport {
	if runtime.GOOS == "windows" {
		return TransportTCP
	}
	return TransportUnix
}

// maxSocketPath is the size of sockaddr_un.sun_path, which no unix socket path
// may exceed. It is a hard kernel limit, not a convention: 104 bytes on the
// BSDs and macOS, 108 on Linux.
func maxSocketPath() int {
	if runtime.GOOS == "linux" {
		return 108
	}
	return 104
}

// SocketPathTooLong reports whether a path cannot be bound as a unix socket.
// A root folder nested deeply enough — a long home directory, a temp dir in a
// CI runner — makes this reachable in practice, and the failure it causes
// ("bind: invalid argument") explains nothing.
func SocketPathTooLong(path string) bool {
	return len(path)+1 > maxSocketPath()
}

// NewToken returns a random authorisation token.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// WriteEndpoint saves the endpoint description. The file carries the token, so
// it is written owner-only: on Windows it is the only thing standing between
// the daemon and any local process.
func WriteEndpoint(path string, ep Endpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	// WARNING: os.WriteFile applies the mode only to a file it creates. A
	// stale endpoint file with a wider mode — restored from a backup, copied
	// by hand — would keep it and show the token to every local user, so the
	// old file goes first and the chmod covers a removal Windows refused.
	_ = os.Remove(path)
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// ReadEndpoint loads an endpoint description written by the daemon.
func ReadEndpoint(path string) (Endpoint, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, fmt.Errorf("read endpoint %s: %w", path, err)
	}
	var ep Endpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return Endpoint{}, fmt.Errorf("parse endpoint %s: %w", path, err)
	}
	if ep.Transport == "" {
		return Endpoint{}, fmt.Errorf("endpoint %s names no transport", path)
	}
	return ep, nil
}
