//go:build windows

package launch

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/yx-zero/camoufox-go/juggler"
	"golang.org/x/sys/windows"
)

// startWithJugglerPipe wires the Juggler pipe on Windows. Camoufox does not use
// CRT file descriptors here; instead nsRemoteDebuggingPipe.cpp reads the raw
// inherited HANDLE values from the PW_PIPE_READ / PW_PIPE_WRITE environment
// variables. We therefore: create two anonymous pipes, mark the two browser-side
// endpoints inheritable, pass them through SysProcAttr.AdditionalInheritedHandles
// (which sets bInheritHandles and a restricted handle list), and export their
// numeric handle values in the environment.
func startWithJugglerPipe(cmd *exec.Cmd, baseEnv []string) (*juggler.PipeTransport, error) {
	// to-browser pipe: browser reads toR (PW_PIPE_READ); we write toW.
	toR, toW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("launch: create pipe to browser: %w", err)
	}
	// from-browser pipe: browser writes fromW (PW_PIPE_WRITE); we read fromR.
	fromR, fromW, err := os.Pipe()
	if err != nil {
		toR.Close()
		toW.Close()
		return nil, fmt.Errorf("launch: create pipe from browser: %w", err)
	}

	cleanup := func() {
		toR.Close()
		toW.Close()
		fromR.Close()
		fromW.Close()
	}

	// The browser-side endpoints must be inheritable by the child.
	for _, h := range []*os.File{toR, fromW} {
		if err := windows.SetHandleInformation(
			windows.Handle(h.Fd()),
			windows.HANDLE_FLAG_INHERIT,
			windows.HANDLE_FLAG_INHERIT,
		); err != nil {
			cleanup()
			return nil, fmt.Errorf("launch: make handle inheritable: %w", err)
		}
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(
		cmd.SysProcAttr.AdditionalInheritedHandles,
		syscall.Handle(toR.Fd()), syscall.Handle(fromW.Fd()),
	)

	cmd.Env = append(append([]string(nil), baseEnv...),
		"PW_PIPE_READ="+strconv.FormatUint(uint64(toR.Fd()), 10),
		"PW_PIPE_WRITE="+strconv.FormatUint(uint64(fromW.Fd()), 10),
	)

	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("launch: start browser: %w", err)
	}

	// The child inherited its own references to the browser-side endpoints.
	toR.Close()
	fromW.Close()

	return juggler.NewPipeTransport(fromR, toW), nil
}
