package camoufox

import (
	"encoding/json"
	"strings"
)

// ConsoleMessage is a console.* message emitted by the page.
type ConsoleMessage struct {
	Type string // "log", "warning", "error", "info", ...
	Text string
}

// OnConsole registers a handler for console messages from the page.
func (p *Page) OnConsole(handler func(ConsoleMessage)) {
	p.mu.Lock()
	p.consoleHandler = handler
	p.mu.Unlock()
}

// OnPageError registers a handler for uncaught page JavaScript errors.
func (p *Page) OnPageError(handler func(string)) {
	p.mu.Lock()
	p.pageErrorHandler = handler
	p.mu.Unlock()
}

// OnPopup registers a handler for popups (windows/tabs opened by this page).
// The popup Page is delivered ready to use.
func (p *Page) OnPopup(handler func(*Page)) {
	p.mu.Lock()
	p.popupHandler = handler
	p.mu.Unlock()
}

func (p *Page) onConsole(params json.RawMessage) {
	p.mu.Lock()
	h := p.consoleHandler
	p.mu.Unlock()
	if h == nil {
		return
	}
	var ev struct {
		Type string `json:"type"`
		Args []struct {
			Value json.RawMessage `json:"value"`
			Type  string          `json:"type"`
		} `json:"args"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	parts := make([]string, 0, len(ev.Args))
	for _, a := range ev.Args {
		if len(a.Value) > 0 {
			var s string
			if json.Unmarshal(a.Value, &s) == nil {
				parts = append(parts, s)
			} else {
				parts = append(parts, string(a.Value))
			}
		} else if a.Type != "" {
			parts = append(parts, a.Type)
		}
	}
	h(ConsoleMessage{Type: ev.Type, Text: strings.Join(parts, " ")})
}

func (p *Page) onUncaughtError(params json.RawMessage) {
	p.mu.Lock()
	h := p.pageErrorHandler
	p.mu.Unlock()
	if h == nil {
		return
	}
	var ev struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	h(ev.Message)
}
