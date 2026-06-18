// Package fingerprint generates realistic Firefox fingerprints for Camoufox and
// flattens them into the dotted/colon config.Map that the browser consumes.
//
// It is a Go port of camoufox/pythonlib/camoufox/fingerprints.py. We implement
// the "real preset" path (from_preset) rather than the BrowserForge synthetic
// Bayesian-network path: the bundled presets are real fingerprints captured
// from genuine browsers, which give the strongest anti-detection profile and
// avoid statistical-generation artifacts. Hundreds of presets per OS are
// bundled (via go:embed) and one is chosen per identity, giving ample rotation.
package fingerprint

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yx-zero/camoufox-go/config"
)

// Options controls fingerprint generation.
type Options struct {
	// OS selects the operating system family for the fingerprint: "windows",
	// "macos" or "linux". Empty means choose randomly across all three.
	OS string
	// FFVersion is the Firefox major version of the browser that will run the
	// fingerprint (e.g. 135). When non-zero the preset's User-Agent and rv: are
	// rewritten to this version, and the v150+ preset bundle is preferred for
	// versions >= 149. Zero leaves the preset's native version intact.
	FFVersion int
	// Seed makes generation deterministic when non-zero (useful for tests and
	// reproducible identities). Zero draws a fresh random seed each call.
	Seed int64
	// Timezone, when set, overrides the preset timezone (IANA name e.g.
	// "Europe/London").
	Timezone string
	// Locale, when set, is a BCP-47 tag (e.g. "en-US", "zh-Hant-TW") used to set
	// navigator.language and the locale:* properties.
	Locale string
	// WebRTCIP, when set, spoofs the WebRTC public IPv4 (webrtc:ipv4).
	WebRTCIP string
	// ScreenMaxWidth/ScreenMaxHeight, when > 0, constrain preset selection to
	// presets whose screen fits within the given bounds.
	ScreenMaxWidth  int
	ScreenMaxHeight int
	// Overrides are applied last, replacing any generated config keys. Use this
	// for advanced manual property control.
	Overrides config.Map
}

// presetCounter perturbs time-based seeds so rapid successive calls with Seed==0
// still diverge.
var presetCounter int64

