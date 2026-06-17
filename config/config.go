// Package config builds the flattened Camoufox property map and serializes it
// into the CAMOU_CONFIG_1..N environment variables that the Camoufox browser
// binary reads at startup.
//
// This is a faithful port of camoufox/pythonlib/camoufox/utils.py
// (get_env_vars) — the env-var chunking is byte-for-byte compatible with the
// official library so the downloaded browser binary applies the exact same
// fingerprint patches regardless of which language launched it.
package config

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Map is the flattened Camoufox config: dotted/colon-delimited property keys
// (e.g. "navigator.userAgent", "screen.width", "webgl:vendor", "webrtc:ipv4",
// "fonts", "canvas:seed", "headers.User-Agent") mapped to scalar/array/object
// values. It is the lingua franca between the fingerprint generator and the
// launcher.
type Map map[string]any

// chunkSizeFor returns the per-host-OS CAMOU_CONFIG chunk size, matching
// utils.py: `chunk_size = 2047 if OS_NAME == 'win' else 32767`.
//
// hostOS is the OS the process runs on (runtime.GOOS), NOT the spoofed
// fingerprint OS — the limit is an environment-variable length constraint of
// the host, where 2047 is the safe per-variable ceiling on Windows.
func chunkSizeFor(hostOS string) int {
	if hostOS == "windows" {
		return 2047
	}
	return 32767
}

// EnvVars serializes the config map into CAMOU_CONFIG_1, CAMOU_CONFIG_2, …
// entries in `KEY=VALUE` form (ready for exec.Cmd.Env), exactly like the
// Python library's get_env_vars().
//
// The JSON is split into chunks of at most chunkSizeFor(hostOS) Unicode code
// points (not bytes) — mirroring Python's str slicing — so reconstructing the
// value by concatenating the chunks in numeric order yields the original JSON.
func EnvVars(m Map, hostOS string) ([]string, error) {
	if m == nil {
		m = Map{}
	}
	data, err := marshalCanonical(m)
	if err != nil {
		return nil, fmt.Errorf("config: marshal: %w", err)
	}

	// Slice by rune to match Python's codepoint-based str slicing. The browser
	// concatenates the raw values, so a mid-rune byte split would also round-trip,
	// but rune slicing keeps each env var individually valid UTF-8 and matches
	// the reference implementation precisely.
	runes := []rune(string(data))
	size := chunkSizeFor(hostOS)

	out := make([]string, 0, (len(runes)/size)+1)
	for i, n := 0, 1; i < len(runes); i, n = i+size, n+1 {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, fmt.Sprintf("CAMOU_CONFIG_%d=%s", n, string(runes[i:end])))
	}
	// An empty map serializes to "{}" (2 runes), so the loop always runs at
	// least once and we always emit CAMOU_CONFIG_1 — same as the reference.
	return out, nil
}

// marshalCanonical produces compact JSON with deterministically ordered keys.
// orjson (used by the reference) preserves insertion order; Go sorts map keys.
// Key order is irrelevant to the browser (it parses JSON into a map), but a
// stable order makes our output reproducible and testable.
func marshalCanonical(m Map) ([]byte, error) {
	// json.Marshal already sorts map[string]... keys, but we route through an
	// explicit ordered encode so nested Maps are handled identically and we can
	// guarantee compactness (no spaces), matching orjson.dumps.
	return json.Marshal(orderedValue(m))
}

// orderedValue recursively converts Maps to a stable representation. Plain
// map[string]any (including our Map) is sorted by json.Marshal automatically,
// so this mainly exists to normalize nested Map values to map[string]any.
func orderedValue(v any) any {
	switch t := v.(type) {
	case Map:
		mm := make(map[string]any, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mm[k] = orderedValue(t[k])
		}
		return mm
	case map[string]any:
		return orderedValue(Map(t))
	case []any:
		for i := range t {
			t[i] = orderedValue(t[i])
		}
		return t
	default:
		return v
	}
}

// Merge copies keys from src into dst only when the key is absent in dst,
// porting utils.py merge_into(). Existing values in dst are preserved.
func (dst Map) Merge(src Map) {
	for k, v := range src {
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}
}

// SetDefault sets key=value only if key is not already present, porting
// utils.py set_into().
func (dst Map) SetDefault(key string, value any) {
	if _, ok := dst[key]; !ok {
		dst[key] = value
	}
}

// IsDomainSet reports whether any of the given properties are present in the
// map, porting utils.py is_domain_set(). A property ending in '.' or ':' is
// treated as a domain prefix; otherwise it must match a key exactly.
func (m Map) IsDomainSet(properties ...string) bool {
	for _, prop := range properties {
		if prop == "" {
			continue
		}
		last := prop[len(prop)-1]
		if last == '.' || last == ':' {
			for k := range m {
				if len(k) >= len(prop) && k[:len(prop)] == prop {
					return true
				}
			}
		} else if _, ok := m[prop]; ok {
			return true
		}
	}
	return false
}
