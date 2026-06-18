package camoufox

import (
	"context"
	"fmt"
)

// Response describes the main-frame document response captured during the most
// recent navigation.
type Response struct {
	URL        string
	Status     int
	StatusText string
	Headers    map[string]string
	RemoteIP   string
	FromCache  bool
}

// headerKV is the Juggler HTTPHeader wire shape ({name, value}).
type headerKV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func headersToMap(hs []headerKV) map[string]string {
	m := make(map[string]string, len(hs))
	for _, h := range hs {
		m[h.Name] = h.Value
	}
	return m
}

// Response returns the captured main-frame document response from the last
// navigation, or nil if none has been observed yet.
func (p *Page) Response() *Response {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastResponse
}

// SetExtraHTTPHeaders sets headers sent with every request from this page.
func (p *Page) SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error {
	ctx, cancel := p.browser.opCtx(ctx)
	defer cancel()
	arr := make([]map[string]string, 0, len(headers))
	for k, v := range headers {
		arr = append(arr, map[string]string{"name": k, "value": v})
	}
	if _, err := p.client.Call(ctx, p.sessionID, "Network.setExtraHTTPHeaders", map[string]any{
		"headers": arr,
	}); err != nil {
		return fmt.Errorf("camoufox: setExtraHTTPHeaders: %w", err)
	}
	return nil
}

// inflightCount reports how many network requests are currently in flight.
func (p *Page) inflightCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inflight)
}
