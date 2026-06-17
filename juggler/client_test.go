package juggler

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// mockTransport is an in-memory Transport for tests.
type mockTransport struct {
	sent   chan *Message
	recv   chan *Message
	closed chan struct{}
	once   sync.Once
}

func newMock() *mockTransport {
	return &mockTransport{
		sent:   make(chan *Message, 16),
		recv:   make(chan *Message, 16),
		closed: make(chan struct{}),
	}
}
func (m *mockTransport) Send(msg *Message) error { m.sent <- msg; return nil }
func (m *mockTransport) Receive() (*Message, error) {
	select {
	case msg := <-m.recv:
		return msg, nil
	case <-m.closed:
		return nil, io.EOF
	}
}
func (m *mockTransport) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}

func TestClientCallResponse(t *testing.T) {
	mt := newMock()
	c := NewClient(mt)
	defer c.Close()

	// Auto-responder: echo each request as a successful response.
	go func() {
		for {
			select {
			case req := <-mt.sent:
				mt.recv <- &Message{ID: req.ID, Result: json.RawMessage(`{"ok":true}`)}
			case <-mt.closed:
				return
			}
		}
	}()

	res, err := c.Call(context.Background(), "sess1", "Page.navigate", map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ OK bool }
	if err := json.Unmarshal(res, &got); err != nil || !got.OK {
		t.Fatalf("unexpected result %s (err %v)", res, err)
	}
}

func TestClientCallError(t *testing.T) {
	mt := newMock()
	c := NewClient(mt)
	defer c.Close()
	go func() {
		req := <-mt.sent
		mt.recv <- &Message{ID: req.ID, Error: &Error{Message: "boom", Data: "details"}}
	}()
	_, err := c.Call(context.Background(), "", "X.y", nil)
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestClientEventDispatch(t *testing.T) {
	mt := newMock()
	c := NewClient(mt)
	defer c.Close()

	got := make(chan string, 1)
	c.Subscribe("Page.eventFired", func(sessionID string, params json.RawMessage) {
		got <- sessionID
	})
	mt.recv <- &Message{Method: "Page.eventFired", SessionID: "s7", Params: json.RawMessage(`{}`)}

	select {
	case sid := <-got:
		if sid != "s7" {
			t.Fatalf("session = %q", sid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event handler not called")
	}
}

func TestClientClosedSignal(t *testing.T) {
	mt := newMock()
	c := NewClient(mt)
	mt.Close() // simulate browser exit -> read loop sees EOF
	select {
	case <-c.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("Closed() not signaled on transport EOF")
	}
}

// TestPipeTransportFraming exercises NUL framing over a real OS pipe.
func TestPipeTransportFraming(t *testing.T) {
	// client writes to w1 (browser reads); browser writes to w2 (client reads).
	r1, w1, _ := os.Pipe()
	r2, w2, _ := os.Pipe()
	defer func() { r1.Close(); w1.Close(); r2.Close(); w2.Close() }()

	tr := NewPipeTransport(r2, w1)

	// Verify Send writes JSON + NUL into w1 (readable from r1).
	go func() {
		_ = tr.Send(&Message{ID: 5, Method: "Browser.enable"})
	}()
	buf := make([]byte, 256)
	n, err := r1.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if buf[n-1] != 0 {
		t.Fatalf("message not NUL-terminated: %q", buf[:n])
	}
	var sent Message
	if err := json.Unmarshal(buf[:n-1], &sent); err != nil || sent.Method != "Browser.enable" {
		t.Fatalf("bad framed message: %q err=%v", buf[:n-1], err)
	}

	// Verify Receive parses a NUL-terminated message from w2.
	go func() {
		w2.Write([]byte(`{"id":5,"result":{"v":1}}` + "\x00"))
	}()
	msg, err := tr.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if !msg.IsResponse() || msg.ID != 5 {
		t.Fatalf("unexpected received message: %+v", msg)
	}
}
