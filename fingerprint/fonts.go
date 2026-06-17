package fingerprint

import (
	_ "embed"
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"sync"
)

// Bundled real fingerprint data, copied verbatim from the official camoufox
// Python package (pythonlib/camoufox/*.json). See THIRD_PARTY_LICENSES.md.

//go:embed data/fingerprint-presets.json
var presetsBaseJSON []byte

//go:embed data/fingerprint-presets-v150.json
var presetsV150JSON []byte

//go:embed data/fonts.json
var fontsJSON []byte

//go:embed data/voices.json
var voicesJSON []byte

var (
	osFontsOnce  sync.Once
	osFonts      map[string][]string
	osVoicesOnce sync.Once
	osVoices     map[string][]string
)

func loadOSFonts() map[string][]string {
	osFontsOnce.Do(func() {
		_ = json.Unmarshal(fontsJSON, &osFonts) // keys: win, mac, lin
	})
	return osFonts
}

func loadOSVoices() map[string][]string {
	osVoicesOnce.Do(func() {
		var raw map[string][]string
		_ = json.Unmarshal(voicesJSON, &raw)
		osVoices = make(map[string][]string, len(raw))
		for k, entries := range raw {
			osVoices[k] = voiceNames(entries) // "Name:locale:type" -> "Name"
		}
	})
	return osVoices
}

// voiceNames reduces "Name:locale:type" voice descriptors to bare names.
func voiceNames(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e
		if i := strings.IndexByte(e, ':'); i >= 0 {
			name = e[:i]
		}
		out = append(out, name)
	}
	return out
}

// CreepJS OS-marker fonts: presence/absence of these is used by detectors to
// infer the OS, so they are force-included to match the spoofed platform.
var (
	macMarkerFonts   = []string{"Helvetica Neue", "PingFang HK", "PingFang SC", "PingFang TC"}
	linuxMarkerFonts = []string{"Arimo", "Cousine", "Tinos", "Twemoji Mozilla"}
	winMarkerFonts   = []string{"Segoe UI", "Tahoma", "Cambria Math", "Nirmala UI"}
)

// Essential fonts always present in a subset for a given OS.
var (
	essentialFontsMac = map[string]bool{
		"Arial": true, "Helvetica": true, "Times New Roman": true, "Courier New": true, "Verdana": true,
		"Georgia": true, "Trebuchet MS": true, "Tahoma": true, "Helvetica Neue": true, "Lucida Grande": true,
		"Menlo": true, "Monaco": true, "Geneva": true, "PingFang HK": true, "PingFang SC": true, "PingFang TC": true,
	}
	essentialFontsWin = map[string]bool{
		"Arial": true, "Times New Roman": true, "Courier New": true, "Verdana": true, "Georgia": true,
		"Trebuchet MS": true, "Tahoma": true, "Segoe UI": true, "Calibri": true, "Cambria Math": true,
		"Nirmala UI": true, "Consolas": true,
	}
	essentialFontsLinux = map[string]bool{
		"Arimo": true, "Cousine": true, "Tinos": true, "Twemoji Mozilla": true,
		"Noto Sans Devanagari": true, "Noto Sans JP": true, "Noto Sans KR": true,
		"Noto Sans SC": true, "Noto Sans TC": true,
	}
)

func markerFonts(targetOS string) []string {
	switch targetOS {
	case "windows":
		return winMarkerFonts
	case "linux":
		return linuxMarkerFonts
	default:
		return macMarkerFonts
	}
}

func essentialFonts(targetOS string) map[string]bool {
	switch targetOS {
	case "windows":
		return essentialFontsWin
	case "linux":
		return essentialFontsLinux
	default:
		return essentialFontsMac
	}
}

// generateRandomFontSubset returns a per-identity random subset of the OS font
// list, always including essential + marker fonts, porting
// _generate_random_font_subset(). Returns nil if the font list is unavailable.
func generateRandomFontSubset(targetOS string, rng *rand.Rand) []string {
	osKey := map[string]string{"macos": "mac", "windows": "win", "linux": "lin"}[targetOS]
	if osKey == "" {
		osKey = "mac"
	}
	full := loadOSFonts()[osKey]
	if len(full) == 0 {
		return nil
	}
	essential := essentialFonts(targetOS)

	result := make([]string, 0, len(full))
	nonEssential := make([]string, 0, len(full))
	for _, f := range full {
		if essential[f] {
			result = append(result, f)
		} else {
			nonEssential = append(nonEssential, f)
		}
	}

	// Random 30-78% of the non-essential fonts.
	pct := 30 + int(rng.Float64()*49)
	count := int(math.Round((float64(pct) / 100) * float64(len(nonEssential))))
	result = append(result, sampleStrings(rng, nonEssential, count)...)

	return ensureMarkerFonts(result, markerFonts(targetOS))
}

// Essential speech voices always present in a subset.
var essentialVoicesMac = map[string]bool{
	"Samantha": true, "Alex": true, "Fred": true, "Victoria": true, "Karen": true, "Daniel": true,
}

// generateRandomVoiceSubset ports _generate_random_voice_subset(): macOS gets a
// random 40-80% subset (+ essentials), Windows gets all voices, Linux gets none.
func generateRandomVoiceSubset(targetOS string, rng *rand.Rand) []string {
	osKey := map[string]string{"macos": "mac", "windows": "win", "linux": "lin"}[targetOS]
	if osKey == "" {
		osKey = "mac"
	}
	full := loadOSVoices()[osKey]
	if len(full) == 0 {
		return nil
	}
	if targetOS == "windows" {
		out := make([]string, len(full))
		copy(out, full)
		return out
	}
	// macOS subset.
	result := make([]string, 0, len(full))
	nonEssential := make([]string, 0, len(full))
	for _, v := range full {
		if essentialVoicesMac[v] {
			result = append(result, v)
		} else {
			nonEssential = append(nonEssential, v)
		}
	}
	pct := 40 + int(rng.Float64()*41) // 40-80%
	count := int(math.Round((float64(pct) / 100) * float64(len(nonEssential))))
	return append(result, sampleStrings(rng, nonEssential, count)...)
}

// sampleStrings selects count items from pool without replacement, in random
// order, mirroring random.sample(). If count >= len(pool) the whole pool is
// returned in original order.
func sampleStrings(rng *rand.Rand, pool []string, count int) []string {
	if count <= 0 {
		return nil
	}
	if count >= len(pool) {
		out := make([]string, len(pool))
		copy(out, pool)
		return out
	}
	perm := rng.Perm(len(pool))
	out := make([]string, count)
	for i := 0; i < count; i++ {
		out[i] = pool[perm[i]]
	}
	return out
}

// ensureMarkerFonts appends any missing marker fonts to the list in place,
// porting _ensure_marker_fonts().
func ensureMarkerFonts(fonts, markers []string) []string {
	existing := make(map[string]bool, len(fonts))
	for _, f := range fonts {
		existing[f] = true
	}
	for _, m := range markers {
		if !existing[m] {
			fonts = append(fonts, m)
			existing[m] = true
		}
	}
	return fonts
}
