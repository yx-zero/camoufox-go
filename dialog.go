package camoufox

import (
	"context"
	"encoding/json"
	"fmt"
)

// Dialog is a JavaScript dialog (alert/confirm/prompt/beforeunload) awaiting a
// decision.
type Dialog struct {
	page         *Page
	id           string
	Type         string // "alert" | "confirm" | "prompt" | "beforeunload"
	Message      string
	DefaultValue string
}

// OnDialog registers a handler for JS dialogs. With no handler, dialogs are
// automatically dismissed (so the page never blocks).
func (p *Page) OnDialog(handler func(*Dialog)) {
	p.mu.Lock()
	p.dialogHandler = handler
	p.mu.Unlock()
}

// Accept accepts the dialog. For prompts, promptText is the entered value (pass
// "" to use the default).
func (d *Dialog) Accept(ctx context.Context, promptText string) error {
	return d.respond(ctx, true, promptText)
}

// Dismiss cancels the dialog.
func (d *Dialog) Dismiss(ctx context.Context) error {
	return d.respond(ctx, false, "")
}

func (d *Dialog) respond(ctx context.Context, accept bool, promptText string) error {
	ctx, cancel := d.page.browser.opCtx(ctx)
	defer cancel()
	params := map[string]any{"dialogId": d.id, "accept": accept}
	if promptText != "" {
		params["promptText"] = promptText
	}
	if _, err := d.page.client.Call(ctx, d.page.sessionID, "Page.handleDialog", params); err != nil {
		return fmt.Errorf("camoufox: handle dialog: %w", err)
	}
	return nil
}

func (p *Page) onDialogOpened(params json.RawMessage) {
	var ev struct {
		DialogID     string `json:"dialogId"`
		Type         string `json:"type"`
		Message      string `json:"message"`
		DefaultValue string `json:"defaultValue"`
	}
	if err := json.Unmarshal(params, &ev); err != nil {
		return
	}
	d := &Dialog{
		page: p, id: ev.DialogID, Type: ev.Type,
		Message: ev.Message, DefaultValue: ev.DefaultValue,
	}
	p.mu.Lock()
	handler := p.dialogHandler
	p.mu.Unlock()

	// Run off the event loop so handlers can issue protocol calls.
	go func() {
		if handler != nil {
			handler(d)
			return
		}
		_ = d.Dismiss(context.Background())
	}()
}
