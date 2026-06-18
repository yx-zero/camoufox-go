package camoufox

import (
	"context"
	"fmt"
)

// SetCookies adds cookies to the page's browser context.
func (p *Page) SetCookies(ctx context.Context, cookies []Cookie) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	arr := make([]map[string]any, 0, len(cookies))
	for _, c := range cookies {
		m := map[string]any{"name": c.Name, "value": c.Value}
		if c.Domain != "" {
			m["domain"] = c.Domain
		}
		if c.Path != "" {
			m["path"] = c.Path
		}
		if c.Expires > 0 {
			m["expires"] = c.Expires
		}
		m["httpOnly"] = c.HTTPOnly
		m["secure"] = c.Secure
		if c.SameSite != "" {
			m["sameSite"] = c.SameSite
		}
		arr = append(arr, m)
	}
	params := map[string]any{"cookies": arr}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	if _, err := p.client.Call(ctx, "", "Browser.setCookies", params); err != nil {
		return fmt.Errorf("camoufox: setCookies: %w", err)
	}
	return nil
}

// ClearCookies removes all cookies from the page's browser context.
func (p *Page) ClearCookies(ctx context.Context) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	params := map[string]any{}
	if p.contextID != "" {
		params["browserContextId"] = p.contextID
	}
	if _, err := p.client.Call(ctx, "", "Browser.clearCookies", params); err != nil {
		return fmt.Errorf("camoufox: clearCookies: %w", err)
	}
	return nil
}

// LocalStorage returns the current origin's localStorage as a map.
func (p *Page) LocalStorage(ctx context.Context) (map[string]string, error) {
	return p.readStorage(ctx, "localStorage")
}

// SessionStorage returns the current origin's sessionStorage as a map.
func (p *Page) SessionStorage(ctx context.Context) (map[string]string, error) {
	return p.readStorage(ctx, "sessionStorage")
}

func (p *Page) readStorage(ctx context.Context, which string) (map[string]string, error) {
	out := map[string]string{}
	err := p.EvaluateInto(ctx, `(() => { const s = `+which+`, o = {};
		for (let i = 0; i < s.length; i++) { const k = s.key(i); o[k] = s.getItem(k); }
		return o; })()`, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetLocalStorage writes key/value pairs into the current origin's localStorage.
func (p *Page) SetLocalStorage(ctx context.Context, kv map[string]string) error {
	for k, v := range kv {
		expr := fmt.Sprintf("localStorage.setItem(%s, %s)", jsString(k), jsString(v))
		if _, err := p.Evaluate(ctx, expr); err != nil {
			return err
		}
	}
	return nil
}

// OriginStorage holds one origin's localStorage for a StorageState.
type OriginStorage struct {
	Origin       string            `json:"origin"`
	LocalStorage map[string]string `json:"localStorage"`
}

// StorageState is a portable snapshot of cookies + localStorage, compatible in
// spirit with Playwright's storageState (single current origin captured).
type StorageState struct {
	Cookies []Cookie        `json:"cookies"`
	Origins []OriginStorage `json:"origins"`
}

// StorageState captures the context cookies and the current page's localStorage.
func (p *Page) StorageState(ctx context.Context) (*StorageState, error) {
	cookies, err := p.Cookies(ctx)
	if err != nil {
		return nil, err
	}
	st := &StorageState{Cookies: cookies}
	origin, _ := p.EvaluateString(ctx, "location.origin")
	if origin != "" && origin != "null" {
		ls, err := p.LocalStorage(ctx)
		if err == nil && len(ls) > 0 {
			st.Origins = append(st.Origins, OriginStorage{Origin: origin, LocalStorage: ls})
		}
	}
	return st, nil
}

// SetStorageState restores cookies and, for the current origin, localStorage.
// Navigate to the target origin before calling to restore its localStorage.
func (p *Page) SetStorageState(ctx context.Context, st *StorageState) error {
	if st == nil {
		return nil
	}
	if len(st.Cookies) > 0 {
		if err := p.SetCookies(ctx, st.Cookies); err != nil {
			return err
		}
	}
	origin, _ := p.EvaluateString(ctx, "location.origin")
	for _, o := range st.Origins {
		if o.Origin == origin {
			if err := p.SetLocalStorage(ctx, o.LocalStorage); err != nil {
				return err
			}
		}
	}
	return nil
}
