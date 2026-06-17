//go:build !windows

package launch

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/yx-zero/camoufox-go/juggler"
)

// startWithJugglerPipe wires the Juggler pipe via inherited file descriptors.
// Camoufox (Unix) reads commands from FD 3 and writes responses to FD 4
// (nsRemoteDebuggingPipe.cpp: readFD=3, writeFD=4). exec.Cmd.ExtraFiles maps
// entry i to the child's FD 3+i, so ExtraFiles[0]->FD3, ExtraFiles[1]->FD4.
func startWithJugglerPipe(cmd *exec.Cmd, baseEnv []string) (*juggler.PipeTransport, error) {
	// to-browser pipe: browser reads toR (FD3); we write toW.
	toR, toW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("launch: create pipe to browser: %w", err)
	}
	// from-browser pipe: browser writes fromW (FD4); we read fromR.
	fromR, fromW, err := os.Pipe()
	if err != nil {
		toR.Close()
		toW.Close()
		return nil, fmt.Errorf("launch: create pipe from browser: %w", err)
	}

	cmd.ExtraFiles = []*os.File{toR, fromW}
	cmd.Env = baseEnv

	if err := cmd.Start(); err != nil {
		toR.Close()
		toW.Close()
		fromR.Close()
		fromW.Close()
		return nil, fmt.Errorf("launch: start browser: %w", err)
	}

	// The child now owns its copies of the browser-side endpoints.
	toR.Close()
	fromW.Close()

	return juggler.NewPipeTransport(fromR, toW), nil
}
