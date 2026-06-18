package camoufox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Frame is a frame within a page (the main frame or a sub-frame/iframe).
type Frame struct {
	page     *Page
	ID       string
	URL      string
	Name     string
	ParentID string
}

// BoundingBox is an element/frame rectangle in CSS pixels (viewport coordinates).
type BoundingBox struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// Center returns the box center, convenient for Click.
func (b BoundingBox) Center() (float64, float64) { return b.X + b.Width/2, b.Y + b.Height/2 }

// Frames returns all frames currently known on the page.
func (p *Page) Frames() []*Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Frame, 0, len(p.frames))
	for _, fi := range p.frames {
		out = append(out, &Frame{page: p, ID: fi.id, URL: fi.url, Name: fi.name, ParentID: fi.parentID})
	}
	return out
}

// MainFrame returns the page's main frame.
func (p *Page) MainFrame() *Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	fi := p.frames[p.mainFrameID]
	if fi == nil {
		return &Frame{page: p, ID: p.mainFrameID, URL: p.url}
	}
	return &Frame{page: p, ID: fi.id, URL: fi.url, Name: fi.name, ParentID: fi.parentID}
}

// FrameByURL returns the first frame whose URL contains substr, or nil.
func (p *Page) FrameByURL(substr string) *Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fi := range p.frames {
		if strings.Contains(fi.url, substr) {
			return &Frame{page: p, ID: fi.id, URL: fi.url, Name: fi.name, ParentID: fi.parentID}
		}
	}
	return nil
}

// WaitForFrameByURL waits until a frame whose URL contains substr appears.
// This is the robust way to locate a cross-origin widget (e.g. the Cloudflare
// Turnstile iframe) that page JS cannot see through a closed shadow root.
func (p *Page) WaitForFrameByURL(ctx context.Context, substr string) (*Frame, error) {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	for {
		if f := p.FrameByURL(substr); f != nil {
			return f, nil
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, fmt.Errorf("camoufox: wait for frame %q: %w", substr, err)
		}
	}
}

// BoundingBox returns the frame's owner-element rectangle in the parent document,
// in viewport coordinates. It works even when the owner iframe lives inside a
// closed shadow root (the engine knows the owner element via the frame tree),
// using Juggler's adoptNode + getContentQuads.
func (f *Frame) BoundingBox(ctx context.Context) (*BoundingBox, error) {
	p := f.page
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()

	if f.ParentID == "" {
		return nil, fmt.Errorf("camoufox: BoundingBox is not available for the main frame")
	}

	// The owner element lives in the parent frame; adopt it into the parent's
	// main-world execution context to obtain a handle.
	parentCtx := p.frameContext(f.ParentID)
	if parentCtx == "" {
		return nil, fmt.Errorf("camoufox: no execution context for parent frame")
	}

	adopt, err := p.client.Call(ctx, p.sessionID, "Page.adoptNode", map[string]any{
		"frameId":            f.ID,
		"executionContextId": parentCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: adoptNode: %w", err)
	}
	var ar struct {
		RemoteObject *struct {
			ObjectID string `json:"objectId"`
		} `json:"remoteObject"`
	}
	if err := json.Unmarshal(adopt, &ar); err != nil {
		return nil, fmt.Errorf("camoufox: adoptNode result: %w", err)
	}
	if ar.RemoteObject == nil || ar.RemoteObject.ObjectID == "" {
		return nil, fmt.Errorf("camoufox: frame owner element not found")
	}

	quads, err := p.client.Call(ctx, p.sessionID, "Page.getContentQuads", map[string]any{
		"frameId":  f.ParentID,
		"objectId": ar.RemoteObject.ObjectID,
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: getContentQuads: %w", err)
	}
	var qr struct {
		Quads []struct {
			P1 point `json:"p1"`
			P2 point `json:"p2"`
			P3 point `json:"p3"`
			P4 point `json:"p4"`
		} `json:"quads"`
	}
	if err := json.Unmarshal(quads, &qr); err != nil {
		return nil, fmt.Errorf("camoufox: getContentQuads result: %w", err)
	}
	if len(qr.Quads) == 0 {
		return nil, fmt.Errorf("camoufox: frame has no content quads (not rendered?)")
	}
	q := qr.Quads[0]
	minX := min4(q.P1.X, q.P2.X, q.P3.X, q.P4.X)
	maxX := max4(q.P1.X, q.P2.X, q.P3.X, q.P4.X)
	minY := min4(q.P1.Y, q.P2.Y, q.P3.Y, q.P4.Y)
	maxY := max4(q.P1.Y, q.P2.Y, q.P3.Y, q.P4.Y)
	return &BoundingBox{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}, nil
}

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// frameContext returns the main-world execution context id for a frame, waiting
// briefly for it to be created if necessary.
func (p *Page) frameContext(frameID string) string {
	for i := 0; i < 20; i++ {
		p.mu.Lock()
		ctxID := p.frameCtx[frameID]
		p.mu.Unlock()
		if ctxID != "" {
			return ctxID
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frameCtx[frameID]
}

func min4(a, b, c, d float64) float64 { return minF(minF(a, b), minF(c, d)) }
func max4(a, b, c, d float64) float64 { return maxF(maxF(a, b), maxF(c, d)) }
func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
