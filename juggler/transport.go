package juggler

// Adapted from VulpineOS/foxbridge pkg/backend/juggler/transport.go (MIT).

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Transport sends and receives framed Juggler messages.
type Transport interface {
	Send(msg *Message) error
	Receive() (*Message, error)
	Close() error
}

// PipeTransport speaks Juggler over a pair of OS pipe endpoints. Each message is
// JSON terminated by a NUL byte (\x00), matching nsRemoteDebuggingPipe's framing.
// The endpoints are provided by the launcher, which wires them to the browser
// in an OS-appropriate way (Unix FD 3/4, Windows inherited HANDLEs).
type PipeTransport struct {
	reader  *bufio.Reader
	writer  io.WriteCloser
	readFD  *os.File
	writeFD *os.File
	writeMu sync.Mutex
}

// NewPipeTransport builds a transport that reads browser→client messages from
// readFD and writes client→browser messages to writeFD.
func NewPipeTransport(readFD, writeFD *os.File) *PipeTransport {
	return &PipeTransport{
		reader:  bufio.NewReaderSize(readFD, 1<<16),
		writer:  writeFD,
		readFD:  readFD,
		writeFD: writeFD,
	}
}

// Send marshals msg to JSON and writes it followed by a NUL byte.
func (t *PipeTransport) Send(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("juggler: marshal message: %w", err)
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := t.writer.Write(append(data, 0)); err != nil {
		return fmt.Errorf("juggler: write message: %w", err)
	}
	return nil
}

// Receive reads up to the next NUL byte and unmarshals the JSON message.
func (t *PipeTransport) Receive() (*Message, error) {
	data, err := t.reader.ReadBytes(0)
	if err != nil {
		return nil, fmt.Errorf("juggler: read message: %w", err)
	}
	if len(data) > 0 {
		data = data[:len(data)-1] // strip NUL
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("juggler: unmarshal message: %w", err)
	}
	return &msg, nil
}

// Close closes both pipe endpoints.
func (t *PipeTransport) Close() error {
	return errors.Join(t.readFD.Close(), t.writeFD.Close())
}
