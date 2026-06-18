package camoufox

import (
	"context"
	"fmt"
)

// AddInitScript registers JavaScript to run in every new document on this page,
// before the page's own scripts execute (porting Playwright's addInitScript).
// Scripts accumulate; Juggler's setInitScripts replaces the full list each call.
func (p *Page) AddInitScript(ctx context.Context, script string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()

	p.mu.Lock()
	p.initScripts = append(p.initScripts, script)
	scripts := make([]map[string]any, 0, len(p.initScripts))
	for _, s := range p.initScripts {
		scripts = append(scripts, map[string]any{"script": s})
	}
	p.mu.Unlock()

	if _, err := p.client.Call(ctx, p.sessionID, "Page.setInitScripts", map[string]any{
		"scripts": scripts,
	}); err != nil {
		return fmt.Errorf("camoufox: addInitScript: %w", err)
	}
	return nil
}
