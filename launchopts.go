package camoufox

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/yx-zero/camoufox-go/addons"
	"github.com/yx-zero/camoufox-go/config"
	"github.com/yx-zero/camoufox-go/fetch"
	"github.com/yx-zero/camoufox-go/geoip"
	"github.com/yx-zero/camoufox-go/locale"
	"github.com/yx-zero/camoufox-go/webgl"
)

// Proxy routes all browser traffic through a proxy server. It is applied via the
// Juggler Browser.setBrowserProxy method and, when GeoIP auto-detection is used,
// the public-IP lookup is sent through it.
type Proxy struct {
	// Server is "host:port" or "scheme://host:port" (scheme defaults to http;
	// http, https, socks/socks5 and socks4 are supported).
	Server string
	// Username and Password authenticate to the proxy (optional).
	Username string
	Password string
	// Bypass is a comma-separated list of hosts that skip the proxy (optional).
	Bypass string
}

// cachePrefs caches previous pages and requests (uses more memory), porting
// utils.py CACHE_PREFS.
var cachePrefs = map[string]any{
	"browser.sessionhistory.max_entries":         10,
	"browser.sessionhistory.max_total_viewers":   -1,
	"browser.cache.memory.enable":                true,
	"browser.cache.disk_cache_ssl":               true,
	"browser.cache.disk.smart_size.enabled":      true,
}

// resolveCacheDir returns the shared camoufox cache directory (used for the
// browser binary, GeoIP databases and downloaded addons), matching fetch's
// default of os.UserCacheDir()/camoufox.
func (opts Options) resolveCacheDir() (string, error) {
	if opts.CacheDir != "" {
		return opts.CacheDir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("camoufox: resolve cache dir: %w", err)
	}
	return filepath.Join(base, fetch.CacheDirName), nil
}

// applyLaunchOptions layers all the non-fingerprint launch options onto an
// already-generated config map and returns the Firefox preferences to write.
// It is a port of camoufox/pythonlib/camoufox/utils.py launch_options() — the
// portion that runs after fingerprint generation.
func applyLaunchOptions(cfg config.Map, opts Options, cacheDir string, rng *rand.Rand) (map[string]any, error) {
	prefs := map[string]any{}
	for k, v := range opts.UserPrefs {
		prefs[k] = v
	}

	targetOS := osKeyFromConfig(cfg) // "win" | "mac" | "lin" | ""

	// Random window.history.length (1..5), setdefault so user config wins.
	cfg.SetDefault("window.history.length", rng.Intn(5)+1)

	// Font overrides.
	if len(opts.Fonts) > 0 {
		cfg["fonts"] = opts.Fonts
	}
	if opts.CustomFontsOnly {
		if len(opts.Fonts) == 0 {
			return nil, errors.New("camoufox: CustomFontsOnly requires Fonts to be set")
		}
		prefs["gfx.bundled-fonts.activate"] = 0
	}

	// GeoIP: derive timezone/locale/geolocation (+ WebRTC) from an IP address.
	if opts.GeoIP != "" {
		var gp *geoip.Proxy
		if opts.Proxy != nil {
			gp = opts.Proxy.toGeoIP()
		}
		ip := opts.GeoIP
		if ip == "auto" || ip == "true" {
			detected, err := geoip.PublicIP(gp)
			if err != nil {
				return nil, fmt.Errorf("camoufox: geoip public IP: %w", err)
			}
			ip = detected
		}
		// Spoof WebRTC to the same IP unless WebRTC is blocked.
		if !opts.BlockWebRTC {
			switch {
			case geoip.ValidIPv4(ip):
				cfg["webrtc:ipv4"] = ip
				prefs["network.dns.disableIPv6"] = true
			case geoip.ValidIPv6(ip):
				cfg["webrtc:ipv6"] = ip
			}
		}
		geo, err := geoip.Resolve(ip, opts.GeoIPDB, cacheDir, rng)
		if err != nil {
			return nil, fmt.Errorf("camoufox: geoip resolve: %w", err)
		}
		// GeoIP overrides the preset's timezone/locale/geolocation so the
		// identity matches the proxy's location.
		for k, v := range geo.ConfigKeys() {
			cfg[k] = v
		}
	}

	// Explicit Locale overrides GeoIP (first locale drives the Intl API).
	if opts.Locale != "" {
		locs := splitCommaTrim(opts.Locale)
		if err := locale.Handle(locs, cfg); err != nil {
			return nil, fmt.Errorf("camoufox: locale: %w", err)
		}
	}
	// Explicit timezone / WebRTC IP take final precedence.
	if opts.Timezone != "" {
		cfg["timezone"] = opts.Timezone
	}
	if opts.WebRTCIP != "" {
		cfg["webrtc:ipv4"] = opts.WebRTCIP
	}

	// Main-world script evaluation ("mw:"-prefixed scripts).
	if opts.MainWorldEval {
		cfg.SetDefault("allowMainWorld", true)
	}

	// Resource-blocking and COOP preferences.
	if opts.BlockImages {
		prefs["permissions.default.image"] = 2
	}
	if opts.BlockWebRTC {
		prefs["media.peerconnection.enabled"] = false
	}
	if opts.DisableCOOP {
		prefs["browser.tabs.remote.useCrossOriginOpenerPolicy"] = false
	}

	// WebGL: either disable it, or sample a coherent vendor/renderer + params.
	if opts.BlockWebGL {
		prefs["webgl.disabled"] = true
	} else if targetOS != "" {
		data, webgl2, err := sampleWebGL(cfg, opts, targetOS, rng)
		if err != nil {
			return nil, err
		}
		if data != nil {
			for k, v := range data {
				cfg.SetDefault(k, v) // merge_into: keep preset vendor/renderer
			}
			prefs["webgl.enable-webgl2"] = webgl2
		}
		prefs["webgl.force-enabled"] = true
	}

	// Page/request cache.
	if opts.EnableCache {
		for k, v := range cachePrefs {
			if _, ok := prefs[k]; !ok {
				prefs[k] = v
			}
		}
	}

	// Addons: default addons are opt-in (DefaultAddons); custom dirs always load.
	exclude := opts.ExcludeAddons
	if !opts.DefaultAddons {
		exclude = addons.DefaultList() // exclude everything → download nothing
	}
	addonPaths, err := addons.Resolve(cacheDir, opts.Addons, exclude)
	if err != nil {
		return nil, fmt.Errorf("camoufox: addons: %w", err)
	}
	if len(addonPaths) > 0 {
		cfg["addons"] = addonPaths
	}

	// Fixed window size.
	if opts.WindowWidth > 0 && opts.WindowHeight > 0 {
		applyWindowSize(cfg, opts.WindowWidth, opts.WindowHeight)
	}

	return prefs, nil
}

