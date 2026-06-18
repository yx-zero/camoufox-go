package camoufox

import (
	"context"
	"fmt"
)

// Locator is a lazy, auto-waiting reference to an element matched by a CSS
// selector. Each action re-resolves the selector (piercing open shadow roots)
// and waits up to the operation timeout for it to appear.
type Locator struct {
	page     *Page
	selector string
}

// Locator returns a Locator for the given CSS selector.
func (p *Page) Locator(selector string) *Locator {
	return &Locator{page: p, selector: selector}
}

// resolve waits for the element to appear and returns a handle to it.
func (l *Locator) resolve(ctx context.Context) (*ElementHandle, error) {
	ctx, cancel := l.page.browser.opCtx(ctx)
	defer cancel()
	expr := selectorElementJS(l.selector)
	for {
		h, found, err := l.page.evalHandle(ctx, expr)
		if err == nil && found {
			return h, nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, fmt.Errorf("camoufox: locator %q: %w", l.selector, err)
		}
	}
}

// WaitFor waits until the element exists.
func (l *Locator) WaitFor(ctx context.Context) error {
	_, err := l.resolve(ctx)
	return err
}

// Click resolves the element and performs a real mouse click at its center.
func (l *Locator) Click(ctx context.Context) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.Click(ctx)
}

// Hover moves the mouse to the element's center.
func (l *Locator) Hover(ctx context.Context) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.Hover(ctx)
}

// Fill sets the value of an input/textarea.
func (l *Locator) Fill(ctx context.Context, value string) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.Fill(ctx, value)
}

// Type focuses the element and types text through the input pipeline.
func (l *Locator) Type(ctx context.Context, text string) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.Type(ctx, text)
}

// Press focuses the element and sends a key press.
func (l *Locator) Press(ctx context.Context, key string) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	if err := h.Focus(ctx); err != nil {
		return err
	}
	return l.page.Press(ctx, key)
}

// Check / Uncheck toggle a checkbox or radio.
func (l *Locator) Check(ctx context.Context) error   { return l.act(ctx, (*ElementHandle).Check) }
func (l *Locator) Uncheck(ctx context.Context) error { return l.act(ctx, (*ElementHandle).Uncheck) }

func (l *Locator) act(ctx context.Context, fn func(*ElementHandle, context.Context) error) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return fn(h, ctx)
}

// SelectOption selects an <option> by value.
func (l *Locator) SelectOption(ctx context.Context, value string) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.SelectOption(ctx, value)
}

// InnerText / TextContent / InputValue / GetAttribute read element data.
func (l *Locator) InnerText(ctx context.Context) (string, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return "", err
	}
	return h.InnerText(ctx)
}

func (l *Locator) TextContent(ctx context.Context) (string, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return "", err
	}
	return h.TextContent(ctx)
}

func (l *Locator) InputValue(ctx context.Context) (string, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return "", err
	}
	return h.InputValue(ctx)
}

func (l *Locator) GetAttribute(ctx context.Context, name string) (string, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return "", err
	}
	return h.GetAttribute(ctx, name)
}

// BoundingBox returns the element's viewport rectangle.
func (l *Locator) BoundingBox(ctx context.Context) (*BoundingBox, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return h.BoundingBox(ctx)
}

// SetInputFiles sets the files for an <input type=file>.
func (l *Locator) SetInputFiles(ctx context.Context, paths []string) error {
	h, err := l.resolve(ctx)
	if err != nil {
		return err
	}
	return h.SetInputFiles(ctx, paths)
}

// IsVisible reports whether the element currently exists and is visible (it does
// not wait).
func (l *Locator) IsVisible(ctx context.Context) (bool, error) {
	h, found, err := l.page.evalHandle(ctx, selectorElementJS(l.selector))
	if err != nil || !found {
		return false, err
	}
	return h.IsVisible(ctx)
}

// Count returns the number of light-DOM elements matching the selector.
func (l *Locator) Count(ctx context.Context) (int, error) {
	var n int
	err := l.page.EvaluateInto(ctx, "document.querySelectorAll("+jsString(l.selector)+").length", &n)
	return n, err
}

// selectorElementJS returns an expression that resolves the selector to an
// element (or null), piercing open shadow roots.
func selectorElementJS(selector string) string {
	return `(() => {
		const sel = ` + jsString(selector) + `;
		const find = root => {
			let el = root.querySelector(sel);
			if (el) return el;
			for (const e of root.querySelectorAll('*')) {
				if (e.shadowRoot) { const f = find(e.shadowRoot); if (f) return f; }
			}
			return null;
		};
		return find(document);
	})()`
}
