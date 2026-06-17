// Package camoufox is a pure-Go SDK for the Camoufox anti-detect browser. It
// downloads the real Camoufox build, generates a realistic fingerprint, injects
// it via CAMOU_CONFIG, launches the browser, and drives it over Playwright's
// Juggler protocol — with no Node.js, Python, or Playwright runtime required.
//
// Typical use:
//
//	br, err := camoufox.Launch(ctx, camoufox.Options{Headless: true})
//	if err != nil { ... }
//	defer br.Close()
//	pg, _ := br.NewPage(ctx)
//	pg.Goto(ctx, "https://example.com")
//	ua, _ := pg.EvaluateString(ctx, "navigator.userAgent")
package camoufox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yx-zero/camoufox-go/config"
	"github.com/yx-zero/camoufox-go/fetch"
	"github.com/yx-zero/camoufox-go/fingerprint"
	"github.com/yx-zero/camoufox-go/juggler"
	"github.com/yx-zero/camoufox-go/launch"
)

// Options configure a Camoufox launch.
type Options struct {
	// ExecutablePath is an explicit Camoufox binary. If empty, the browser is
	// downloaded (and cached) automatically via the fetch package.
	ExecutablePath string
	// CacheDir overrides where the auto-downloaded browser is cached.
	CacheDir string
	// Progress receives download progress when the browser is fetched.
	Progress fetch.ProgressFunc

	// OS selects the fingerprint operating system ("windows", "macos", "linux").
	// Empty picks one at random.
	OS string
	// FingerprintSeed makes the generated fingerprint deterministic when non-zero.
	FingerprintSeed int64
	// Timezone, Locale and WebRTCIP override the corresponding fingerprint fields.
	Timezone string
	Locale   string
	WebRTCIP string
	// Fingerprint, when non-nil, is used directly as the CAMOU_CONFIG map and
	// fingerprint generation is skipped (advanced use).
	Fingerprint config.Map

	// Headless runs without a visible window (recommended).
	Headless bool
	// Args, Env and UserPrefs are passed through to the browser process.
	Args      []string
	Env       []string
	UserPrefs map[string]any

	// Timeout bounds individual operations that lack their own context deadline
	// (default 30s).
	Timeout time.Duration

	// Debug enables verbose logging of unhandled Juggler events.
	Debug bool
}

// Browser is a running Camoufox instance.
type Browser struct {
	proc    *launch.Process
	client  *juggler.Client
	timeout time.Duration

	mu            sync.Mutex
	pages         map[string]*Page      // juggler sessionID -> page
	attached      map[string]*Page      // targetID -> page
	attachWaiters map[string]chan *Page // targetID -> waiter
}

// Launch downloads (if needed), starts and connects to a Camoufox browser.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	execPath := opts.ExecutablePath
	ffVersion := 0

	if execPath == "" {
		fopts := fetch.Options{CacheDir: opts.CacheDir, Progress: opts.Progress}
		p, err := fetch.Fetch(ctx, fopts)
		if err != nil {
			return nil, fmt.Errorf("camoufox: fetch browser: %w", err)
		}
		execPath = p
		if v, err := fetch.InstalledVersion(fopts); err == nil && v != nil {
			ffVersion = majorVersion(v.Version)
		}
	}

	cfg := opts.Fingerprint
	if cfg == nil {
		var err error
		cfg, err = fingerprint.Generate(fingerprint.Options{
			OS:        opts.OS,
			FFVersion: ffVersion,
			Seed:      opts.FingerprintSeed,
			Timezone:  opts.Timezone,
			Locale:    opts.Locale,
			WebRTCIP:  opts.WebRTCIP,
		})
		if err != nil {
			return nil, fmt.Errorf("camoufox: generate fingerprint: %w", err)
		}
	}

	lopts := launch.Options{
		ExecutablePath: execPath,
		Config:         cfg,
		Headless:       opts.Headless,
		Args:           opts.Args,
		Env:            opts.Env,
		UserPrefs:      opts.UserPrefs,
		Debug:          opts.Debug,
	}

	// A brand-new Camoufox install performs one-time first-run initialization
	// that can make the very first process restart, dropping the Juggler pipe.
	// Retry once so the first Launch on a fresh machine is reliable.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		b, err := connect(ctx, lopts, orDefault(opts.Timeout, 30*time.Second), opts.Debug)
		if err == nil {
			return b, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// connect starts the browser process and completes the Browser.enable handshake.
func connect(ctx context.Context, lopts launch.Options, timeout time.Duration, debug bool) (*Browser, error) {
	proc, err := launch.Start(lopts)
	if err != nil {
		return nil, err
	}

	b := &Browser{
		proc:          proc,
		client:        proc.Client(),
		timeout:       timeout,
		pages:         make(map[string]*Page),
		attached:      make(map[string]*Page),
		attachWaiters: make(map[string]chan *Page),
	}
	if debug {
		b.client.Log = stdLogger{}
	}
	b.subscribe()

	enableCtx, cancel := b.opCtx(ctx)
	defer cancel()
	if _, err := b.client.Call(enableCtx, "", "Browser.enable", map[string]any{
		"attachToDefaultContext": true,
	}); err != nil {
		proc.Stop()
		return nil, fmt.Errorf("camoufox: Browser.enable: %w", err)
	}
	return b, nil
}

// NewPage opens a new page in the default browser context.
func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	ctx, cancel := b.opCtx(ctx)
	defer cancel()

	res, err := b.client.Call(ctx, "", "Browser.newPage", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("camoufox: newPage: %w", err)
	}
	var r struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: newPage result: %w", err)
	}

	page, err := b.waitAttach(ctx, r.TargetID)
	if err != nil {
		return nil, err
	}
	if err := page.waitReady(ctx); err != nil {
		return nil, err
	}
	return page, nil
}