// sampleWebGL chooses the WebGL fingerprint: an explicit vendor/renderer pair,
// the preset's pair (enriched with matching params), or a random one for the OS.
func sampleWebGL(cfg config.Map, opts Options, targetOS string, rng *rand.Rand) (map[string]any, bool, error) {
	if opts.WebGLVendor != "" && opts.WebGLRenderer != "" {
		data, w2, err := webgl.Sample(targetOS, opts.WebGLVendor, opts.WebGLRenderer, rng)
		if err != nil {
			return nil, false, fmt.Errorf("camoufox: webgl_config: %w", err)
		}
		return data, w2, nil
	}
	if v, ok := cfg["webGl:vendor"].(string); ok && v != "" {
		if r, ok := cfg["webGl:renderer"].(string); ok && r != "" {
			data, w2, err := webgl.Sample(targetOS, v, r, rng)
			if err == nil {
				return data, w2, nil
			}
			// Preset pair not in the WebGL DB — fall back to a random sample.
		}
	}
	data, w2, err := webgl.Sample(targetOS, "", "", rng)
	if err != nil {
		// No WebGL data for this OS: leave the preset vendor/renderer as-is.
		return nil, false, nil
	}
	return data, w2, nil
}

// applyWindowSize sets a fixed outer window size and centers it on the screen,
// a pragmatic port of fingerprints.py handle_window_size for the preset path.
func applyWindowSize(cfg config.Map, w, h int) {
	cfg["window.outerWidth"] = w
	cfg["window.outerHeight"] = h
	cfg["window.innerWidth"] = w
	if inner := h - 74; inner > 0 { // approximate chrome height
		cfg["window.innerHeight"] = inner
	} else {
		cfg["window.innerHeight"] = h
	}
	if sw, ok := toInt(cfg["screen.width"]); ok {
		if x := (sw - w) / 2; x > 0 {
			cfg["window.screenX"] = x
		} else {
			cfg["window.screenX"] = 0
		}
	}
	if sh, ok := toInt(cfg["screen.height"]); ok {
		if y := (sh - h) / 2; y > 0 {
			cfg["window.screenY"] = y
		} else {
			cfg["window.screenY"] = 0
		}
	}
}

// applyProxy configures the browser proxy via the Juggler protocol.
func (b *Browser) applyProxy(ctx context.Context, p *Proxy) error {
	scheme, host, port, err := p.toGeoIP().Parts()
	if err != nil {
		return fmt.Errorf("camoufox: parse proxy: %w", err)
	}
	jugglerType := proxySchemeToType(scheme)
	bypass := []string{}
	for _, h := range splitCommaTrim(p.Bypass) {
		if h != "" {
			bypass = append(bypass, h)
		}
	}
	params := map[string]any{
		"type":   jugglerType,
		"host":   host,
		"port":   port,
		"bypass": bypass,
	}
	if p.Username != "" {
		params["username"] = p.Username
	}
	if p.Password != "" {
		params["password"] = p.Password
	}
	ctx, cancel := b.opCtx(ctx)
	defer cancel()
	if _, err := b.client.Call(ctx, "", "Browser.setBrowserProxy", params); err != nil {
		return fmt.Errorf("camoufox: setBrowserProxy: %w", err)
	}
	return nil
}

func (p *Proxy) toGeoIP() *geoip.Proxy {
	return &geoip.Proxy{
		Server:   p.Server,
		Username: p.Username,
		Password: p.Password,
		Bypass:   p.Bypass,
	}
}

// proxySchemeToType maps a URL scheme to a Juggler setBrowserProxy type.
func proxySchemeToType(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https":
		return "https"
	case "socks", "socks5", "socks5h":
		return "socks"
	case "socks4", "socks4a":
		return "socks4"
	default:
		return "http"
	}
}

// osKeyFromConfig derives the "win"/"mac"/"lin" key from navigator.platform.
func osKeyFromConfig(cfg config.Map) string {
	plat, _ := cfg["navigator.platform"].(string)
	switch {
	case plat == "Win32":
		return "win"
	case plat == "MacIntel":
		return "mac"
	case strings.Contains(strings.ToLower(plat), "linux"):
		return "lin"
	default:
		return ""
	}
}

func splitCommaTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
