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
	"time"
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
	// background names the methods answered whenever they finish rather than
	// in turn; see HandleBackground.
	background map[string]bool

	transport Transport
	token     string
	endpoint  Endpoint
	// authTimeout is how long a TCP client has to present the token.
	authTimeout time.Duration

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
		path:        socketPath,
		log:         log,
		authTimeout: 10 * time.Second,
		transport:   DefaultTransport(),
		handlers:    map[string]Handler{},
		background:  map[string]bool{},
		clients:     map[*client]struct{}{},
	}
}

// SetTransport overrides the transport, so a TCP endpoint can be exercised on
// a machine that also supports unix sockets.
func (s *Server) SetTransport(t Transport) { s.transport = t }

// Endpoint describes where this server is listening. Valid after Listen.
func (s *Server) Endpoint() Endpoint { return s.endpoint }

// Handle registers a method handler. Must be called before Serve.
func (s *Server) Handle(method string, h Handler) { s.handlers[method] = h }

// HandleBackground registers a handler that runs beside the connection's
// other requests and is answered whenever it finishes. It is for actions that
// take minutes — a download, an install — and must not hold up every click
// that comes after them on the same connection. Must be called before Serve.
func (s *Server) HandleBackground(method string, h Handler) {
	s.handlers[method] = h
	s.background[method] = true
}

// Listen binds the transport for this OS and returns the endpoint clients
// should use.
func (s *Server) Listen() error {
	if s.transport == TransportTCP {
		return s.listenTCP()
	}
	// INFO: A path that will not fit in sun_path cannot be bound at all. Falling
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
	// WARNING: The socket is the daemon's whole authorisation model: only the user who
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
		if !c.authorised {
			// WARNING: Loopback admits every local process. Without a
			// deadline, one that never presents the token could hold any
			// number of the daemon's connections open for good.
			_ = conn.SetReadDeadline(time.Now().Add(s.authTimeout))
		}
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
		// WARNING: A client that is disconnecting has closed its queue but is still
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

	c.gone = make(chan struct{})
	go func() {
		defer close(c.gone)
		for line := range c.out {
			if _, err := c.conn.Write(line); err != nil {
				return
			}
		}
	}()
	defer func() {
		// WARNING: A background handler still sends its answer on the queue;
		// closing it first would make that send panic.
		c.inflight.Wait()
		c.closeOut()
		<-c.gone
	}()

	dec := bufio.NewScanner(c.conn)
	// INFO: Requests are small; a project name is the largest field.
	dec.Buffer(make([]byte, 0, 4096), 1<<20)
	for dec.Scan() {
		var req Request
		if err := json.Unmarshal(dec.Bytes(), &req); err != nil {
			c.send(errorResponse("", CodeBadRequest, "malformed request: "+err.Error()))
			continue
		}
		if !s.dispatch(ctx, c, req) {
			return
		}
	}
	if err := dec.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.log.Debug("client read ended", "err", err)
	}
}

// dispatch runs one request and reports whether the connection may stay
// open. Requests are handled in order on the connection — starting a service
// then reading state must not race — except those registered with
// HandleBackground, which run beside them.
func (s *Server) dispatch(ctx context.Context, c *client, req Request) bool {
	if s.token != "" && !c.authorised {
		return s.authorise(c, req)
	}
	if req.Method == MethodAuth {
		// INFO: Already authorised, or on a transport that needs no token.
		c.send(Response{ID: req.ID, OK: true, Result: json.RawMessage(`{"ok":true}`)})
		return true
	}

	h, ok := s.handlers[req.Method]
	if !ok {
		c.send(errorResponse(req.ID, CodeUnknownMethod, "unknown method "+req.Method))
		return true
	}
	if s.background[req.Method] {
		c.inflight.Add(1)
		go func() {
			defer c.inflight.Done()
			s.answer(ctx, c, req, h)
		}()
		return true
	}
	s.answer(ctx, c, req, h)
	return true
}

// answer runs one handler and sends its response.
func (s *Server) answer(ctx context.Context, c *client, req Request, h Handler) {
	result, err := h(ctx, req.Params)
	if err != nil {
		var ipcErr *Error
		if errors.As(err, &ipcErr) {
			// INFO: A coded error is an answer, not a fault: a project that
			// does not exist is the user's typo, not the daemon's problem.
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

// authorise handles a TCP client's requests until it has presented the
// token, and reports whether the connection may stay open.
func (s *Server) authorise(c *client, req Request) bool {
	if req.Method != MethodAuth {
		c.send(errorResponse(req.ID, CodeUnauthorized, "authenticate first: call "+MethodAuth+" with the token from "+EndpointFileName))
		return true
	}
	var p struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || subtle.ConstantTimeCompare([]byte(p.Token), []byte(s.token)) != 1 {
		c.send(errorResponse(req.ID, CodeUnauthorized, "invalid token"))
		// WARNING: A wrong token ends the connection, so no local process
		// can try one token after another on it.
		return false
	}
	c.authorised = true
	_ = c.conn.SetReadDeadline(time.Time{})
	c.send(Response{ID: req.ID, OK: true, Result: json.RawMessage(`{"ok":true}`)})
	return true
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
	// inflight counts background handlers that have not sent their answer.
	inflight sync.WaitGroup
	// gone is closed when the connection's writer has stopped, and nothing
	// queued will be written any more.
	gone chan struct{}
}

// send queues a response, waiting for room if events have filled the queue.
// Unlike an event, a response is never dropped: its caller is waiting for
// that one answer, and without it the call — a click in the GUI — hangs for
// good. It runs on the connection's own goroutine, the one that later closes
// the queue, or in a background handler that goroutine waits for before it
// does, so it cannot send on a closed queue.
func (c *client) send(resp Response) {
	line, err := json.Marshal(resp)
	if err != nil {
		return
	}
	line = append(line, '\n')
	select {
	case c.out <- line:
	case <-c.gone:
	}
}

// push queues an event unless the client is closed or its queue is full. It
// is how every goroutine but the connection's own sends to a client, so none
// of them can send on the queue after closeOut.
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
