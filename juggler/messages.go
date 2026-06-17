// Package juggler is a pure-Go client for Playwright's Juggler protocol — the
// automation protocol of the patched Firefox that Camoufox is built on.
//
// It speaks the Juggler wire protocol over the browser's remote-debugging pipe
// (JSON messages terminated by a NUL byte). The transport, message framing and
// JSON-RPC/session core are adapted from VulpineOS/foxbridge (MIT licensed) —
// see THIRD_PARTY_LICENSES.md. The OS-specific spawning of the pipe (FD 3/4 on
// Unix, inherited HANDLEs via PW_PIPE_READ/WRITE on Windows) lives in the
// launch package.
package juggler

import "encoding/json"

// Message is a single Juggler protocol message. Requests carry ID+Method+Params;
// responses carry ID+Result/Error; events carry Method+Params with no ID.
// SessionID routes a message to a particular page/target session ("" = root).
type Message struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

// Error is a Juggler protocol error payload.
type Error struct {
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e.Data != "" {
		return e.Message + ": " + e.Data
	}
	return e.Message
}

// IsEvent reports whether the message is an event (no ID, has Method).
func (m *Message) IsEvent() bool { return m.ID == 0 && m.Method != "" }

// IsResponse reports whether the message is a response (has ID, no Method).
func (m *Message) IsResponse() bool { return m.ID != 0 && m.Method == "" }

// EventHandler receives a Juggler event. sessionID identifies the originating
// page session ("" for browser-level events).
type EventHandler = func(sessionID string, params json.RawMessage)
