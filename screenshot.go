package camoufox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// screenshotClip captures a PNG of the given rectangle (CSS pixels).
func (p *Page) screenshotClip(ctx context.Context, x, y, w, h float64) ([]byte, error) {
	res, err := p.client.Call(ctx, p.sessionID, "Page.screenshot", map[string]any{
		"mimeType": "image/png",
		"clip":     map[string]any{"x": x, "y": y, "width": w, "height": h},
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

// ScreenshotFull captures the full scrollable page as PNG bytes.
func (p *Page) ScreenshotFull(ctx context.Context) ([]byte, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	var dim struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	if err := p.EvaluateInto(ctx,
		`({w: Math.max(document.documentElement.scrollWidth, document.body ? document.body.scrollWidth : 0),`+
			` h: Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0)})`,
		&dim); err != nil {
		return nil, err
	}
	if dim.W <= 0 {
		dim.W = 1280
	}
	if dim.H <= 0 {
		dim.H = 720
	}
	return p.screenshotClip(ctx, 0, 0, dim.W, dim.H)
}

// Screenshot captures just this element as PNG bytes.
func (h *ElementHandle) Screenshot(ctx context.Context) ([]byte, error) {
	cctx, cancel := h.page.browser.opCtx(ctx)
	defer cancel()
	if err := h.ScrollIntoView(cctx); err != nil {
		return nil, err
	}
	box, err := h.BoundingBox(cctx)
	if err != nil {
		return nil, err
	}
	return h.page.screenshotClip(cctx, box.X, box.Y, box.Width, box.Height)
}

// Screenshot resolves the locator and captures just that element.
func (l *Locator) Screenshot(ctx context.Context) ([]byte, error) {
	h, err := l.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return h.Screenshot(ctx)
}
