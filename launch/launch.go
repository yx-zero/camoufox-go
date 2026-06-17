// Package launch spawns the Camoufox browser process and connects to its
// Juggler remote-debugging pipe, returning a ready juggler.Client.
//
// The OS-specific pipe wiring lives in pipe_unix.go / pipe_windows.go:
//   - Unix: the two browser-side endpoints are inherited as file descriptors
//     3 and 4 via exec.Cmd.ExtraFiles.
//   - Windows: Camoufox reads the endpoint HANDLE values from the PW_PIPE_READ /
//     PW_PIPE_WRITE environment variables (see nsRemoteDebuggingPipe.cpp), so we
//     mark those handles inheritable, pass them via
//     SysProcAttr.AdditionalInheritedHandles, and export their numeric values.
//
// In both cases the fingerprint is injected through the CAMOU_CONFIG_* env vars,
// which the browser binary applies at startup — identical to the official lib.
package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yx-zero/camoufox-go/config"
	"github.com/yx-zero/camoufox-go/juggler"
)

// Options configure a Camoufox launch.
type Options struct {
	// ExecutablePath is the Camoufox binary (required). Obtain it from the fetch
	// package.
	ExecutablePath string
	// Config is the flattened fingerprint/property map injected via CAMOU_CONFIG.
	Config config.Map
	// Headless runs without a visible window (recommended; Camoufox's headless
	// mode is engine-patched to be undetectable).
	Headless bool
	// Args are extra command-line arguments appended after the defaults.
	Args []string
	// Env are extra environment entries ("KEY=VALUE") merged over the inherited
	// environment.
	Env []string
	// UserPrefs are Firefox preferences written into the profile's user.js.
	UserPrefs map[string]any
	// ProfileDir, when set, is used as the profile directory; otherwise a
	// temporary one is created and removed on Stop.
	ProfileDir string
	// StartupTimeout bounds how long Start waits for the browser to come up
	// (default 60s).
	StartupTimeout time.Duration
	// Debug routes the browser's stderr to the parent's stderr for diagnostics.
	Debug bool
}

// Process is a running Camoufox instance and its Juggler client.
type Process struct {
	cmd        *exec.Cmd
	client     *juggler.Client
	profileDir string
	ownProfile bool

	mu     sync.Mutex
	stopped bool
}

// Start launches Camoufox and returns once the Juggler pipe is connected.
func Start(opts Options) (*Process, error) {
	if opts.ExecutablePath == "" {
		return nil, fmt.Errorf("launch: ExecutablePath is required")
	}
	if _, err := os.Stat(opts.ExecutablePath); err != nil {
		return nil, fmt.Errorf("launch: executable not found: %w", err)
	}

	profileDir := opts.ProfileDir
	ownProfile := false
	if profileDir == "" {
		dir, err := os.MkdirTemp("", "camoufox-profile-*")
		if err != nil {
			return nil, fmt.Errorf("launch: create profile dir: %w", err)
		}
		profileDir = dir
		ownProfile = true
	}
	if err := writeUserJS(profileDir, opts.UserPrefs); err != nil {
		if ownProfile {
			os.RemoveAll(profileDir)
		}
		return nil, err
	}

	args := []string{"-no-remote", "-juggler-pipe", "-profile", profileDir}
	if opts.Headless {
		args = append(args, "-headless")
	}
	args = append(args, opts.Args...)

	cmd := exec.Command(opts.ExecutablePath, args...)
	// Discard browser stdio (Camoufox is noisy); the pipe is a separate channel.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if opts.Debug {
		cmd.Stderr = os.Stderr
	}

	baseEnv, err := buildEnv(opts)
	if err != nil {
		if ownProfile {
			os.RemoveAll(profileDir)
		}
		return nil, err
	}

	transport, err := startWithJugglerPipe(cmd, baseEnv)
	if err != nil {
		if ownProfile {
			os.RemoveAll(profileDir)
		}
		return nil, err
	}

	client := juggler.NewClient(transport)
	p := &Process{cmd: cmd, client: client, profileDir: profileDir, ownProfile: ownProfile}
	return p, nil
}

// buildEnv assembles the child environment: inherited env + CAMOU_CONFIG_*
// chunks (host-OS chunk sizing) + caller extras.
func buildEnv(opts Options) ([]string, error) {
	env := os.Environ()
	camou, err := config.EnvVars(opts.Config, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("launch: build CAMOU_CONFIG: %w", err)
	}
	env = append(env, camou...)
	env = append(env, opts.Env...)
	return env, nil
}

// Client returns the connected Juggler client.
func (p *Process) Client() *juggler.Client { return p.client }

// PID returns the browser process id, or 0.
func (p *Process) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Closed returns a channel closed when the Juggler connection drops (e.g. the
// browser exits).
func (p *Process) Closed() <-chan struct{} { return p.client.Closed() }

// Stop gracefully shuts the browser down: it sends Browser.close, waits briefly
// for exit, then kills if necessary, and removes a temporary profile.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return nil
	}
	p.stopped = true

	// Fire-and-forget graceful close; the browser may tear down the pipe before
	// replying, so we do not wait for a response.
	_ = p.client.Send("", "Browser.close", nil)

	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-done
	}
	_ = p.client.Close()

	if p.ownProfile {
		os.RemoveAll(p.profileDir)
	}
	return nil
}

// writeUserJS writes the profile's user.js with sane automation defaults merged
// with caller-provided preferences.
func writeUserJS(profileDir string, prefs map[string]any) error {
	merged := map[string]any{
		// Avoid first-run noise, update checks and default-browser nags that
		// could interfere with a clean automation session.
		"browser.shell.checkDefaultBrowser":              false,
		"browser.startup.homepage_override.mstone":       "ignore",
		"browser.startup.page":                           0,
		"datareporting.policy.dataSubmissionEnabled":     false,
		"datareporting.healthreport.uploadEnabled":       false,
		"toolkit.telemetry.enabled":                      false,
		"app.update.enabled":                             false,
		"app.update.auto":                                false,
		"extensions.update.enabled":                      false,
		"browser.aboutConfig.showWarning":                false,
		"browser.tabs.warnOnClose":                       false,
	}
	for k, v := range prefs {
		merged[k] = v
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		val, err := json.Marshal(merged[k])
		if err != nil {
			return fmt.Errorf("launch: marshal pref %q: %w", k, err)
		}
		fmt.Fprintf(&sb, "user_pref(%q, %s);\n", k, val)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "user.js"), []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("launch: write user.js: %w", err)
	}
	return nil
}
