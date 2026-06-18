package camoufox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LoadState identifies a page lifecycle milestone to wait for.
type LoadState string

const (
	// LoadStateDOMContentLoaded waits for the DOMContentLoaded event.
	LoadStateDOMContentLoaded LoadState = "domcontentloaded"
	// LoadStateLoad waits for the load event.
	LoadStateLoad LoadState = "load"
	// LoadStateNetworkIdle waits until there are no network connections for at
	// least 500ms.
	LoadStateNetworkIdle LoadState = "networkidle"
)

// pollInterval is how often the wait helpers re-check their condition.
const pollInterval = 100 * time.Millisecond

// WaitForLoadState blocks until the page reaches the given lifecycle state.
func (p *Page) WaitForLoadState(ctx context.Context, state LoadState) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	for {
		p.mu.Lock()
		dcl, load := p.domContentLoaded, p.loadFired
		idleFor := time.Since(p.lastNetwork)
		n := len(p.inflight)
		p.mu.Unlock()

		switch state {
		case LoadStateDOMContentLoaded:
			if dcl || load {
				return nil
			}
		case LoadStateLoad:
			if load {
				return nil
			}
		case LoadStateNetworkIdle:
			if n == 0 && idleFor >= 500*time.Millisecond {
				return nil
			}
		default:
			return fmt.Errorf("camoufox: unknown load state %q", state)
		}

		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("camoufox: wait for %s: %w", state, err)
		}
	}
}

// WaitForSelectorOptions configures WaitForSelector.
type WaitForSelectorOptions struct {
	// Visible requires the element to be present AND visible (non-zero box and
	// not display:none/visibility:hidden). Default waits only for presence.
	Visible bool
}

// WaitForSelector waits until an element matching the CSS selector appears (and,
// when Visible is set, is visible). It searches the main frame and, recursively,
// any open shadow roots.
func (p *Page) WaitForSelector(ctx context.Context, selector string, opts WaitForSelectorOptions) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	expr := selectorProbeJS(selector, opts.Visible)
	for {
		var ok bool
		if err := p.EvaluateInto(ctx, expr, &ok); err == nil && ok {
			return nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("camoufox: wait for selector %q: %w", selector, err)
		}
	}
}

// WaitForFunction polls a JavaScript expression until it returns a truthy value.
func (p *Page) WaitForFunction(ctx context.Context, expression string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	expr := "(() => { try { return !!(" + expression + "); } catch (e) { return false; } })()"
	for {
		var ok bool
		if err := p.EvaluateInto(ctx, expr, &ok); err == nil && ok {
			return nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("camoufox: wait for function: %w", err)
		}
	}
}

// WaitForURL waits until the main-frame URL contains the given substring.
func (p *Page) WaitForURL(ctx context.Context, substr string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	for {
		if strings.Contains(p.URL(), substr) {
			return nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return fmt.Errorf("camoufox: wait for url %q: %w", substr, err)
		}
	}
}

// selectorProbeJS builds an expression that reports whether selector matches
// (optionally requiring visibility), piercing open shadow roots.
func selectorProbeJS(selector string, visible bool) string {
	sel, _ := json.Marshal(selector)
	visCheck := "true"
	if visible {
		visCheck = `(() => { const r = el.getBoundingClientRect();
			const s = getComputedStyle(el);
			return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; })()`
	}
	return `(() => {
		const sel = ` + string(sel) + `;
		const find = root => {
			let el = root.querySelector(sel);
			if (el) return el;
			for (const e of root.querySelectorAll('*')) {
				if (e.shadowRoot) { const f = find(e.shadowRoot); if (f) return f; }
			}
			return null;
		};
		const el = find(document);
		if (!el) return false;
		return ` + visCheck + `;
	})()`
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
