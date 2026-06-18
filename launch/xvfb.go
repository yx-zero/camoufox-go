package launch

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Xvfb is a spawned virtual X display (Linux only). It lets Camoufox run in a
// real (non-headless) rendering mode without a physical display — sometimes more
// undetectable than the engine's headless mode.
type Xvfb struct {
	cmd     *exec.Cmd
	Display string // e.g. ":99"
}

// xvfbArgs mirror camoufox/pythonlib/camoufox/virtdisplay.py.
var xvfbArgs = []string{
	"-screen", "0", "1x1x24",
	"-ac",
	"-nolisten", "tcp",
	"-extension", "RENDER",
	"+extension", "GLX",
	"-extension", "COMPOSITE",
	"-extension", "XVideo",
	"-extension", "XVideo-MotionCompensation",
	"-extension", "XINERAMA",
	"-fp", "built-ins",
	"-nocursor",
	"-br",
}

// StartXvfb launches Xvfb with -displayfd so it picks a free display number
// atomically and reports it back, returning once the display is ready.
func StartXvfb(debug bool) (*Xvfb, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("launch: virtual display is only supported on Linux")
	}
	path, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, fmt.Errorf("launch: Xvfb not found (install it to use a virtual display): %w", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	// ExtraFiles[0] is fd 3 in the child; tell Xvfb to write the display there.
	args := append([]string{"-displayfd", "3"}, xvfbArgs...)
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = []*os.File{w}
	cmd.Env = append(os.Environ(),
		"__GLX_VENDOR_LIBRARY_NAME=mesa",
		"LIBGL_ALWAYS_SOFTWARE=1",
	)
	if debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		w.Close()
		return nil, fmt.Errorf("launch: start Xvfb: %w", err)
	}
	w.Close() // close our copy so the read end EOFs if Xvfb dies

	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			ch <- result{0, err}
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		ch <- result{n, err}
	}()

	select {
	case got := <-ch:
		if got.err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("launch: Xvfb display read: %w", got.err)
		}
		return &Xvfb{cmd: cmd, Display: fmt.Sprintf(":%d", got.n)}, nil
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("launch: Xvfb did not report a display within 10s")
	}
}

// Kill terminates the Xvfb process.
func (x *Xvfb) Kill() {
	if x == nil || x.cmd == nil || x.cmd.Process == nil {
		return
	}
	_ = x.cmd.Process.Kill()
	_, _ = x.cmd.Process.Wait()
}
