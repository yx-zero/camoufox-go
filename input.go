package camoufox

import (
	"context"
	"fmt"
)

// Click dispatches a left-button mouse click at viewport coordinates (x, y):
// a move, a button-down and a button-up, matching how Juggler expects the
// buttons bitmask (left = 1) to be set during the press.
func (p *Page) Click(ctx context.Context, x, y float64) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()

	steps := []map[string]any{
		{"type": "mousemove", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0},
		{"type": "mousedown", "button": 0, "buttons": 1, "x": x, "y": y, "modifiers": 0, "clickCount": 1},
		{"type": "mouseup", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0, "clickCount": 1},
	}
	for _, s := range steps {
		if _, err := p.client.Call(ctx, p.sessionID, "Page.dispatchMouseEvent", s); err != nil {
			return fmt.Errorf("camoufox: click: %w", err)
		}
	}
	return nil
}

// Move dispatches a mouse-move to viewport coordinates (x, y).
func (p *Page) Move(ctx context.Context, x, y float64) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	_, err := p.client.Call(ctx, p.sessionID, "Page.dispatchMouseEvent", map[string]any{
		"type": "mousemove", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0,
	})
	if err != nil {
		return fmt.Errorf("camoufox: move: %w", err)
	}
	return nil
}

// Type inserts text into the currently focused element. Focus an input first
// (e.g. via Click) before calling Type.
func (p *Page) Type(ctx context.Context, text string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	_, err := p.client.Call(ctx, p.sessionID, "Page.insertText", map[string]any{"text": text})
	if err != nil {
		return fmt.Errorf("camoufox: type: %w", err)
	}
	return nil
}
