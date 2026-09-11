package ipc

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Handler serves one method. Returning an *Error sends that code to the
// client; any other error is reported as CodeInternal.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server accepts GUI connections. The transport is a unix socket everywhere
// dart:io supports one, and loopback TCP with a token on Windows, where it
// does not (see endpoint.go).
type Server struct {
	path     string
	log      *slog.Logger
	handlers map[string]Handler

	transport Transport
	token     string
	endpoint  Endpoint

	ln net.Listener

	mu      sync.Mutex
	clients map[*client]struct{}
	closed  bool
}

// NewServer returns a server bound to nothing yet; register handlers, then
// call Serve.
func NewServer(socketPath string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		path:      socketPath,
		log:       log,
		transport: DefaultTransport(),
		handlers:  map[string]Handler{},
		clients:   map[*client]struct{}{},
	}
}

// SetTransport overrides the transport, so a TCP endpoint can be exercised on
// a machine that also supports unix sockets.
func (s *Server) SetTransport(t Transport) { s.transport = t }

// Endpoint describes where this server is listening. Valid after Listen.
func (s *Server) Endpoint() Endpoint { return s.endpoint }

// Handle registers a method handler. Must be called before Serve.
func (s *Server) Handle(method string, h Handler) { s.handlers[method] = h }

// Listen binds the transport for this OS and returns the endpoint clients
// should use.
func (s *Server) Listen() error {
	if s.transport == TransportTCP {
		return s.listenTCP()
	}
	// A path that will not fit in sun_path cannot be bound at all. Falling
	// back to the transport Windows already uses keeps the daemon working;
	// clients read the endpoint file and never notice.
	if SocketPathTooLong(s.path) {
		s.log.Warn("socket path is too long for a unix socket; using loopback TCP instead",
			"path", s.path, "limit", maxSocketPath())
		return s.listenTCP()
	}
	return s.listenUnix()
}

// listenUnix binds the unix socket. A stale socket from a crashed daemon is
// removed first, but only after checking that nothing is listening on it —
// otherwise a second daemon would silently steal the first one's endpoint.
func (s *Server) listenUnix() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.path); err == nil {
		conn, derr := net.Dial("unix", s.path)
		if derr == nil {
			conn.Close()
			return fmt.Errorf("another daemon is already listening on %s", s.path)
		}
		if err := os.Remove(s.path); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.path, err)
	}
	// The socket is the daemon's whole authorisation model: only the user who
	// owns the root folder may talk to it.
	if err := os.Chmod(s.path, 0o600); err != nil {
		ln.Close()
		return err
	}
	s.ln = ln
	s.endpoint = Endpoint{Transport: TransportUnix, Path: s.path}
	return nil
}

// listenTCP binds loopback on an OS-assigned port and mints a token. Loopback
// alone is not authorisation — any local process can connect — so the token,
// readable only by the owner of the endpoint file, is what authorises.
func (s *Server) listenTCP() error {
	token, err := NewToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	s.ln = ln
	s.token = token
	s.endpoint = Endpoint{Transport: TransportTCP, Address: ln.Addr().String(), Token: token}
	return nil
}

// Serve accepts connections until ctx is cancelled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		s.Close()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		c := &client{conn: conn, out: make(chan []byte, 32), log: s.log, authorised: s.token == ""}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		go s.serveClient(ctx, c)
	}
}

// Broadcast pushes an event to every connected client. A client that is not
// keeping up drops the event rather than stalling the daemon; the next event
// carries a full snapshot anyway.
func (s *Server) Broadcast(event string, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		s.log.Error("marshal event", "event", event, "err", err)
		return
	}
	line, err := json.Marshal(Event{Event: event, Data: body})
	if err != nil {
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		// A client that is disconnecting has closed its queue but is still
		// listed until its handler returns; push checks, a bare send would
		// panic and take the daemon — and every service it manages — down.
		if !c.push(line) {
			s.log.Warn("dropping event for slow client", "event", event)
		}
	}
}

