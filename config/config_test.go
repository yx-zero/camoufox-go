package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// reassemble strips the CAMOU_CONFIG_n= prefixes, orders by n, and concatenates
// the values back into the original JSON string.
func reassemble(t *testing.T, env []string) string {
	t.Helper()
	type chunk struct {
		n int
		v string
	}
	chunks := make([]chunk, 0, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("env entry has no '=': %q", e)
		}
		var n int
		if _, err := fmt.Sscanf(k, "CAMOU_CONFIG_%d", &n); err != nil {
			t.Fatalf("bad env key %q: %v", k, err)
		}
		chunks = append(chunks, chunk{n, v})
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].n < chunks[j].n })
	// keys must be contiguous 1..N
	var sb strings.Builder
	for i, c := range chunks {
		if c.n != i+1 {
			t.Fatalf("non-contiguous chunk numbering: got %d want %d", c.n, i+1)
		}
		sb.WriteString(c.v)
	}
	return sb.String()
}

func TestEnvVarsRoundTrip(t *testing.T) {
	// A long font name list forces the serialized JSON well past the 2047-rune
	// Windows chunk size, exercising multi-chunk splitting.
	fonts := make([]any, 0, 400)
	for i := 0; i < 400; i++ {
		fonts = append(fonts, fmt.Sprintf("Font Family Number %d", i))
	}
	m := Map{
		"navigator.userAgent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0",
		"navigator.platform":  "Win32",
		"screen.width":        1920,
		"screen.height":       1080,
		"webgl:vendor":        "Google Inc. (Intel)",
		"webrtc:ipv4":         "203.0.113.7",
		"canvas:seed":         1234567890,
		"audio:seed":          987654321,
		"fonts":               fonts,
		"headers.User-Agent":  "Mozilla/5.0",
		"some.bool":           true,
		"unicode.name":        "Buenos días — Köln — 日本語フォント",
	}

	for _, host := range []string{"windows", "linux", "darwin"} {
		env, err := EnvVars(m, host)
		if err != nil {
			t.Fatalf("%s: EnvVars error: %v", host, err)
		}
		if len(env) == 0 {
			t.Fatalf("%s: no env vars produced", host)
		}
		// Windows must split into multiple chunks for this payload; others may not.
		if host == "windows" && len(env) < 2 {
			t.Fatalf("windows: expected multiple chunks, got %d", len(env))
		}

		got := reassemble(t, env)
		var back map[string]any
		if err := json.Unmarshal([]byte(got), &back); err != nil {
			t.Fatalf("%s: reassembled JSON invalid: %v\n%s", host, err, got)
		}

		var want map[string]any
		canon, _ := marshalCanonical(m)
		_ = json.Unmarshal(canon, &want)
		if !reflect.DeepEqual(back, want) {
			t.Fatalf("%s: round-trip mismatch\n got: %v\nwant: %v", host, back, want)
		}
	}
}

func TestEnvVarsChunkBoundary(t *testing.T) {
	// Build a value whose JSON length is deterministic, then verify the chunk
	// count matches ceil(len/chunkSize) for the host.
	payload := strings.Repeat("a", 5000)
	m := Map{"k": payload}
	canon, _ := marshalCanonical(m)
	total := len([]rune(string(canon)))

	cases := map[string]int{"windows": 2047, "linux": 32767, "darwin": 32767}
	for host, size := range cases {
		env, err := EnvVars(m, host)
		if err != nil {
			t.Fatal(err)
		}
		want := (total + size - 1) / size
		if len(env) != want {
			t.Fatalf("%s: chunk count = %d, want %d (total runes=%d size=%d)", host, len(env), want, total, size)
		}
	}
}

func TestEnvVarsEmpty(t *testing.T) {
	env, err := EnvVars(Map{}, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0] != "CAMOU_CONFIG_1={}" {
		t.Fatalf("empty map => %v, want [CAMOU_CONFIG_1={}]", env)
	}
	// nil map behaves like empty.
	env, _ = EnvVars(nil, "linux")
	if len(env) != 1 || env[0] != "CAMOU_CONFIG_1={}" {
		t.Fatalf("nil map => %v", env)
	}
}

func TestMergeAndSetDefault(t *testing.T) {
	m := Map{"a": 1}
	m.Merge(Map{"a": 99, "b": 2})
	if m["a"] != 1 || m["b"] != 2 {
		t.Fatalf("Merge clobbered or dropped: %v", m)
	}
	m.SetDefault("a", 5)
	m.SetDefault("c", 3)
	if m["a"] != 1 || m["c"] != 3 {
		t.Fatalf("SetDefault wrong: %v", m)
	}
}

func TestIsDomainSet(t *testing.T) {
	m := Map{"navigator.userAgent": "x", "webrtc:ipv4": "1.2.3.4", "timezone": "UTC"}
	cases := []struct {
		props []string
		want  bool
	}{
		{[]string{"navigator."}, true},
		{[]string{"screen.", "window."}, false},
		{[]string{"webrtc:"}, true},
		{[]string{"timezone"}, true},
		{[]string{"geolocation:"}, false},
		{[]string{"navigator.userAgent"}, true},
		{[]string{"navigator.platform"}, false},
	}
	for _, c := range cases {
		if got := m.IsDomainSet(c.props...); got != c.want {
			t.Errorf("IsDomainSet(%v) = %v, want %v", c.props, got, c.want)
		}
	}
}
