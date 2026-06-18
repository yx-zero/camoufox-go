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
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yx-zero/camoufox-go/addons"
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

	// FFVersion overrides the Firefox major version used for the fingerprint
	// (User-Agent/rv: rewrite + preset bundle selection). Zero uses the installed
	// browser's version.
	FFVersion int

	// Headless runs without a visible window (recommended).
	Headless bool
	// Humanize enables Camoufox's engine-level human-like cursor trajectories for
	// dispatched mouse events. HumanizeMaxTime (seconds), when > 0, caps a single
	// movement's duration (maps to the "humanize:maxTime" config key).
	Humanize        bool
	HumanizeMaxTime float64

	// Proxy routes all browser traffic through a proxy (Juggler setBrowserProxy).
	Proxy *Proxy

	// GeoIP, when set, spoofs timezone/locale/geolocation (and the WebRTC IP)
	// from an IP address. Pass a specific IP, or "auto" to detect the public IP
	// (through Proxy when one is set). Requires the GeoIP database to be
	// downloadable (cached under CacheDir/geoip).
	GeoIP string
	// GeoIPDB selects the GeoIP database by name (default "MaxMind GeoLite2").
	GeoIPDB string

	// Addons are paths to extracted Firefox addon directories (each containing a
	// manifest.json) to load. DefaultAddons opts into the bundled defaults
	// (e.g. uBlock Origin, downloaded on first use); ExcludeAddons skips specific
	// defaults when DefaultAddons is enabled.
	Addons        []string
	DefaultAddons bool
	ExcludeAddons []addons.Default

	// Fonts overrides the generated font list. CustomFontsOnly disables the
	// browser's bundled OS fonts (requires Fonts to be set).
	Fonts           []string
	CustomFontsOnly bool

	// ScreenMaxWidth/ScreenMaxHeight constrain the generated fingerprint's screen
	// dimensions. WindowWidth/WindowHeight set a fixed outer window size.
	ScreenMaxWidth  int
	ScreenMaxHeight int
	WindowWidth     int
	WindowHeight    int

	// WebGLVendor/WebGLRenderer force a specific WebGL vendor/renderer pair
	// (both must be set, and must be a valid combination for the fingerprint OS).
	WebGLVendor   string
	WebGLRenderer string

	// BlockImages, BlockWebRTC and BlockWebGL disable the respective subsystems.
	// Blocking WebGL/WebRTC can be a detection signal — use only when needed.
	BlockImages bool
	BlockWebRTC bool
	BlockWebGL  bool
	// DisableCOOP disables Cross-Origin-Opener-Policy so elements in cross-origin
	// iframes (e.g. the Cloudflare Turnstile checkbox) can be clicked.
	DisableCOOP bool
	// EnableCache keeps page/request caches across navigations (more memory).
	EnableCache bool
	// MainWorldEval enables main-world script evaluation; prefix scripts with
	// "mw:" to run them in the page's main world.
	MainWorldEval bool

	// UserDataDir uses a persistent profile directory (cookies, cf_clearance and
	// caches persist across launches). Empty uses a fresh temporary profile.
	UserDataDir string
	// VirtualDisplay (Linux) points the browser at an existing Xvfb display, e.g.
	// ":99". It sets DISPLAY and forces the X11 backend.
	VirtualDisplay string

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
	cleanup       []func()              // run on Close (e.g. kill Xvfb)
}

