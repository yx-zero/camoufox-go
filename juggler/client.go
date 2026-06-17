package juggler

// Adapted from VulpineOS/foxbridge pkg/backend/juggler/client.go (MIT).
// Changes: dependency on foxbridge's backend package removed; added fire-and-
// forget Send (for Browser.close), a Closed() disconnect signal, any-typed
// params, and an optional logger instead of always logging unhandled events.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultCallTimeout bounds a Call when its context has no deadline.
const DefaultCallTimeout = 30 * time.Second

// Logger is the minimal logging hook used for unhandled events when debugging.
type Logger interface {
	Printf(format string, args ...any)
}

// Client is a Juggler JSON-RPC client over a Transport. It correlates responses
// by message ID and dispatches events to subscribed handlers. It is safe for
// concurrent use.
type Client struct {
	transport Transport
	nextID    atomic.Int64

	pending   map[int]chan *Message
	pendingMu sync.Mutex

	handlers  map[string][]EventHandler
	handlerMu sync.RWMutex

	done      chan struct{}
	closeOnce sync.Once

	// Log, if set, records unhandled events and read-loop termination.
	Log Logger
}

// NewClient starts a client on the given transport and begins reading messages.
func NewClient(transport Transport) *Client {
	c := &Client{
		transport: transport,
		pending:   make(map[int]chan *Message),
		handlers:  make(map[string][]EventHandler),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Call sends method on sessionID and waits for its response. params may be nil,
// a json.RawMessage, or any JSON-marshalable value. If ctx has no deadline a
// DefaultCallTimeout is applied.
func (c *Client) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	raw, err := toRaw(params)
	if err != nil {
		return nil, fmt.Errorf("juggler: marshal params for %s: %w", method, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultCallTimeout)
		defer cancel()
	}

	id := int(c.nextID.Add(1))
	msg := &Message{ID: id, Method: method, Params: raw, SessionID: sessionID}

	ch := make(chan *Message, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.transport.Send(msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("juggler: %s: %w", method, resp.Error)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("juggler: call %s: %w", method, ctx.Err())
	case <-c.done:
		return nil, fmt.Errorf("juggler: call %s: client closed", method)
	}
}

// Send dispatches a request without waiting for a response. This is used for
// Browser.close, whose response may never arrive because the browser tears down
// the pipe as it shuts down.
func (c *Client) Send(sessionID, method string, params any) error {
	raw, err := toRaw(params)
	if err != nil {
		return fmt.Errorf("juggler: marshal params for %s: %w", method, err)
	}
	id := int(c.nextID.Add(1))
	return c.transport.Send(&Message{ID: id, Method: method, Params: raw, SessionID: sessionID})
}

// Subscribe registers a handler for a Juggler event name. Multiple handlers may
// subscribe to the same event.
func (c *Client) Subscribe(event string, handler EventHandler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[event] = append(c.handlers[event], handler)
}

// Closed returns a channel closed when the client disconnects (explicit Close or
// a transport read error, e.g. the browser exiting).
func (c *Client) Closed() <-chan struct{} { return c.done }

// Close shuts down the client and its transport.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return c.transport.Close()
}

func (c *Client) readLoop() {
	for {
		msg, err := c.transport.Receive()
		if err != nil {
			select {
			case <-c.done:
			default:
				if c.Log != nil {
					c.Log.Printf("juggler: read loop ended: %v", err)
				}
				c.closeOnce.Do(func() { close(c.done) })
			}
			return
		}

		switch {
		case msg.IsResponse():
			c.pendingMu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- msg
			}
		case msg.IsEvent():
			c.handlerMu.RLock()
			handlers := c.handlers[msg.Method]
			c.handlerMu.RUnlock()
			if len(handlers) == 0 && c.Log != nil {
				c.Log.Printf("juggler: unhandled event %s (session=%s)", msg.Method, msg.SessionID)
			}
			for _, h := range handlers {
				h(msg.SessionID, msg.Params)
			}
		}
	}
}

// toRaw normalizes params into a json.RawMessage. nil yields nil (the field is
// omitted on the wire).
func toRaw(params any) (json.RawMessage, error) {
	switch p := params.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return p, nil
	case []byte:
		return p, nil
	default:
		return json.Marshal(p)
	}
}
