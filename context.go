package camoufox

import (
	"context"
	"encoding/json"
	"fmt"
)

// Context is an isolated browser context (separate cookies, storage and cache),
// analogous to Playwright's BrowserContext. Use it to run independent sessions —
// e.g. different proxies or logins — in one browser process.
type Context struct {
	browser *Browser
	id      string
}

// ContextOptions configure a new browser context.
type ContextOptions struct {
	// Proxy routes this context's traffic through a proxy (Browser.setContextProxy).
	Proxy *Proxy
}

// ID returns the Juggler browser-context id.
func (c *Context) ID() string { return c.id }

// NewContext creates an isolated browser context.
func (b *Browser) NewContext(ctx context.Context, opts ContextOptions) (*Context, error) {
	cctx, cancel := b.opCtx(ctx)
	defer cancel()
	res, err := b.client.Call(cctx, "", "Browser.createBrowserContext", map[string]any{
		"removeOnDetach": true,
	})
	if err != nil {
		return nil, fmt.Errorf("camoufox: createBrowserContext: %w", err)
	}
	var r struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, fmt.Errorf("camoufox: createBrowserContext result: %w", err)
	}
	c := &Context{browser: b, id: r.BrowserContextID}
	if opts.Proxy != nil {
		if err := c.applyProxy(cctx, opts.Proxy); err != nil {
			_ = c.Close(context.Background())
			return nil, err
		}
	}
	return c, nil
}

// NewPage opens a new page within this context.
func (c *Context) NewPage(ctx context.Context) (*Page, error) {
	return c.browser.newPageIn(ctx, c.id)
}

// Close removes the context and all its pages.
func (c *Context) Close(ctx context.Context) error {
	cctx, cancel := c.browser.opCtx(ctx)
	defer cancel()
	if _, err := c.browser.client.Call(cctx, "", "Browser.removeBrowserContext", map[string]any{
		"browserContextId": c.id,
	}); err != nil {
		return fmt.Errorf("camoufox: removeBrowserContext: %w", err)
	}
	return nil
}

func (c *Context) applyProxy(ctx context.Context, p *Proxy) error {
	scheme, host, port, err := p.toGeoIP().Parts()
	if err != nil {
		return fmt.Errorf("camoufox: parse proxy: %w", err)
	}
	bypass := []string{}
	for _, h := range splitCommaTrim(p.Bypass) {
		if h != "" {
			bypass = append(bypass, h)
		}
	}
	params := map[string]any{
		"browserContextId": c.id,
		"type":             proxySchemeToType(scheme),
		"host":             host,
		"port":             port,
		"bypass":           bypass,
	}
	if p.Username != "" {
		params["username"] = p.Username
	}
	if p.Password != "" {
		params["password"] = p.Password
	}
	if _, err := c.browser.client.Call(ctx, "", "Browser.setContextProxy", params); err != nil {
		return fmt.Errorf("camoufox: setContextProxy: %w", err)
	}
	return nil
}
