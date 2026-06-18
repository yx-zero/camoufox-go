package camoufox

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// routeEntry is a registered interception rule.
type routeEntry struct {
	pattern string
	re      *regexp.Regexp
	handler func(*Route)
}

// Route represents a single intercepted request awaiting a decision. Exactly one
// of Continue/Abort/Fulfill should be called; if none is, the request continues.
type Route struct {
	page      *Page
	RequestID string
	URL       string
	Method    string
	Headers   map[string]string
	handled   bool
}

// Route registers a handler for requests whose URL matches pattern. The pattern
// is a glob ("*" matches any run of characters, "?" any single character); a
// pattern with no wildcards matches as a substring. The handler runs off the
// event loop and must call Continue, Abort or Fulfill (Continue is the default
// if it returns without choosing).
func (p *Page) Route(pattern string, handler func(*Route)) error {
	entry := &routeEntry{pattern: pattern, re: globToRegexp(pattern), handler: handler}
	p.mu.Lock()
	p.routes = append(p.routes, entry)
	needEnable := !p.interceptEnabled
	p.interceptEnabled = true
	p.mu.Unlock()

	if needEnable {
		ctx, cancel := p.browser.opCtx(context.Background())
		defer cancel()
		if _, err := p.client.Call(ctx, p.sessionID, "Network.setRequestInterception", map[string]any{
			"enabled": true,
		}); err != nil {
			return fmt.Errorf("camoufox: enable request interception: %w", err)
		}
	}
	return nil
}

// matchRouteLocked finds the first route matching url. Caller holds p.mu.
func (p *Page) matchRouteLocked(url string) *routeEntry {
	for _, r := range p.routes {
		if r.matches(url) {
			return r
		}
	}
	return nil
}

func (r *routeEntry) matches(url string) bool {
	if r.re != nil {
		return r.re.MatchString(url)
	}
	return strings.Contains(url, r.pattern)
}

// Continue lets the request proceed unchanged.
func (r *Route) Continue() error {
	r.handled = true
	ctx, cancel := r.page.browser.opCtx(context.Background())
	defer cancel()
	if _, err := r.page.client.Call(ctx, r.page.sessionID, "Network.resumeInterceptedRequest", map[string]any{
		"requestId": r.RequestID,
	}); err != nil {
		return fmt.Errorf("camoufox: route continue: %w", err)
	}
	return nil
}

// Abort fails the request. errorCode defaults to "failed".
func (r *Route) Abort(errorCode string) error {
	r.handled = true
	if errorCode == "" {
		errorCode = "failed"
	}
	ctx, cancel := r.page.browser.opCtx(context.Background())
	defer cancel()
	if _, err := r.page.client.Call(ctx, r.page.sessionID, "Network.abortInterceptedRequest", map[string]any{
		"requestId": r.RequestID,
		"errorCode": errorCode,
	}); err != nil {
		return fmt.Errorf("camoufox: route abort: %w", err)
	}
	return nil
}

// Fulfill responds to the request with a synthetic response.
func (r *Route) Fulfill(status int, headers map[string]string, body []byte) error {
	r.handled = true
	hdr := make([]map[string]string, 0, len(headers))
	for k, v := range headers {
		hdr = append(hdr, map[string]string{"name": k, "value": v})
	}
	params := map[string]any{
		"requestId":  r.RequestID,
		"status":     status,
		"statusText": "",
		"headers":    hdr,
	}
	if len(body) > 0 {
		params["base64body"] = base64.StdEncoding.EncodeToString(body)
	}
	ctx, cancel := r.page.browser.opCtx(context.Background())
	defer cancel()
	if _, err := r.page.client.Call(ctx, r.page.sessionID, "Network.fulfillInterceptedRequest", params); err != nil {
		return fmt.Errorf("camoufox: route fulfill: %w", err)
	}
	return nil
}

// globToRegexp compiles a glob pattern to a regexp, or returns nil for a plain
// substring pattern (no wildcards).
func globToRegexp(pattern string) *regexp.Regexp {
	if !strings.ContainsAny(pattern, "*?") {
		return nil
	}
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}
