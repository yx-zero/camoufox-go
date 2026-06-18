package camoufox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ElementHandle is a reference to a live DOM element in the page's main frame.
type ElementHandle struct {
	page     *Page
	objectID string
}

// evalHandle evaluates expr (which must return an element or null) in the main
// world and returns a handle. found is false when the expression yields null.
func (p *Page) evalHandle(ctx context.Context, expr string) (*ElementHandle, bool, error) {
	if err := p.waitReady(ctx); err != nil {
		return nil, false, err
	}
	cid := p.executionContext()
	if cid == "" {
		return nil, false, errors.New("camoufox: no execution context")
	}
	res, err := p.client.Call(ctx, p.sessionID, "Runtime.evaluate", map[string]any{
		"executionContextId": cid,
		"expression":         expr,
		"returnByValue":      false,
	})
	if err != nil {
		return nil, false, fmt.Errorf("camoufox: evaluate handle: %w", err)
	}
	var r struct {
		Result *struct {
			ObjectID string `json:"objectId"`
			Subtype  string `json:"subtype"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, false, fmt.Errorf("camoufox: evaluate handle result: %w", err)
	}
	if r.ExceptionDetails != nil {
		return nil, false, fmt.Errorf("camoufox: evaluate handle exception: %s", r.ExceptionDetails.Text)
	}
	if r.Result == nil || r.Result.ObjectID == "" {
		return nil, false, nil
	}
	return &ElementHandle{page: p, objectID: r.Result.ObjectID}, true, nil
}

// callFn invokes fnDecl (a JS function taking the element as its first argument)
// with this handle. When returnByValue is true the function's JSON value is
// returned; otherwise value is nil.
func (h *ElementHandle) callFn(ctx context.Context, fnDecl string, returnByValue bool) (json.RawMessage, error) {
	cid := h.page.executionContext()
	res, err := h.page.client.Call(ctx, h.page.sessionID, "Runtime.callFunction", map[string]any{
		"executionContextId":  cid,
		"functionDeclaration": fnDecl,
		"returnByValue":       returnByValue,
		"args":                []map[string]any{{"objectId": h.objectID}},
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: callFunction: %w", err)
	}
	var r struct {
		Result *struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: callFunction result: %w", err)
	}
	if r.ExceptionDetails != nil {
		return nil, fmt.Errorf("camoufox: callFunction exception: %s", r.ExceptionDetails.Text)
	}
	if r.Result == nil {
		return nil, nil
	}
	return r.Result.Value, nil
}

func (h *ElementHandle) callString(ctx context.Context, fnDecl string) (string, error) {
	v, err := h.callFn(ctx, fnDecl, true)
	if err != nil {
		return "", err
	}
	var s string
	_ = json.Unmarshal(v, &s)
	return s, nil
}

func (h *ElementHandle) callBool(ctx context.Context, fnDecl string) (bool, error) {
	v, err := h.callFn(ctx, fnDecl, true)
	if err != nil {
		return false, err
	}
	var b bool
	_ = json.Unmarshal(v, &b)
	return b, nil
}

// ScrollIntoView scrolls the element into the viewport if needed.
func (h *ElementHandle) ScrollIntoView(ctx context.Context) error {
	_, err := h.page.client.Call(ctx, h.page.sessionID, "Page.scrollIntoViewIfNeeded", map[string]any{
		"frameId":  h.page.mainFrame(),
		"objectId": h.objectID,
	})
	if err != nil {
		return fmt.Errorf("camoufox: scrollIntoView: %w", err)
	}
	return nil
}

// BoundingBox returns the element's rectangle in viewport coordinates.
func (h *ElementHandle) BoundingBox(ctx context.Context) (*BoundingBox, error) {
	res, err := h.page.client.Call(ctx, h.page.sessionID, "Page.getContentQuads", map[string]any{
		"frameId":  h.page.mainFrame(),
		"objectId": h.objectID,
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: getContentQuads: %w", err)
	}
	var qr struct {
		Quads []struct {
			P1, P2, P3, P4 point
		} `json:"quads"`
	}
	if err := json.Unmarshal(res, &qr); err != nil {
		return nil, fmt.Errorf("camoufox: getContentQuads result: %w", err)
	}
	if len(qr.Quads) == 0 {
		return nil, errors.New("camoufox: element has no content quads (not visible?)")
	}
	q := qr.Quads[0]
	minX := min4(q.P1.X, q.P2.X, q.P3.X, q.P4.X)
	maxX := max4(q.P1.X, q.P2.X, q.P3.X, q.P4.X)
	minY := min4(q.P1.Y, q.P2.Y, q.P3.Y, q.P4.Y)
	maxY := max4(q.P1.Y, q.P2.Y, q.P3.Y, q.P4.Y)
	return &BoundingBox{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
}

// Click scrolls the element into view and performs a real mouse click at its
// center (honoring Humanize when enabled).
func (h *ElementHandle) Click(ctx context.Context) error {
	if err := h.ScrollIntoView(ctx); err != nil {
		return err
	}
	box, err := h.BoundingBox(ctx)
	if err != nil {
		return err
	}
	x, y := box.Center()
	return h.page.Click(ctx, x, y)
}

// Hover moves the mouse to the element's center.
func (h *ElementHandle) Hover(ctx context.Context) error {
	if err := h.ScrollIntoView(ctx); err != nil {
		return err
	}
	box, err := h.BoundingBox(ctx)
	if err != nil {
		return err
	}
	x, y := box.Center()
	return h.page.Move(ctx, x, y)
}

// Fill focuses the element and sets its value, dispatching input + change events.
func (h *ElementHandle) Fill(ctx context.Context, value string) error {
	fn := `(el) => {
		el.focus();
		const v = ` + jsString(value) + `;
		const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
		const setter = Object.getOwnPropertyDescriptor(proto, 'value');
		if (setter && setter.set) setter.set.call(el, v); else el.value = v;
		el.dispatchEvent(new Event('input', {bubbles:true}));
		el.dispatchEvent(new Event('change', {bubbles:true}));
	}`
	_, err := h.callFn(ctx, fn, false)
	return err
}

// Focus focuses the element.
func (h *ElementHandle) Focus(ctx context.Context) error {
	_, err := h.callFn(ctx, "(el)=>el.focus()", false)
	return err
}

// Type focuses the element and inserts text via the input pipeline.
func (h *ElementHandle) Type(ctx context.Context, text string) error {
	if err := h.Focus(ctx); err != nil {
		return err
	}
	return h.page.Type(ctx, text)
}

// Check ensures a checkbox/radio is checked.
func (h *ElementHandle) Check(ctx context.Context) error { return h.setChecked(ctx, true) }

// Uncheck ensures a checkbox is unchecked.
func (h *ElementHandle) Uncheck(ctx context.Context) error { return h.setChecked(ctx, false) }

func (h *ElementHandle) setChecked(ctx context.Context, want bool) error {
	cur, err := h.callBool(ctx, "(el)=>!!el.checked")
	if err != nil {
		return err
	}
	if cur != want {
		return h.Click(ctx)
	}
	return nil
}

// SelectOption selects an <option> by value and dispatches a change event.
func (h *ElementHandle) SelectOption(ctx context.Context, value string) error {
	fn := `(el) => {
		const v = ` + jsString(value) + `;
		el.value = v;
		el.dispatchEvent(new Event('input', {bubbles:true}));
		el.dispatchEvent(new Event('change', {bubbles:true}));
	}`
	_, err := h.callFn(ctx, fn, false)
	return err
}

// InnerText returns the element's rendered text.
func (h *ElementHandle) InnerText(ctx context.Context) (string, error) {
	return h.callString(ctx, "(el)=>el.innerText")
}

// TextContent returns the element's textContent.
func (h *ElementHandle) TextContent(ctx context.Context) (string, error) {
	return h.callString(ctx, "(el)=>el.textContent")
}

// InputValue returns an input/textarea/select value.
func (h *ElementHandle) InputValue(ctx context.Context) (string, error) {
	return h.callString(ctx, "(el)=>el.value")
}

// GetAttribute returns an attribute value (empty if absent).
func (h *ElementHandle) GetAttribute(ctx context.Context, name string) (string, error) {
	return h.callString(ctx, "(el)=>el.getAttribute("+jsString(name)+") || ''")
}

// IsVisible reports whether the element is rendered and visible.
func (h *ElementHandle) IsVisible(ctx context.Context) (bool, error) {
	return h.callBool(ctx, `(el)=>{ const r=el.getBoundingClientRect(); const s=getComputedStyle(el);
		return r.width>0 && r.height>0 && s.visibility!=='hidden' && s.display!=='none'; }`)
}

// IsChecked reports a checkbox/radio checked state.
func (h *ElementHandle) IsChecked(ctx context.Context) (bool, error) {
	return h.callBool(ctx, "(el)=>!!el.checked")
}

// SetInputFiles sets the files for an <input type=file> element.
func (h *ElementHandle) SetInputFiles(ctx context.Context, paths []string) error {
	_, err := h.page.client.Call(ctx, h.page.sessionID, "Page.setFileInputFiles", map[string]any{
		"frameId":  h.page.mainFrame(),
		"objectId": h.objectID,
		"files":    paths,
	})
	if err != nil {
		return fmt.Errorf("camoufox: setFileInputFiles: %w", err)
	}
	return nil
}
