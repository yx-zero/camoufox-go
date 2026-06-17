package fingerprint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yx-zero/camoufox-go/config"
)

func mustJSON(t *testing.T, m config.Map) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGenerateDeterministic(t *testing.T) {
	a, err := Generate(Options{OS: "windows", FFVersion: 135, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(Options{OS: "windows", FFVersion: 135, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(t, a) != mustJSON(t, b) {
		t.Fatalf("same seed produced different fingerprints:\n%s\n%s", mustJSON(t, a), mustJSON(t, b))
	}
	// Different seed should (almost certainly) differ.
	c, _ := Generate(Options{OS: "windows", FFVersion: 135, Seed: 43})
	if mustJSON(t, a) == mustJSON(t, c) {
		t.Fatal("different seeds produced identical fingerprints")
	}
}

func TestGeneratePerOS(t *testing.T) {
	required := []string{
		"navigator.userAgent", "navigator.platform", "navigator.oscpu",
		"screen.width", "screen.height", "webGl:vendor", "webGl:renderer",
		"fonts", "fonts:spacing_seed", "audio:seed", "canvas:seed",
	}
	for _, os := range []string{"windows", "macos", "linux"} {
		cfg, err := Generate(Options{OS: os, FFVersion: 135, Seed: 7})
		if err != nil {
			t.Fatalf("%s: %v", os, err)
		}
		for _, k := range required {
			if _, ok := cfg[k]; !ok {
				t.Errorf("%s: missing required key %q", os, k)
			}
		}
		plat, _ := cfg["navigator.platform"].(string)
		if got := osFromPlatform(plat); got != os {
			t.Errorf("%s: platform %q maps to %q", os, plat, got)
		}
		// Seeds must be in [1, 2^32-1].
		for _, sk := range []string{"fonts:spacing_seed", "audio:seed", "canvas:seed"} {
			v, _ := cfg[sk].(int64)
			if v < 1 || v > 4294967295 {
				t.Errorf("%s: seed %s out of range: %d", os, sk, v)
			}
		}
		// Marker fonts must be present.
		fonts, _ := cfg["fonts"].([]string)
		set := map[string]bool{}
		for _, f := range fonts {
			set[f] = true
		}
		for _, m := range markerFonts(os) {
			if !set[m] {
				t.Errorf("%s: marker font %q missing from subset", os, m)
			}
		}
	}
}

func TestFFVersionRewrite(t *testing.T) {
	cfg, err := Generate(Options{OS: "windows", FFVersion: 142, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	ua, _ := cfg["navigator.userAgent"].(string)
	if !strings.Contains(ua, "Firefox/142.0") {
		t.Errorf("UA not rewritten to Firefox/142.0: %q", ua)
	}
	if !strings.Contains(ua, "rv:142.0") {
		t.Errorf("UA rv: not rewritten: %q", ua)
	}
}

func TestOverridesAndLocale(t *testing.T) {
	cfg, err := Generate(Options{
		OS: "windows", FFVersion: 135, Seed: 5,
		Timezone: "Europe/London", Locale: "en-GB", WebRTCIP: "203.0.113.9",
		Overrides: config.Map{"navigator.hardwareConcurrency": 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg["timezone"] != "Europe/London" {
		t.Errorf("timezone override failed: %v", cfg["timezone"])
	}
	if cfg["navigator.language"] != "en-GB" || cfg["locale:language"] != "en" || cfg["locale:region"] != "GB" {
		t.Errorf("locale parse wrong: lang=%v l:lang=%v l:region=%v", cfg["navigator.language"], cfg["locale:language"], cfg["locale:region"])
	}
	if cfg["webrtc:ipv4"] != "203.0.113.9" {
		t.Errorf("webrtc override failed: %v", cfg["webrtc:ipv4"])
	}
	if cfg["navigator.hardwareConcurrency"] != 99 {
		t.Errorf("override not applied last: %v", cfg["navigator.hardwareConcurrency"])
	}
}

func TestGenerateFeedsConfigEnvVars(t *testing.T) {
	cfg, err := Generate(Options{OS: "macos", FFVersion: 135, Seed: 11})
	if err != nil {
		t.Fatal(err)
	}
	env, err := config.EnvVars(cfg, "windows")
	if err != nil {
		t.Fatalf("EnvVars: %v", err)
	}
	if len(env) == 0 || !strings.HasPrefix(env[0], "CAMOU_CONFIG_1=") {
		t.Fatalf("unexpected env output: %v", env)
	}
}
