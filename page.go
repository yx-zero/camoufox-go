package camoufox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yx-zero/camoufox-go/juggler"
)

// Page is a single Camoufox page (tab).
type Page struct {
	browser   *Browser
	client    *juggler.Client
	sessionID string
	targetID  string
	contextID string

	mu          sync.Mutex
	mainFrameID string
	execCtx     string // latest main-world execution context id (main frame)
	url         string
	closed      bool

	readyCh   chan struct{}
	readyOnce sync.Once

	navWaiters []*navWaiter

	// frame tree: frameID -> frame info, and frameID -> its main-world context.
	frames   map[string]*frameInfo
	frameCtx map[string]string

	// lifecycle flags for the main frame, reset on each main-frame commit.
	domContentLoaded bool
	loadFired        bool

	// network tracking (for WaitForLoadState networkidle + the nav Response).
	inflight     map[string]bool
	lastNetwork  time.Time
	mainNavID    string    // navigationId of the current main-frame navigation
	mainNavReqID string    // requestId of that navigation's document request
	lastResponse *Response // most recent main-frame document response

	// init scripts accumulated via AddInitScript (Page.setInitScripts replaces).
	initScripts []string

	// network interception routes + whether interception is enabled.
	routes          []*routeEntry
	interceptEnabled bool

	// dialogHandler handles JS dialogs; nil auto-dismisses.
	dialogHandler func(*Dialog)

	// event handlers (nil = ignored).
	consoleHandler   func(ConsoleMessage)
	pageErrorHandler func(string)
	popupHandler     func(*Page)
}

// frameInfo is a node in the page's frame tree.
type frameInfo struct {
	id       string
	parentID string
	url      string
	name     string
}

// navWaiter tracks a pending navigation's load lifecycle. A waiter is only
// fulfilled by a load event after the TARGET navigation has committed. Camoufox
// fires intermediate about:blank commits+loads before the real document; we
// must not let those satisfy the waiter, or Goto returns while the page is still
// about:blank. We gate on the navigate call's navigationId (and, until that id
// is known, on the commit being to a non-about:blank URL).
type navWaiter struct {
	ch        chan error
	committed bool
	navID     string // navigationId returned by Page.navigate ("" until known)
}

func newPage(b *Browser, sessionID, targetID, contextID string) *Page {
	return &Page{
		browser:     b,
		client:      b.client,
		sessionID:   sessionID,
		targetID:    targetID,
		contextID:   contextID,
		readyCh:     make(chan struct{}),
		frames:      make(map[string]*frameInfo),
		frameCtx:    make(map[string]string),
		inflight:    make(map[string]bool),
		lastNetwork: time.Now(),
	}
}

// SessionID returns the page's Juggler session id.
func (p *Page) SessionID() string { return p.sessionID }

// ContextID returns the page's browser-context id ("" for the default context).
func (p *Page) ContextID() string { return p.contextID }

// URL returns the last known committed URL of the main frame.
func (p *Page) URL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// ----- lifecycle event handlers (called from Browser.routePage) -----

