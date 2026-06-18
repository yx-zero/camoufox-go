package camoufox

import "encoding/json"

// jsString returns a JSON-encoded (and thus safely quoted) JavaScript string
// literal for embedding a Go string into an evaluated expression.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
