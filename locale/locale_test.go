package locale

import (
	"math/rand"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in       string
		wantLang string
		wantReg  string
		wantScr  string
		wantErr  bool
	}{
		{"en-US", "en", "US", "", false},
		{"en_US", "en", "US", "", false},
		{"zh-Hans-CN", "zh", "CN", "Hans", false},
		{"sr-Cyrl-RS", "sr", "RS", "Cyrl", false},
		{"es-419", "es", "419", "", false},
		{"EN-us", "en", "US", "", false},
		{"en", "", "", "", true}, // missing region
		{"", "", "", "", true},
	}

	for _, tc := range tests {
		got, err := Normalize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q): expected error, got %+v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got.Language != tc.wantLang || got.Region != tc.wantReg || got.Script != tc.wantScr {
			t.Errorf("Normalize(%q) = %+v, want lang=%q reg=%q scr=%q",
				tc.in, got, tc.wantLang, tc.wantReg, tc.wantScr)
		}
	}
}

func TestLocaleStringAndConfig(t *testing.T) {
	l := Locale{Language: "en", Region: "US"}
	if l.String() != "en-US" {
		t.Errorf("String() = %q, want en-US", l.String())
	}
	l2 := Locale{Language: "en"}
	if l2.String() != "en" {
		t.Errorf("String() = %q, want en", l2.String())
	}

	cfg := Locale{Language: "zh", Region: "CN", Script: "Hans"}.ConfigKeys()
	if cfg["locale:language"] != "zh" || cfg["locale:region"] != "CN" || cfg["locale:script"] != "Hans" {
		t.Errorf("ConfigKeys() = %+v", cfg)
	}
	noScript := Locale{Language: "en", Region: "US"}.ConfigKeys()
	if _, ok := noScript["locale:script"]; ok {
		t.Errorf("ConfigKeys() should omit locale:script when empty")
	}
}

func TestFromRegionDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	loc, err := FromRegion("US", rng)
	if err != nil {
		t.Fatalf("FromRegion(US): %v", err)
	}
	if loc.Region != "US" {
		t.Errorf("FromRegion(US).Region = %q, want US", loc.Region)
	}
	if loc.Language == "" {
		t.Errorf("FromRegion(US).Language is empty")
	}

	if _, err := FromRegion("ZZZ", rng); err == nil {
		t.Errorf("FromRegion(ZZZ): expected error for unknown territory")
	}
}

func TestFromLanguageDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	loc, err := FromLanguage("en", rng)
	if err != nil {
		t.Fatalf("FromLanguage(en): %v", err)
	}
	if loc.Language != "en" {
		t.Errorf("FromLanguage(en).Language = %q, want en", loc.Language)
	}
	if loc.Region == "" {
		t.Errorf("FromLanguage(en).Region is empty")
	}

	if _, err := FromLanguage("zzz", rng); err == nil {
		t.Errorf("FromLanguage(zzz): expected error for unknown language")
	}
}

func TestHandle(t *testing.T) {
	cfg := map[string]any{}
	if err := Handle([]string{"en-US"}, cfg); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if cfg["locale:language"] != "en" || cfg["locale:region"] != "US" {
		t.Errorf("Handle single: %+v", cfg)
	}
	if _, ok := cfg["locale:all"]; ok {
		t.Errorf("Handle single should not set locale:all")
	}

	cfg2 := map[string]any{}
	if err := Handle([]string{"en-US", "fr-FR", "en-US"}, cfg2); err != nil {
		t.Fatalf("Handle multi: %v", err)
	}
	all, ok := cfg2["locale:all"].(string)
	if !ok || all != "en-US, fr-FR" {
		t.Errorf("Handle multi locale:all = %v, want \"en-US, fr-FR\"", cfg2["locale:all"])
	}
}