func (p *Page) onExecCtxCreated(params json.RawMessage) {
	var ev struct {
		ExecutionContextID string `json:"executionContextId"`
		AuxData            struct {
			FrameID string `json:"frameId"`
			Name    string `json:"name"`
		} `json:"auxData"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.AuxData.FrameID != "" && p.mainFrameID == "" {
		p.mainFrameID = ev.AuxData.FrameID
	}
	// The main world's context has an empty world name; isolated/utility worlds
	// are named. Evaluate runs in the main world so the page sees real globals.
	if ev.AuxData.Name == "" && ev.AuxData.FrameID != "" {
		p.frameCtx[ev.AuxData.FrameID] = ev.ExecutionContextID
		if ev.AuxData.FrameID == p.mainFrameID {
			p.execCtx = ev.ExecutionContextID
		}
	}
	ready := p.mainFrameID != "" && p.execCtx != ""
	p.mu.Unlock()
	if ready {
		p.signalReady()
	}
}

func (p *Page) onFrameAttached(params json.RawMessage) {
	var ev struct {
		FrameID       string `json:"frameId"`
		ParentFrameID string `json:"parentFrameId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	p.frames[ev.FrameID] = &frameInfo{id: ev.FrameID, parentID: ev.ParentFrameID}
	if ev.ParentFrameID == "" && p.mainFrameID == "" {
		p.mainFrameID = ev.FrameID
	}
	ready := p.mainFrameID != "" && p.execCtx != ""
	p.mu.Unlock()
	if ready {
		p.signalReady()
	}
}

func (p *Page) onFrameDetached(params json.RawMessage) {
	var ev struct {
		FrameID string `json:"frameId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	delete(p.frames, ev.FrameID)
	delete(p.frameCtx, ev.FrameID)
	p.mu.Unlock()
}

func (p *Page) onNavigationCommitted(params json.RawMessage) {
	var ev struct {
		FrameID      string `json:"frameId"`
		URL          string `json:"url"`
		Name         string `json:"name"`
		NavigationID string `json:"navigationId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if fi := p.frames[ev.FrameID]; fi != nil {
		fi.url = ev.URL
		fi.name = ev.Name
	} else {
		p.frames[ev.FrameID] = &frameInfo{id: ev.FrameID, url: ev.URL, name: ev.Name}
	}
	if ev.FrameID == p.mainFrameID {
		p.url = ev.URL
		// A new main-document committed: reset lifecycle flags.
		p.domContentLoaded = false
		p.loadFired = false
		for _, w := range p.navWaiters {
			switch {
			case w.navID != "":
				// We know the target navigation id: require an exact match.
				if ev.NavigationID == w.navID {
					w.committed = true
				}
			case ev.URL != "about:blank":
				// Id not yet known: any real (non-blank) commit is the target.
				w.committed = true
			}
		}
	}
	p.mu.Unlock()
}

func (p *Page) onNavigationStarted(params json.RawMessage) {
	var ev struct {
		FrameID      string `json:"frameId"`
		NavigationID string `json:"navigationId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
		p.mainNavID = ev.NavigationID
		p.mainNavReqID = ""
		p.lastResponse = nil
	}
	p.mu.Unlock()
}

func (p *Page) onEventFired(params json.RawMessage) {
	var ev struct {
		FrameID string `json:"frameId"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
		switch ev.Name {
		case "DOMContentLoaded":
			p.domContentLoaded = true
		case "load":
			p.loadFired = true
			kept := p.navWaiters[:0]
			for _, w := range p.navWaiters {
				if w.committed {
					w.ch <- nil
				} else {
					kept = append(kept, w)
				}
			}
			p.navWaiters = kept
		}
	}
	p.mu.Unlock()
}

// ----- network event handlers (for networkidle + the nav Response) -----

func (p *Page) onRequestWillBeSent(params json.RawMessage) {
	var ev struct {
		FrameID       string     `json:"frameId"`
		RequestID     string     `json:"requestId"`
		NavigationID  string     `json:"navigationId"`
		URL           string     `json:"url"`
		Method        string     `json:"method"`
		Headers       []headerKV `json:"headers"`
		IsIntercepted bool       `json:"isIntercepted"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	p.inflight[ev.RequestID] = true
	p.lastNetwork = time.Now()
	if ev.NavigationID != "" && ev.NavigationID == p.mainNavID && p.mainNavReqID == "" {
		p.mainNavReqID = ev.RequestID
	}
	route := p.matchRouteLocked(ev.URL)
	p.mu.Unlock()

	if ev.IsIntercepted {
		r := &Route{
			page: p, RequestID: ev.RequestID, URL: ev.URL,
			Method: ev.Method, Headers: headersToMap(ev.Headers),
		}
		// Run the handler off the event loop; default to continue when unmatched.
		go func() {
			if route != nil {
				route.handler(r)
				if !r.handled {
					_ = r.Continue()
				}
			} else {
				_ = r.Continue()
			}
		}()
	}
}

func (p *Page) onResponseReceived(params json.RawMessage) {
	var ev struct {
		RequestID  string         `json:"requestId"`
		Status     int            `json:"status"`
		StatusText string         `json:"statusText"`
		Headers    []headerKV     `json:"headers"`
		RemoteIP   string         `json:"remoteIPAddress"`
		FromCache  bool           `json:"fromCache"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.RequestID == p.mainNavReqID {
		p.lastResponse = &Response{
			URL:        p.url,
			Status:     ev.Status,
			StatusText: ev.StatusText,
			Headers:    headersToMap(ev.Headers),
			RemoteIP:   ev.RemoteIP,
			FromCache:  ev.FromCache,
		}
	}
	p.mu.Unlock()
}

func (p *Page) onRequestDone(params json.RawMessage) {
	var ev struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	delete(p.inflight, ev.RequestID)
	p.lastNetwork = time.Now()
	p.mu.Unlock()
}

func (p *Page) onNavigationAborted(params json.RawMessage) {
	var ev struct {
		FrameID      string `json:"frameId"`
		NavigationID string `json:"navigationId"`
		ErrorText    string `json:"errorText"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
		kept := p.navWaiters[:0]
		for _, w := range p.navWaiters {
			if w.navID != "" && w.navID == ev.NavigationID {
				w.ch <- fmt.Errorf("navigation aborted: %s", ev.ErrorText)
			} else {
				kept = append(kept, w)
			}
		}
		p.navWaiters = kept
	}
	p.mu.Unlock()
}

func (p *Page) signalReady() { p.readyOnce.Do(func() { close(p.readyCh) }) }

func (p *Page) markClosed() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}

// waitReady blocks until the page has a main frame and execution context.
func (p *Page) waitReady(ctx context.Context) error {
	select {
	case <-p.readyCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("camoufox: page not ready: %w", ctx.Err())
	case <-p.browser.proc.Closed():
		return errors.New("camoufox: browser closed before page ready")
	}
}

func (p *Page) mainFrame() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mainFrameID
}

func (p *Page) executionContext() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.execCtx
}

// ----- navigation -----

// Goto navigates the page to url and waits for the load event.
func (p *Page) Goto(ctx context.Context, url string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		return err
	}

	w := &navWaiter{ch: make(chan error, 1)}
	p.mu.Lock()
	p.navWaiters = append(p.navWaiters, w)
	frameID := p.mainFrameID
	p.mu.Unlock()

	res, err := p.client.Call(ctx, p.sessionID, "Page.navigate", map[string]any{
		"frameId": frameID,
		"url":     url,
	})
	if err != nil {
		p.removeNavWaiter(w)
		return fmt.Errorf("camoufox: navigate %s: %w", url, err)
	}
	// Pin the waiter to this navigation's id so intermediate about:blank commits
	// can't satisfy it. (If the real commit already arrived and set committed via
	// the non-blank branch, this is harmless.)
	var navRes struct {
		NavigationID string `json:"navigationId"`
	}
	if json.Unmarshal(res, &navRes) == nil && navRes.NavigationID != "" {
		p.mu.Lock()
		if !w.committed {
			w.navID = navRes.NavigationID
		}
		p.mu.Unlock()
	}

	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		p.removeNavWaiter(w)
		return fmt.Errorf("camoufox: navigate %s: %w", url, ctx.Err())
	case <-p.browser.proc.Closed():
		return errors.New("camoufox: browser closed during navigation")
	}
}

func (p *Page) removeNavWaiter(target *navWaiter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.navWaiters[:0]
	for _, w := range p.navWaiters {
		if w != target {
			kept = append(kept, w)
		}
	}
	p.navWaiters = kept
}

// ----- evaluation -----

// Evaluate runs a JavaScript expression in the page's main world and returns the
// raw JSON value of the result. For a function, wrap it: "(() => {...})()".
func (p *Page) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		return nil, err
	}
	cid := p.executionContext()
	if cid == "" {
		return nil, errors.New("camoufox: no execution context")
	}

	res, err := p.client.Call(ctx, p.sessionID, "Runtime.evaluate", map[string]any{
		"executionContextId": cid,
		"expression":         expression,
		"returnByValue":      true,
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: evaluate: %w", err)
	}
	var r struct {
		Result *struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text  string          `json:"text"`
			Value json.RawMessage `json:"value"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: evaluate result: %w", err)
	}
	if r.ExceptionDetails != nil {
		return nil, fmt.Errorf("camoufox: evaluate exception: %s", r.ExceptionDetails.Text)
	}
	if r.Result == nil {
		return nil, nil
	}
	return r.Result.Value, nil
}

// EvaluateInto runs expression and unmarshals the result into out.
func (p *Page) EvaluateInto(ctx context.Context, expression string, out any) error {
	raw, err := p.Evaluate(ctx, expression)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// EvaluateString runs expression and returns the result as a string.
func (p *Page) EvaluateString(ctx context.Context, expression string) (string, error) {
	var s string
	if err := p.EvaluateInto(ctx, expression, &s); err != nil {
		return "", err
	}
	return s, nil
}

// Content returns the page's current HTML.
func (p *Page) Content(ctx context.Context) (string, error) {
	return p.EvaluateString(ctx, "document.documentElement.outerHTML")
}

// Title returns the document title.
func (p *Page) Title(ctx context.Context) (string, error) {
	return p.EvaluateString(ctx, "document.title")
}

// ----- screenshot -----

// Screenshot captures the current viewport as PNG bytes.
func (p *Page) Screenshot(ctx context.Context) ([]byte, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()

	var vp struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	if err := p.EvaluateInto(ctx,
		`({w: Math.max(document.documentElement.clientWidth, window.innerWidth) || 1280,`+
			` h: Math.max(document.documentElement.clientHeight, window.innerHeight) || 720})`,
		&vp); err != nil {
		return nil, err
	}
	if vp.W <= 0 {
		vp.W = 1280
	}
	if vp.H <= 0 {
		vp.H = 720
	}

	res, err := p.client.Call(ctx, p.sessionID, "Page.screenshot", map[string]any{
		"mimeType": "image/png",
		"clip":     map[string]any{"x": 0, "y": 0, "width": vp.W, "height": vp.H},
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: screenshot: %w", err)
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: screenshot result: %w", err)
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

// ----- cookies -----

// Cookie is a browser cookie.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Size     int     `json:"size"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	Session  bool    `json:"session"`
	SameSite string  `json:"sameSite"`
}

// Cookies returns the cookies for this page's browser context.
func (p *Page) Cookies(ctx context.Context) ([]Cookie, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()

	params := map[string]any{}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	res, err := p.client.Call(ctx, "", "Browser.getCookies", params)
	if err != nil {
		return nil, fmt.Errorf("camoufox: getCookies: %w", err)
	}
	var r struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: getCookies result: %w", err)
	}
	return r.Cookies, nil
}

// Close closes the page.
func (p *Page) Close(ctx context.Context) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	_, err := p.client.Call(ctx, p.sessionID, "Page.close", map[string]any{})
	return err
}
