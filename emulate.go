package camoufox

import (
	"context"
	"fmt"
)

// SetViewportSize sets the page's viewport (layout) size in CSS pixels.
func (p *Page) SetViewportSize(ctx context.Context, width, height int) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if _, err := p.client.Call(ctx, p.sessionID, "Page.setViewportSize", map[string]any{
		"viewportSize": map[string]any{"width": width, "height": height},
	}); err != nil {
		return fmt.Errorf("camoufox: setViewportSize: %w", err)
	}
	return nil
}

// SetGeolocation overrides the page context's geolocation.
func (p *Page) SetGeolocation(ctx context.Context, latitude, longitude, accuracy float64) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	geo := map[string]any{"latitude": latitude, "longitude": longitude}
	if accuracy > 0 {
		geo["accuracy"] = accuracy
	}
	params := map[string]any{"geolocation": geo}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	if _, err := p.client.Call(ctx, "", "Browser.setGeolocationOverride", params); err != nil {
		return fmt.Errorf("camoufox: setGeolocationOverride: %w", err)
	}
	return nil
}

// firefoxPermission maps web permission names to the Firefox/Juggler internal
// names that Browser.grantPermissions expects (mirrors Playwright's mapping).
var firefoxPermission = map[string]string{
	"geolocation":   "geo",
	"notifications":  "desktop-notification",
	"push":           "desktop-notification",
	"persistent-storage": "persistent-storage",
	"camera":         "camera",
	"microphone":     "microphone",
	"background-sync": "background-sync",
	"midi":           "midi",
	"midi-sysex":     "midi-sysex",
	"clipboard-read":  "clipboard-read",
	"clipboard-write": "clipboard-write",
}

// GrantPermissions grants permissions (e.g. "geolocation", "notifications") to
// an origin for the page's context. Web permission names are translated to the
// Firefox internal names the browser expects.
func (p *Page) GrantPermissions(ctx context.Context, origin string, permissions []string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	mapped := make([]string, 0, len(permissions))
	for _, perm := range permissions {
		if ff, ok := firefoxPermission[perm]; ok {
			mapped = append(mapped, ff)
		} else {
			mapped = append(mapped, perm)
		}
	}
	params := map[string]any{"origin": origin, "permissions": mapped}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	if _, err := p.client.Call(ctx, "", "Browser.grantPermissions", params); err != nil {
		return fmt.Errorf("camoufox: grantPermissions: %w", err)
	}
	return nil
}

// SetOffline toggles network emulation between offline and online.
func (p *Page) SetOffline(ctx context.Context, offline bool) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	mode := "online"
	if offline {
		mode = "offline"
	}
	params := map[string]any{"override": mode}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	if _, err := p.client.Call(ctx, "", "Browser.setOnlineOverride", params); err != nil {
		return fmt.Errorf("camoufox: setOnlineOverride: %w", err)
	}
	return nil
}

// SetColorScheme emulates the prefers-color-scheme media feature ("dark",
// "light" or "no-preference").
func (p *Page) SetColorScheme(ctx context.Context, scheme string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	if _, err := p.client.Call(ctx, p.sessionID, "Page.setEmulatedMedia", map[string]any{
		"colorScheme": scheme,
	}); err != nil {
		return fmt.Errorf("camoufox: setEmulatedMedia: %w", err)
	}
	return nil
}
