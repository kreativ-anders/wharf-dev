package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// Client is a connection to the daemon. The Flutter GUI speaks this protocol
// directly in Dart; this Go client backs the CLI and the daemon's own tests.
type Client struct {
	conn net.Conn
	enc  *json.Encoder

	mu      sync.Mutex
	nextID  int
	pending map[string]chan frame

	events chan Event

	closeOnce sync.Once
	closed    chan struct{}
	readErr   error
}

// Dial connects to a unix socket by path. Prefer DialEndpoint, which works on
// every transport.
func Dial(path string) (*Client, error) {
	return DialEndpoint(Endpoint{Transport: TransportUnix, Path: path})
}

// DialEndpoint connects using whatever transport the daemon published, and
// authenticates if the transport requires it.
func DialEndpoint(ep Endpoint) (*Client, error) {
	network, address := "unix", ep.Path
	if ep.Transport == TransportTCP {
		network, address = "tcp", ep.Address
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", address, err)
	}
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		pending: map[string]chan frame{},
		events:  make(chan Event, 32),
		closed:  make(chan struct{}),
	}
	go c.readLoop()

	if ep.Token != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Call(ctx, MethodAuth, map[string]string{"token": ep.Token}, nil); err != nil {
			c.Close()
			return nil, fmt.Errorf("authenticate with daemon: %w", err)
		}
	}
	return c, nil
}

// DialFile reads an endpoint file written by the daemon and connects to it.
// This is the path every client should take: it needs to know the root folder
// and nothing else about the transport.
func DialFile(endpointPath string) (*Client, error) {
	ep, err := ReadEndpoint(endpointPath)
	if err != nil {
		return nil, err
	}
	return DialEndpoint(ep)
}

// DialFileWait retries until the daemon has published its endpoint and accepts
// a connection, or the timeout expires.
func DialFileWait(endpointPath string, timeout time.Duration) (*Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := DialFile(endpointPath)
		if err == nil {
			return c, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Events yields pushed events. It is closed when the connection ends.
func (c *Client) Events() <-chan Event { return c.events }

// Call sends a request and decodes the result into out, which may be nil.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	var raw json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = body
	}

	c.mu.Lock()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	ch := make(chan frame, 1)
	c.pending[id] = ch
	err := c.enc.Encode(Request{ID: id, Method: method, Params: raw})
	c.mu.Unlock()
	if err != nil {
		c.forget(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case <-c.closed:
		c.forget(id)
		if c.readErr != nil {
			return c.readErr
		}
		return errors.New("daemon connection closed")
	case f := <-ch:
		if f.Error != nil {
			return f.Error
		}
		if out == nil || len(f.Result) == 0 {
			return nil
		}
		return json.Unmarshal(f.Result, out)
	}
}

// Close ends the connection.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.conn.Close()
	})
	return nil
}

func (c *Client) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	defer func() {
		c.closeOnce.Do(func() { close(c.closed); c.conn.Close() })
		close(c.events)
	}()

	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var f frame
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			continue
		}
		if f.Event != "" {
			select {
			case c.events <- Event{Event: f.Event, Data: f.Data}:
			default: // INFO: A consumer that is not reading events gets the next one.
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[f.ID]
		delete(c.pending, f.ID)
		c.mu.Unlock()
		if ok {
			ch <- f
		}
	}
	c.readErr = sc.Err()
}
