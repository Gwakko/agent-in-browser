// Package rpc is a minimal JSON-over-WebSocket command/event transport shared by
// the Chrome DevTools Protocol (CDP) and WebDriver BiDi backends. Both protocols
// use {id, method, params} commands, id-correlated responses and method-tagged
// events; they differ only in how errors are encoded, which Conn handles.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// Event is an unsolicited protocol message (CDP event or BiDi event).
type Event struct {
	Method    string
	Params    json.RawMessage
	SessionID string // CDP flattened session id; empty for BiDi
}

// Error is a protocol-level error returned by the browser.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ErrClosed is returned for calls on a closed connection.
var ErrClosed = errors.New("rpc: connection closed")

type message struct {
	ID        *int64          `json:"id,omitempty"`
	Type      string          `json:"type,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
	Message   string          `json:"message,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// Conn is a single WebSocket connection to a browser.
type Conn struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan response
	onEvent func(Event)
	closed  chan struct{}
	err     error
}

// Dial opens a WebSocket connection. onEvent is invoked from the read loop and
// must not block or issue calls on the same Conn.
func Dial(ctx context.Context, url string, onEvent func(Event)) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	ws.SetReadLimit(512 << 20) // full-page screenshots can be large
	c := &Conn{
		ws:      ws,
		pending: make(map[int64]chan response),
		onEvent: onEvent,
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Conn) readLoop() {
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.shutdown(err)
			return
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.ID == nil {
			if m.Method != "" && c.onEvent != nil {
				c.onEvent(Event{Method: m.Method, Params: m.Params, SessionID: m.SessionID})
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ok {
			ch <- response{result: m.Result, err: decodeError(m)}
		}
	}
}

func decodeError(m message) error {
	if len(m.Error) == 0 || string(m.Error) == "null" {
		return nil
	}
	// CDP: {"code": -32000, "message": "..."}; BiDi: "no such frame" + "message".
	if m.Error[0] == '{' {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		}
		_ = json.Unmarshal(m.Error, &e)
		msg := e.Message
		if e.Data != "" {
			msg += ": " + e.Data
		}
		return &Error{Code: fmt.Sprint(e.Code), Message: msg}
	}
	var code string
	_ = json.Unmarshal(m.Error, &code)
	return &Error{Code: code, Message: m.Message}
}

func (c *Conn) shutdown(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return
	default:
	}
	c.err = err
	close(c.closed)
	for id, ch := range c.pending {
		ch <- response{err: fmt.Errorf("%w: %v", ErrClosed, err)}
		delete(c.pending, id)
	}
}

// Call sends a command and waits for its response. sessionID is only used by CDP.
func (c *Conn) Call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	if params == nil {
		params = struct{}{}
	}
	out := map[string]any{"id": id, "method": method, "params": params}
	if sessionID != "" {
		out["sessionId"] = sessionID
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}

	ch := make(chan response, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return nil, ErrClosed
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err = c.ws.Write(ctx, websocket.MessageText, data)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("%s: write: %w", method, err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", method, r.err)
		}
		return r.result, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// CallInto is Call plus JSON decoding of the result into out (may be nil).
func (c *Conn) CallInto(ctx context.Context, method string, params any, sessionID string, out any) error {
	raw, err := c.Call(ctx, method, params, sessionID)
	if err != nil || out == nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Done is closed when the connection terminates.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Close closes the WebSocket. The browser keeps running.
func (c *Conn) Close() error {
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	c.shutdown(ErrClosed)
	return err
}
