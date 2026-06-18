package camoufox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Reload reloads the current page and waits for the load event.
func (p *Page) Reload(ctx context.Context) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		return err
	}
	w := p.addNavWaiter()
	if _, err := p.client.Call(ctx, p.sessionID, "Page.reload", map[string]any{}); err != nil {
		p.removeNavWaiter(w)
		return fmt.Errorf("camoufox: reload: %w", err)
	}
	return p.awaitNav(ctx, w, "reload")
}

// GoBack navigates to the previous history entry. It reports false if there is
// no entry to go back to.
func (p *Page) GoBack(ctx context.Context) (bool, error) {
	return p.historyNav(ctx, "Page.goBack")
}

// GoForward navigates to the next history entry. It reports false if there is no
// entry to go forward to.
func (p *Page) GoForward(ctx context.Context) (bool, error) {
	return p.historyNav(ctx, "Page.goForward")
}

func (p *Page) historyNav(ctx context.Context, method string) (bool, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		return false, err
	}
	w := p.addNavWaiter()
	res, err := p.client.Call(ctx, p.sessionID, method, map[string]any{"frameId": p.mainFrame()})
	if err != nil {
		p.removeNavWaiter(w)
		return false, fmt.Errorf("camoufox: %s: %w", method, err)
	}
	var r struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(res, &r)
	if !r.Success {
		p.removeNavWaiter(w)
		return false, nil
	}
	if err := p.awaitNav(ctx, w, method); err != nil {
		return false, err
	}
	return true, nil
}

// SetContent replaces the page's HTML with the given markup.
func (p *Page) SetContent(ctx context.Context, html string) error {
	_, err := p.Evaluate(ctx, fmt.Sprintf(
		"(() => { document.open(); document.write(%s); document.close(); })()", jsString(html)))
	if err != nil {
		return fmt.Errorf("camoufox: set content: %w", err)
	}
	return nil
}

// addNavWaiter registers a load-completion waiter for a navigation about to be
// triggered.
func (p *Page) addNavWaiter() *navWaiter {
	w := &navWaiter{ch: make(chan error, 1)}
	p.mu.Lock()
	p.navWaiters = append(p.navWaiters, w)
	p.mu.Unlock()
	return w
}

// awaitNav blocks until the navigation's load event fires (or the context/
// browser ends).
func (p *Page) awaitNav(ctx context.Context, w *navWaiter, what string) error {
	select {
	case err := <-w.ch:
		return err
	case <-ctx.Done():
		p.removeNavWaiter(w)
		return fmt.Errorf("camoufox: %s: %w", what, ctx.Err())
	case <-p.browser.proc.Closed():
		return errors.New("camoufox: browser closed during navigation")
	}
}