// Close stops the listener, disconnects clients and removes the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()

	if s.ln != nil {
		s.ln.Close()
	}
	for _, c := range clients {
		c.conn.Close()
	}
	if s.endpoint.Transport == TransportUnix {
		os.Remove(s.path)
	}
	return nil
}

func (s *Server) serveClient(ctx context.Context, c *client) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		c.conn.Close()
	}()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for line := range c.out {
			if _, err := c.conn.Write(line); err != nil {
				return
			}
		}
	}()
	defer func() {
		c.closeOut()
		<-writerDone
	}()

	dec := bufio.NewScanner(c.conn)
	// Requests are small; a project name is the largest field.
	dec.Buffer(make([]byte, 0, 4096), 1<<20)
	for dec.Scan() {
		var req Request
		if err := json.Unmarshal(dec.Bytes(), &req); err != nil {
			c.send(errorResponse("", CodeBadRequest, "malformed request: "+err.Error()))
			continue
		}
		s.dispatch(ctx, c, req)
	}
	if err := dec.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.log.Debug("client read ended", "err", err)
	}
}

// dispatch runs one request. Requests are handled in order on the connection:
// starting a service then reading state must not race, and a single GUI has no
// use for concurrent calls.
func (s *Server) dispatch(ctx context.Context, c *client, req Request) {
	if s.token != "" && !c.authorised {
		if req.Method != MethodAuth {
			c.send(errorResponse(req.ID, CodeUnauthorized, "authenticate first: call "+MethodAuth+" with the token from "+EndpointFileName))
			return
		}
		var p struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || subtle.ConstantTimeCompare([]byte(p.Token), []byte(s.token)) != 1 {
			c.send(errorResponse(req.ID, CodeUnauthorized, "invalid token"))
			return
		}
		c.authorised = true
		c.send(Response{ID: req.ID, OK: true, Result: json.RawMessage(`{"ok":true}`)})
		return
	}
	if req.Method == MethodAuth {
		// Already authorised, or on a transport that needs no token.
		c.send(Response{ID: req.ID, OK: true, Result: json.RawMessage(`{"ok":true}`)})
		return
	}

	h, ok := s.handlers[req.Method]
	if !ok {
		c.send(errorResponse(req.ID, CodeUnknownMethod, "unknown method "+req.Method))
		return
	}

	result, err := h(ctx, req.Params)
	if err != nil {
		var ipcErr *Error
		if errors.As(err, &ipcErr) {
			// A coded error is an answer, not a fault: a project that does not
			// exist is the user's typo, not the daemon's problem.
			s.log.Debug("request refused", "method", req.Method, "code", ipcErr.Code)
			c.send(Response{ID: req.ID, OK: false, Error: ipcErr})
			return
		}
		s.log.Error("handler failed", "method", req.Method, "err", err)
		c.send(errorResponse(req.ID, CodeInternal, err.Error()))
		return
	}

	body, err := json.Marshal(result)
	if err != nil {
		c.send(errorResponse(req.ID, CodeInternal, "encode result: "+err.Error()))
		return
	}
	c.send(Response{ID: req.ID, OK: true, Result: body})
}

type client struct {
	conn net.Conn
	out  chan []byte
	log  *slog.Logger
	// authorised is true from the start on a unix socket; a TCP client must
	// present the token first.
	authorised bool

	mu     sync.Mutex
	closed bool
}

func (c *client) send(resp Response) {
	line, err := json.Marshal(resp)
	if err != nil {
		return
	}
	line = append(line, '\n')
	if !c.push(line) {
		c.log.Warn("dropping response for slow client", "id", resp.ID)
	}
}

// push queues a line unless the client is closed or its queue is full. It is
// the only way anything is sent to a client, so nothing can send on the queue
// after closeOut.
func (c *client) push(line []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return true
	}
	select {
	case c.out <- line:
		return true
	default:
		return false
	}
}

func (c *client) closeOut() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.out)
	}
}

func errorResponse(id, code, msg string) Response {
	return Response{ID: id, OK: false, Error: &Error{Code: code, Message: msg}}
}

// Errorf builds an *Error for a handler to return.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
