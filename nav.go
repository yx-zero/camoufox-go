package camoufox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Reload reloads the current page and waits for it to load. It re-navigates to
// the current URL (the native Page.reload does not emit the navigationCommitted
// event this driver waits on in this Camoufox build).
func (p *Page) Reload(ctx context.Context) error {
	u := p.URL()
	if u == "" {
		var href string
		_ = p.EvaluateInto(ctx, "location.href", &href)
		u = href
	}
	if u == "" || u == "about:blank" {
		return nil
	}
	return p.Goto(ctx, u)
}

// GoBack navigates to the previous history entry, returning false if there was
// nothing to go back to. It drives history.back() and waits for the document to
// change, which is robust across bfcache restores (the browser's native goBack
// is unreliable for top-level pages in headless mode).
func (p *Page) GoBack(ctx context.Context) (bool, error) {
	return p.historyGo(ctx, -1)
}

// GoForward navigates to the next history entry, returning false if there was
// nothing to go forward to.
func (p *Page) GoForward(ctx context.Context) (bool, error) {
	return p.historyGo(ctx, 1)
}

func (p *Page) historyGo(ctx context.Context, delta int) (bool, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if err := p.waitReady(ctx); err != nil {
		return false, err
	}

	// Read the live document URL (navigationCommitted is unreliable for history
	// navigations in this build, so we compare location.href directly).
	before, _ := p.EvaluateString(ctx, "location.href")

	if _, err := p.Evaluate(ctx, fmt.Sprintf("history.go(%d)", delta)); err != nil {
		return false, fmt.Errorf("camoufox: history.go(%d): %w", delta, err)
	}

	// Wait (bounded) for the document URL to change; if it never does, there was
	// no entry in that direction.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := p.EvaluateString(ctx, "location.href")
		if err == nil && cur != "" && cur != before {
			p.mu.Lock()
			p.url = cur // keep URL() consistent after history navigation
			p.mu.Unlock()
			return true, nil
		}
		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			return false, fmt.Errorf("camoufox: history.go(%d): %w", delta, err)
		}
	}
	return false, nil
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