// Pages returns the currently-open pages.
func (b *Browser) Pages() []*Page {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Page, 0, len(b.pages))
	for _, p := range b.pages {
		out = append(out, p)
	}
	return out
}

// Closed returns a channel closed when the browser connection drops.
func (b *Browser) Closed() <-chan struct{} { return b.proc.Closed() }

// PID returns the browser process id.
func (b *Browser) PID() int { return b.proc.PID() }

// Close shuts the browser down and releases its resources.
func (b *Browser) Close() error { return b.proc.Stop() }

// ----- event wiring -----

func (b *Browser) subscribe() {
	b.client.Subscribe("Browser.attachedToTarget", b.onAttached)
	b.client.Subscribe("Browser.detachedFromTarget", b.onDetached)
	b.client.Subscribe("Runtime.executionContextCreated", b.routePage((*Page).onExecCtxCreated))
	b.client.Subscribe("Page.frameAttached", b.routePage((*Page).onFrameAttached))
	b.client.Subscribe("Page.navigationCommitted", b.routePage((*Page).onNavigationCommitted))
	b.client.Subscribe("Page.navigationAborted", b.routePage((*Page).onNavigationAborted))
	b.client.Subscribe("Page.eventFired", b.routePage((*Page).onEventFired))
}

// routePage wraps a page method as an event handler that dispatches by sessionID.
func (b *Browser) routePage(fn func(*Page, json.RawMessage)) juggler.EventHandler {
	return func(sessionID string, params json.RawMessage) {
		b.mu.Lock()
		p := b.pages[sessionID]
		b.mu.Unlock()
		if p != nil {
			fn(p, params)
		}
	}
}

func (b *Browser) onAttached(_ string, params json.RawMessage) {
	var ev struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			TargetID         string `json:"targetId"`
			BrowserContextID string `json:"browserContextId"`
		} `json:"targetInfo"`
	}
	if err := json.Unmarshal(params, &ev); err != nil || ev.SessionID == "" {
		return
	}
	page := newPage(b, ev.SessionID, ev.TargetInfo.TargetID, ev.TargetInfo.BrowserContextID)

	b.mu.Lock()
	b.pages[ev.SessionID] = page
	b.attached[ev.TargetInfo.TargetID] = page
	waiter := b.attachWaiters[ev.TargetInfo.TargetID]
	delete(b.attachWaiters, ev.TargetInfo.TargetID)
	b.mu.Unlock()

	if waiter != nil {
		waiter <- page
	}
}

func (b *Browser) onDetached(_ string, params json.RawMessage) {
	var ev struct {
		SessionID string `json:"sessionId"`
		TargetID  string `json:"targetId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	b.mu.Lock()
	if p := b.pages[ev.SessionID]; p != nil {
		p.markClosed()
	}
	delete(b.pages, ev.SessionID)
	delete(b.attached, ev.TargetID)
	b.mu.Unlock()
}

func (b *Browser) waitAttach(ctx context.Context, targetID string) (*Page, error) {
	b.mu.Lock()
	if p := b.attached[targetID]; p != nil {
		b.mu.Unlock()
		return p, nil
	}
	ch := make(chan *Page, 1)
	b.attachWaiters[targetID] = ch
	b.mu.Unlock()

	select {
	case p := <-ch:
		return p, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.attachWaiters, targetID)
		b.mu.Unlock()
		return nil, fmt.Errorf("camoufox: timed out waiting for page attach: %w", ctx.Err())
	case <-b.proc.Closed():
		return nil, fmt.Errorf("camoufox: browser closed before page attached")
	}
}

// opCtx returns ctx (if it already has a deadline) or a derived context bounded
// by the browser's default timeout.
func (b *Browser) opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, b.timeout)
}

func majorVersion(v string) int {
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

type stdLogger struct{}

func (stdLogger) Printf(format string, args ...any) {
	fmt.Printf("[camoufox] "+format+"\n", args...)
}
