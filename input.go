package camoufox

import (
	"context"
	"fmt"
	"strings"
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

// DblClick dispatches a double left-click at viewport coordinates (x, y).
func (p *Page) DblClick(ctx context.Context, x, y float64) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	steps := []map[string]any{
		{"type": "mousemove", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0},
		{"type": "mousedown", "button": 0, "buttons": 1, "x": x, "y": y, "modifiers": 0, "clickCount": 1},
		{"type": "mouseup", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0, "clickCount": 1},
		{"type": "mousedown", "button": 0, "buttons": 1, "x": x, "y": y, "modifiers": 0, "clickCount": 2},
		{"type": "mouseup", "button": 0, "buttons": 0, "x": x, "y": y, "modifiers": 0, "clickCount": 2},
	}
	for _, s := range steps {
		if _, err := p.client.Call(ctx, p.sessionID, "Page.dispatchMouseEvent", s); err != nil {
			return fmt.Errorf("camoufox: dblclick: %w", err)
		}
	}
	return nil
}

// MouseDown presses the left mouse button at (x, y) without releasing it.
func (p *Page) MouseDown(ctx context.Context, x, y float64) error {
	return p.mouseButton(ctx, "mousedown", x, y, 1)
}

// MouseUp releases the left mouse button at (x, y).
func (p *Page) MouseUp(ctx context.Context, x, y float64) error {
	return p.mouseButton(ctx, "mouseup", x, y, 0)
}

func (p *Page) mouseButton(ctx context.Context, typ string, x, y float64, buttons int) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	_, err := p.client.Call(ctx, p.sessionID, "Page.dispatchMouseEvent", map[string]any{
		"type": typ, "button": 0, "buttons": buttons, "x": x, "y": y, "modifiers": 0, "clickCount": 1,
	})
	if err != nil {
		return fmt.Errorf("camoufox: %s: %w", typ, err)
	}
	return nil
}

// Wheel dispatches a mouse-wheel scroll at (x, y) by (deltaX, deltaY) pixels.
func (p *Page) Wheel(ctx context.Context, x, y, deltaX, deltaY float64) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	_, err := p.client.Call(ctx, p.sessionID, "Page.dispatchWheelEvent", map[string]any{
		"x": x, "y": y, "deltaX": deltaX, "deltaY": deltaY, "deltaZ": 0, "modifiers": 0,
	})
	if err != nil {
		return fmt.Errorf("camoufox: wheel: %w", err)
	}
	return nil
}

// Press sends a key press (keydown + keyup) to the focused element. Key names
// follow the DOM/Playwright convention: "Enter", "Tab", "Escape", "Backspace",
// "ArrowDown", or a single character like "a".
func (p *Page) Press(ctx context.Context, key string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	def := keyDefinition(key)
	down := map[string]any{
		"type": "keydown", "key": def.key, "keyCode": def.keyCode,
		"code": def.code, "location": 0, "repeat": false,
	}
	if def.text != "" {
		down["text"] = def.text
	}
	if _, err := p.client.Call(ctx, p.sessionID, "Page.dispatchKeyEvent", down); err != nil {
		return fmt.Errorf("camoufox: press %q: %w", key, err)
	}
	up := map[string]any{
		"type": "keyup", "key": def.key, "keyCode": def.keyCode,
		"code": def.code, "location": 0, "repeat": false,
	}
	if _, err := p.client.Call(ctx, p.sessionID, "Page.dispatchKeyEvent", up); err != nil {
		return fmt.Errorf("camoufox: press %q: %w", key, err)
	}
	return nil
}

type keyDef struct {
	key     string
	code    string
	keyCode int
	text    string
}

var namedKeys = map[string]keyDef{
	"Enter":      {"Enter", "Enter", 13, "\r"},
	"Tab":        {"Tab", "Tab", 9, ""},
	"Escape":     {"Escape", "Escape", 27, ""},
	"Backspace":  {"Backspace", "Backspace", 8, ""},
	"Delete":     {"Delete", "Delete", 46, ""},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37, ""},
	"ArrowUp":    {"ArrowUp", "ArrowUp", 38, ""},
	"ArrowRight": {"ArrowRight", "ArrowRight", 39, ""},
	"ArrowDown":  {"ArrowDown", "ArrowDown", 40, ""},
	"Home":       {"Home", "Home", 36, ""},
	"End":        {"End", "End", 35, ""},
	"PageUp":     {"PageUp", "PageUp", 33, ""},
	"PageDown":   {"PageDown", "PageDown", 34, ""},
	"Space":      {" ", "Space", 32, " "},
}

// keyDefinition resolves a key name to its Juggler dispatchKeyEvent fields.
func keyDefinition(key string) keyDef {
	if d, ok := namedKeys[key]; ok {
		return d
	}
	if len(key) == 1 {
		r := key[0]
		def := keyDef{key: key, keyCode: int(r), text: key}
		switch {
		case r >= 'a' && r <= 'z':
			def.code = "Key" + strings.ToUpper(key)
			def.keyCode = int(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			def.code = "Key" + key
		case r >= '0' && r <= '9':
			def.code = "Digit" + key
		}
		return def
	}
	// Unknown multi-char key: pass through as-is with no text.
	return keyDef{key: key, code: key}
}
