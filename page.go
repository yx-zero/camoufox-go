package camoufox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

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
	execCtx     string // latest main-world execution context id
	url         string
	closed      bool

	readyCh   chan struct{}
	readyOnce sync.Once

	navWaiters []*navWaiter
}

// navWaiter tracks a pending navigation's load lifecycle. A waiter is only
// fulfilled by a load event after navigation has committed, so a stale load
// from the pre-navigation document cannot satisfy it.
type navWaiter struct {
	ch        chan error
	committed bool
}

func newPage(b *Browser, sessionID, targetID, contextID string) *Page {
	return &Page{
		browser:   b,
		client:    b.client,
		sessionID: sessionID,
		targetID:  targetID,
		contextID: contextID,
		readyCh:   make(chan struct{}),
	}
}

// SessionID returns the page's Juggler session id.
func (p *Page) SessionID() string { return p.sessionID }

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
	if ev.AuxData.Name == "" && ev.AuxData.FrameID == p.mainFrameID {
		p.execCtx = ev.ExecutionContextID
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
	if ev.ParentFrameID != "" {
		return // sub-frame
	}
	p.mu.Lock()
	if p.mainFrameID == "" {
		p.mainFrameID = ev.FrameID
	}
	ready := p.mainFrameID != "" && p.execCtx != ""
	p.mu.Unlock()
	if ready {
		p.signalReady()
	}
}

func (p *Page) onNavigationCommitted(params json.RawMessage) {
	var ev struct {
		FrameID string `json:"frameId"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
		p.url = ev.URL
		for _, w := range p.navWaiters {
			w.committed = true
		}
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
	if ev.Name != "load" {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
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
	p.mu.Unlock()
}

func (p *Page) onNavigationAborted(params json.RawMessage) {
	var ev struct {
		FrameID   string `json:"frameId"`
		ErrorText string `json:"errorText"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	p.mu.Lock()
	if ev.FrameID == p.mainFrameID {
		for _, w := range p.navWaiters {
			w.ch <- fmt.Errorf("navigation aborted: %s", ev.ErrorText)
		}
		p.navWaiters = nil
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

	if _, err := p.client.Call(ctx, p.sessionID, "Page.navigate", map[string]any{
		"frameId": frameID,
		"url":     url,
	}); err != nil {
		p.removeNavWaiter(w)
		return fmt.Errorf("camoufox: navigate %s: %w", url, err)
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