// Generate builds a flat config.Map describing one Camoufox identity.
func Generate(opts Options) (config.Map, error) {
	seed := opts.Seed
	if seed == 0 {
		seed = time.Now().UnixNano() ^ int64(uint64(atomic.AddInt64(&presetCounter, 1))*0x9e3779b97f4a7c15)
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // fingerprint variety, not crypto

	preset, err := getRandomPreset(opts.OS, opts.FFVersion, opts.ScreenMaxWidth, opts.ScreenMaxHeight, rng)
	if err != nil {
		return nil, err
	}

	ffStr := ""
	if opts.FFVersion > 0 {
		ffStr = strconv.Itoa(opts.FFVersion)
	}

	cfg := fromPreset(preset, ffStr, rng)

	if opts.Timezone != "" {
		cfg["timezone"] = opts.Timezone
	}
	if opts.Locale != "" {
		applyLocale(cfg, opts.Locale)
	}
	if opts.WebRTCIP != "" {
		cfg["webrtc:ipv4"] = opts.WebRTCIP
	}
	for k, v := range opts.Overrides {
		cfg[k] = v
	}
	return cfg, nil
}

var (
	reFirefoxVer = regexp.MustCompile(`Firefox/\d+\.0`)
	reRvVer      = regexp.MustCompile(`rv:\d+\.0`)
)

// fromPreset converts a real fingerprint preset to the Camoufox config map,
// porting fingerprints.py from_preset().
func fromPreset(p *Preset, ffVersion string, rng *rand.Rand) config.Map {
	cfg := config.Map{}

	nav := p.Navigator
	if nav.UserAgent != "" {
		ua := nav.UserAgent
		if ffVersion != "" {
			ua = reFirefoxVer.ReplaceAllString(ua, "Firefox/"+ffVersion+".0")
			ua = reRvVer.ReplaceAllString(ua, "rv:"+ffVersion+".0")
		}
		cfg["navigator.userAgent"] = ua
	}
	if nav.Platform != "" {
		cfg["navigator.platform"] = nav.Platform
	}
	if nav.HardwareConcurrency != 0 {
		cfg["navigator.hardwareConcurrency"] = nav.HardwareConcurrency
	}
	if nav.OSCPU != "" {
		cfg["navigator.oscpu"] = nav.OSCPU
	} else if nav.Platform != "" {
		// Derive oscpu from platform when the preset omits it.
		switch {
		case nav.Platform == "MacIntel":
			cfg["navigator.oscpu"] = "Intel Mac OS X 10.15"
		case nav.Platform == "Win32":
			cfg["navigator.oscpu"] = "Windows NT 10.0; Win64; x64"
		case strings.Contains(strings.ToLower(nav.Platform), "linux"):
			cfg["navigator.oscpu"] = "Linux x86_64"
		}
	}
	if nav.MaxTouchPoints != nil {
		cfg["navigator.maxTouchPoints"] = *nav.MaxTouchPoints
	}

	sc := p.Screen
	if sc.Width != 0 {
		cfg["screen.width"] = sc.Width
	}
	if sc.Height != 0 {
		cfg["screen.height"] = sc.Height
	}
	if sc.ColorDepth != 0 {
		cfg["screen.colorDepth"] = sc.ColorDepth
		cfg["screen.pixelDepth"] = sc.ColorDepth
	}
	if sc.AvailWidth != 0 {
		cfg["screen.availWidth"] = sc.AvailWidth
	}
	if sc.AvailHeight != 0 {
		cfg["screen.availHeight"] = sc.AvailHeight
	}

	if p.Webgl.UnmaskedVendor != "" {
		cfg["webGl:vendor"] = p.Webgl.UnmaskedVendor
	}
	if p.Webgl.UnmaskedRenderer != "" {
		cfg["webGl:renderer"] = p.Webgl.UnmaskedRenderer
	}

	// Per-launch fingerprint noise seeds (1..2^32-1; 0 is a no-op in the C++ engine).
	cfg["fonts:spacing_seed"] = randSeed(rng)
	cfg["audio:seed"] = randSeed(rng)
	cfg["canvas:seed"] = randSeed(rng)

	if p.Timezone != "" {
		cfg["timezone"] = p.Timezone
	}

	targetOS := osFromPlatform(nav.Platform)
	fonts := generateRandomFontSubset(targetOS, rng)
	if len(fonts) == 0 && len(p.Fonts) > 0 {
		fonts = ensureMarkerFonts(append([]string(nil), p.Fonts...), markerFonts(targetOS))
	}
	cfg["fonts"] = fonts

	voices := generateRandomVoiceSubset(targetOS, rng)
	if len(voices) == 0 && len(p.SpeechVoices) > 0 {
		// speechVoices in presets are "Name:locale:type"; reduce to names.
		voices = voiceNames(p.SpeechVoices)
	}
	if len(voices) > 0 {
		cfg["voices"] = voices
	}

	return cfg
}

// randSeed returns a uniform int in [1, 2^32-1].
func randSeed(rng *rand.Rand) int64 {
	return rng.Int63n(4294967295) + 1
}

// osFromPlatform maps a navigator.platform value to a preset OS key.
func osFromPlatform(platform string) string {
	switch {
	case platform == "MacIntel":
		return "macos"
	case platform == "Win32":
		return "windows"
	case strings.Contains(strings.ToLower(platform), "linux"):
		return "linux"
	default:
		return "macos"
	}
}

// applyLocale parses a BCP-47 tag and sets the locale-related config keys,
// a simplified port of locales.normalize_locale for the common cases.
func applyLocale(cfg config.Map, locale string) {
	parts := strings.Split(strings.ReplaceAll(locale, "_", "-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return
	}
	cfg["navigator.language"] = strings.ReplaceAll(locale, "_", "-")
	cfg["locale:language"] = strings.ToLower(parts[0])
	for _, p := range parts[1:] {
		switch {
		case len(p) == 4: // script subtag, e.g. Hant
			cfg["locale:script"] = strings.Title(strings.ToLower(p)) //nolint:staticcheck
		case len(p) == 2 || (len(p) == 3 && isDigits(p)): // region
			cfg["locale:region"] = strings.ToUpper(p)
		}
	}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ----- preset bundle loading (go:embed) -----

// Preset mirrors one entry in the bundled fingerprint-presets JSON.
type Preset struct {
	Navigator struct {
		UserAgent           string `json:"userAgent"`
		Platform            string `json:"platform"`
		HardwareConcurrency int    `json:"hardwareConcurrency"`
		OSCPU               string `json:"oscpu"`
		MaxTouchPoints      *int   `json:"maxTouchPoints"`
	} `json:"navigator"`
	Screen struct {
		Width            int     `json:"width"`
		Height           int     `json:"height"`
		ColorDepth       int     `json:"colorDepth"`
		AvailWidth       int     `json:"availWidth"`
		AvailHeight      int     `json:"availHeight"`
		DevicePixelRatio float64 `json:"devicePixelRatio"`
	} `json:"screen"`
	Webgl struct {
		UnmaskedVendor   string `json:"unmaskedVendor"`
		UnmaskedRenderer string `json:"unmaskedRenderer"`
	} `json:"webgl"`
	Timezone     string   `json:"timezone"`
	Fonts        []string `json:"fonts"`
	SpeechVoices []string `json:"speechVoices"`
}

type presetBundle struct {
	Presets map[string][]*Preset `json:"presets"`
}

// presetsV150MinFF is the Firefox major version at which the v150 bundle (newer
// real fingerprints) becomes preferred — matches PRESETS_V150_MIN_FF.
const presetsV150MinFF = 149

var (
	baseBundleOnce, v150BundleOnce sync.Once
	baseBundle, v150Bundle         *presetBundle
	baseBundleErr, v150BundleErr   error
)

func loadBundle(ffVersion int) (*presetBundle, error) {
	if ffVersion >= presetsV150MinFF {
		v150BundleOnce.Do(func() {
			v150Bundle, v150BundleErr = parseBundle(presetsV150JSON)
		})
		if v150BundleErr == nil && v150Bundle != nil {
			return v150Bundle, nil
		}
	}
	baseBundleOnce.Do(func() {
		baseBundle, baseBundleErr = parseBundle(presetsBaseJSON)
	})
	return baseBundle, baseBundleErr
}

func parseBundle(data []byte) (*presetBundle, error) {
	var b presetBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("fingerprint: parse preset bundle: %w", err)
	}
	return &b, nil
}

var osToPresetKey = map[string]string{
	"windows": "windows", "win": "windows",
	"macos": "macos", "mac": "macos",
	"linux": "linux", "lin": "linux",
}

// getRandomPreset picks a random preset for the requested OS (or any OS when
// empty), porting get_random_preset(). When maxW/maxH are > 0, only presets
// whose screen fits within those bounds are considered (falling back to the full
// candidate set if none fit).
func getRandomPreset(os string, ffVersion, maxW, maxH int, rng *rand.Rand) (*Preset, error) {
	bundle, err := loadBundle(ffVersion)
	if err != nil {
		return nil, err
	}
	var keys []string
	if os == "" {
		keys = []string{"macos", "windows", "linux"}
	} else {
		k, ok := osToPresetKey[strings.ToLower(os)]
		if !ok {
			return nil, fmt.Errorf("fingerprint: unsupported os %q (want windows|macos|linux)", os)
		}
		keys = []string{k}
	}
	var candidates []*Preset
	for _, k := range keys {
		candidates = append(candidates, bundle.Presets[k]...)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("fingerprint: no presets available for os=%q", os)
	}
	if maxW > 0 || maxH > 0 {
		var fit []*Preset
		for _, p := range candidates {
			if (maxW <= 0 || p.Screen.Width <= maxW) && (maxH <= 0 || p.Screen.Height <= maxH) {
				fit = append(fit, p)
			}
		}
		if len(fit) > 0 {
			candidates = fit
		}
	}
	return candidates[rng.Intn(len(candidates))], nil
}
