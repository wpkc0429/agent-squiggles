// Package lsp implements the small subset of the Language Server Protocol
// that agent-squiggles needs: starting a server, syncing documents, and
// collecting diagnostics.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ResponseError is a JSON-RPC error object.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Handler receives messages initiated by the server.
type Handler interface {
	// Notify handles a server notification. It runs on the read loop, so
	// it must not block.
	Notify(method string, params json.RawMessage)
	// Request handles a server-to-client request and returns its result.
	Request(method string, params json.RawMessage) (any, *ResponseError)
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type response struct {
	result json.RawMessage
	err    error
}

// ErrClosed is returned for calls on a connection whose peer went away.
var ErrClosed = errors.New("lsp: connection closed")

// Conn is a JSON-RPC 2.0 connection using the LSP base protocol framing
// (Content-Length headers).
type Conn struct {
	w   io.Writer
	wmu sync.Mutex
	r   *bufio.Reader

	handler Handler
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan response
	done    chan struct{}
	err     error
}

// NewConn wraps a reader/writer pair. Call Run to start processing
// incoming messages.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	return &Conn{
		w:       w,
		r:       bufio.NewReaderSize(r, 64*1024),
		handler: h,
		pending: make(map[int64]chan response),
		done:    make(chan struct{}),
	}
}

// Done is closed when the read loop exits.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err reports why the read loop exited.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Run reads messages until the stream ends. It returns the terminal error.
func (c *Conn) Run() error {
	var err error
	for {
		var msg *message
		msg, err = c.read()
		if err != nil {
			break
		}
		c.dispatch(msg)
	}
	c.mu.Lock()
	c.err = err
	pending := c.pending
	c.pending = map[int64]chan response{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: ErrClosed}
	}
	close(c.done)
	return err
}

func (c *Conn) dispatch(msg *message) {
	switch {
	case msg.Method != "" && len(msg.ID) > 0:
		var result any
		var rerr *ResponseError
		if c.handler != nil {
			result, rerr = c.handler.Request(msg.Method, msg.Params)
		} else {
			rerr = &ResponseError{Code: -32601, Message: "method not found"}
		}
		_ = c.reply(msg.ID, result, rerr)
	case msg.Method != "":
		if c.handler != nil {
			c.handler.Notify(msg.Method, msg.Params)
		}
	case len(msg.ID) > 0:
		id, err := strconv.ParseInt(strings.Trim(string(msg.ID), `"`), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if !ok {
			return
		}
		if msg.Error != nil {
			ch <- response{err: msg.Error}
		} else {
			ch <- response{result: msg.Result}
		}
	}
}

// Call sends a request and decodes the result into out (which may be nil).
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil || isClosed(c.done) {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	if err := c.write(body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.result) > 0 && string(r.result) != "null" {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		_ = c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	return c.write(body)
}

func (c *Conn) reply(id json.RawMessage, result any, rerr *ResponseError) error {
	body := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		body["error"] = rerr
	} else {
		// JSON-RPC requires "result" to be present on success, even if null.
		body["result"] = result
	}
	return c.write(body)
}

func (c *Conn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.w.Write(data)
	return err
}

func (c *Conn) read() (*message, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", value)
			}
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	var msg message
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, fmt.Errorf("lsp: decode message: %w", err)
	}
	return &msg, nil
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