// Launch downloads (if needed), starts and connects to a Camoufox browser.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	execPath := opts.ExecutablePath
	ffVersion := opts.FFVersion

	if execPath == "" {
		fopts := fetch.Options{CacheDir: opts.CacheDir, Progress: opts.Progress}
		p, err := fetch.Fetch(ctx, fopts)
		if err != nil {
			return nil, fmt.Errorf("camoufox: fetch browser: %w", err)
		}
		execPath = p
		if ffVersion == 0 {
			if v, err := fetch.InstalledVersion(fopts); err == nil && v != nil {
				ffVersion = majorVersion(v.Version)
			}
		}
	}

	cacheDir, err := opts.resolveCacheDir()
	if err != nil {
		return nil, err
	}

	seed := opts.FingerprintSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // identity variety, not crypto

	// Base fingerprint (preset selection, fonts, voices, seeds). Timezone/locale/
	// WebRTC are applied later in applyLaunchOptions so GeoIP can override the
	// preset and explicit options can override GeoIP.
	cfg := opts.Fingerprint
	if cfg == nil {
		cfg, err = fingerprint.Generate(fingerprint.Options{
			OS:              opts.OS,
			FFVersion:       ffVersion,
			Seed:            seed,
			ScreenMaxWidth:  opts.ScreenMaxWidth,
			ScreenMaxHeight: opts.ScreenMaxHeight,
		})
		if err != nil {
			return nil, fmt.Errorf("camoufox: generate fingerprint: %w", err)
		}
	}

	// Engine-level humanized cursor: matches camoufox-py utils.py (set_into
	// "humanize" / "humanize:maxTime"). The C++ jugglerSendMouseEvent generates a
	// human trajectory between the last cursor position and the target.
	if opts.Humanize {
		cfg["humanize"] = true
		if opts.HumanizeMaxTime > 0 {
			cfg["humanize:maxTime"] = opts.HumanizeMaxTime
		}
	}

	prefs, err := applyLaunchOptions(cfg, opts, cacheDir, rng)
	if err != nil {
		return nil, err
	}

	// Virtual display: "auto" spawns Xvfb (Linux); any other value is treated as
	// an existing display string (e.g. ":99").
	var xvfb *launch.Xvfb
	if opts.VirtualDisplay == "auto" {
		xvfb, err = launch.StartXvfb(opts.Debug)
		if err != nil {
			return nil, err
		}
		opts.VirtualDisplay = xvfb.Display
	}

	env := opts.Env
	if opts.VirtualDisplay != "" {
		// Point GTK/Firefox at the Xvfb display and force the X11 backend.
		env = append(append([]string(nil), env...),
			"DISPLAY="+opts.VirtualDisplay,
			"GDK_BACKEND=x11",
			"MOZ_ENABLE_WAYLAND=0",
		)
	}

	lopts := launch.Options{
		ExecutablePath: execPath,
		Config:         cfg,
		Headless:       opts.Headless,
		ProfileDir:     opts.UserDataDir,
		Args:           opts.Args,
		Env:            env,
		UserPrefs:      prefs,
		Debug:          opts.Debug,
	}

	// A brand-new Camoufox install performs one-time first-run initialization
	// that can make the very first process restart, dropping the Juggler pipe.
	// Retry once so the first Launch on a fresh machine is reliable.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		b, err := connect(ctx, lopts, orDefault(opts.Timeout, 30*time.Second), opts.Debug)
		if err == nil {
			if xvfb != nil {
				b.cleanup = append(b.cleanup, xvfb.Kill)
			}
			if opts.Proxy != nil {
				if err := b.applyProxy(ctx, opts.Proxy); err != nil {
					b.Close()
					return nil, err
				}
			}
			return b, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if xvfb != nil {
		xvfb.Kill()
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
	return b.newPageIn(ctx, "")
}

// newPageIn opens a page in the given browser context ("" = default).
func (b *Browser) newPageIn(ctx context.Context, browserContextID string) (*Page, error) {
	ctx, cancel := b.opCtx(ctx)
	defer cancel()

	params := map[string]any{}
	if browserContextID != "" {
		params["browserContextId"] = browserContextID
	}
	res, err := b.client.Call(ctx, "", "Browser.newPage", params)
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
func (b *Browser) Close() error {
	err := b.proc.Stop()
	for _, fn := range b.cleanup {
		fn()
	}
	return err
}

// ----- event wiring -----

func (b *Browser) subscribe() {
	b.client.Subscribe("Browser.attachedToTarget", b.onAttached)
	b.client.Subscribe("Browser.detachedFromTarget", b.onDetached)
	b.client.Subscribe("Runtime.executionContextCreated", b.routePage((*Page).onExecCtxCreated))
	b.client.Subscribe("Page.frameAttached", b.routePage((*Page).onFrameAttached))
	b.client.Subscribe("Page.frameDetached", b.routePage((*Page).onFrameDetached))
	b.client.Subscribe("Page.navigationStarted", b.routePage((*Page).onNavigationStarted))
	b.client.Subscribe("Page.navigationCommitted", b.routePage((*Page).onNavigationCommitted))
	b.client.Subscribe("Page.navigationAborted", b.routePage((*Page).onNavigationAborted))
	b.client.Subscribe("Page.eventFired", b.routePage((*Page).onEventFired))
	b.client.Subscribe("Network.requestWillBeSent", b.routePage((*Page).onRequestWillBeSent))
	b.client.Subscribe("Network.responseReceived", b.routePage((*Page).onResponseReceived))
	b.client.Subscribe("Network.requestFinished", b.routePage((*Page).onRequestDone))
	b.client.Subscribe("Network.requestFailed", b.routePage((*Page).onRequestDone))
	b.client.Subscribe("Page.dialogOpened", b.routePage((*Page).onDialogOpened))
	b.client.Subscribe("Runtime.console", b.routePage((*Page).onConsole))
	b.client.Subscribe("Page.uncaughtError", b.routePage((*Page).onUncaughtError))
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
			OpenerID         string `json:"openerId"`
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
	var opener *Page
	if ev.TargetInfo.OpenerID != "" {
		opener = b.attached[ev.TargetInfo.OpenerID]
	}
	b.mu.Unlock()

	if waiter != nil {
		waiter <- page
		return // an awaited (explicitly opened) page is not a popup
	}
	// A target opened by another page with no waiter is a popup.
	if opener != nil {
		opener.mu.Lock()
		h := opener.popupHandler
		opener.mu.Unlock()
		if h != nil {
			go func() {
				if err := page.waitReady(context.Background()); err == nil {
					h(page)
				}
			}()
		}
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
