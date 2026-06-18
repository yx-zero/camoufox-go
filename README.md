# camoufox-go

Pure-Go SDK for [Camoufox](https://github.com/daijro/camoufox) — the anti-detect Firefox browser.

- **Pure Go. No Node. No Python. No Playwright. Single static binary.**
  `CGO_ENABLED=0 go build` produces one self-contained executable per OS/arch.
- Downloads the **real** Camoufox browser at runtime — all engine-level stealth (headless
  undetectability, fingerprint spoofing, anti-graphical fingerprinting) lives in that binary and is
  driven by the `CAMOU_CONFIG` environment variables, exactly like the official Python library.
- Drives the browser over Playwright's **Juggler** protocol (the protocol Camoufox is built for), so
  the automation path — and its stealth — matches upstream.
- **Playwright-style driver**: locators & element handles, auto-waiting (`networkidle`/selector),
  frames + bounding boxes, network interception, cookies/storage, dialogs, multiple browser contexts,
  emulation, and page events — plus **headless Cloudflare Turnstile** clearance (`Humanize`).
- **Launch options** ported from the Python library: proxy, GeoIP (timezone/locale/geolocation),
  locale, WebGL, addons, fonts, resource blocking, persistent profiles, and more.
- Dependencies are pure Go: `golang.org/x/sys`, plus `oschwald/maxminddb-golang` (used only for the
  optional GeoIP feature). No cgo.

## Install

```sh
go get github.com/yx-zero/camoufox-go        # library
go install github.com/yx-zero/camoufox-go/cmd/camoufox@latest   # CLI
```

Requires Go 1.25+ to build. The compiled binary needs no runtime dependencies.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"os"

	camoufox "github.com/yx-zero/camoufox-go"
)

func main() {
	ctx := context.Background()

	// Downloads & caches the real Camoufox on first use, generates a realistic
	// fingerprint, and launches headless — no Node or Python required.
	br, err := camoufox.Launch(ctx, camoufox.Options{Headless: true, OS: "windows"})
	if err != nil {
		panic(err)
	}
	defer br.Close()

	pg, err := br.NewPage(ctx)
	if err != nil {
		panic(err)
	}
	if err := pg.Goto(ctx, "https://abrahamjuliot.github.io/creepjs/"); err != nil {
		panic(err)
	}

	ua, _ := pg.EvaluateString(ctx, "navigator.userAgent")
	wd, _ := pg.Evaluate(ctx, "navigator.webdriver")
	fmt.Println("user-agent:", ua)
	fmt.Println("webdriver: ", string(wd)) // false

	png, _ := pg.Screenshot(ctx)
	os.WriteFile("out.png", png, 0o644)
}
```

## CLI

```sh
camoufox fetch                 # download & cache the browser
camoufox version               # show the installed browser version
camoufox run -url https://bot.sannysoft.com/ -screenshot shot.png
camoufox run -url https://example.com -os macos -headful
```

## Launch options

```go
br, err := camoufox.Launch(ctx, camoufox.Options{
	Headless:        true,            // recommended; engine-patched to be undetectable
	OS:              "windows",       // "windows" | "macos" | "linux" | "" (random)
	Humanize:        true,            // human-like cursor trajectories (needed for Turnstile)
	FingerprintSeed: 0,               // non-zero => deterministic fingerprint
	FFVersion:       0,               // override Firefox major version (0 = installed)

	// Network / identity
	Proxy:    &camoufox.Proxy{Server: "host:3128", Username: "u", Password: "p"},
	GeoIP:    "auto",                 // spoof tz/locale/geolocation from the (proxy) IP; or an IP
	Timezone: "Europe/London",        // explicit overrides (win over GeoIP)
	Locale:   "en-GB,en",
	WebRTCIP: "203.0.113.7",

	// Profile / behavior
	UserDataDir:   "./profile",       // persistent profile (cookies, cf_clearance, cache)
	DisableCOOP:   true,              // allow clicking cross-origin iframes (Turnstile)
	EnableCache:   true,
	MainWorldEval: true,              // enable "mw:"-prefixed main-world evaluate

	// Fingerprint knobs
	ScreenMaxWidth: 1920, ScreenMaxHeight: 1080,
	WindowWidth: 1280, WindowHeight: 800,
	WebGLVendor: "Google Inc. (Intel)", WebGLRenderer: "ANGLE (Intel, ...)",
	Fonts: []string{"Arial", "Segoe UI"}, CustomFontsOnly: false,

	// Resource blocking
	BlockImages: false, BlockWebRTC: false, BlockWebGL: false,

	// Addons (default uBlock is opt-in)
	DefaultAddons: false, Addons: []string{"/path/to/extracted/addon"},

	// VirtualDisplay: "auto",        // Linux: spawn Xvfb automatically
	// ExecutablePath, CacheDir, Args, Env, UserPrefs, Timeout, Debug, Fingerprint ...
})
defer br.Close()
```

## Driver API

```go
pg, _ := br.NewPage(ctx)
pg.Goto(ctx, url)
pg.WaitForLoadState(ctx, camoufox.LoadStateNetworkIdle)
resp := pg.Response()                            // resp.Status, resp.Headers

// Evaluate
val, _ := pg.Evaluate(ctx, expr)                 // raw JSON
s,   _ := pg.EvaluateString(ctx, expr)
pg.EvaluateInto(ctx, expr, &out)
html, _ := pg.Content(ctx); title, _ := pg.Title(ctx)

// Locators (auto-waiting, pierce open shadow roots)
pg.Locator("#user").Fill(ctx, "alice")
pg.Locator("button[type=submit]").Click(ctx)
txt, _ := pg.Locator(".flash").InnerText(ctx)
pg.WaitForSelector(ctx, "#ready", camoufox.WaitForSelectorOptions{Visible: true})

// Input, waits, navigation
pg.Click(ctx, x, y); pg.Type(ctx, "text"); pg.Press(ctx, "Enter"); pg.Wheel(ctx, x, y, 0, 400)
pg.Reload(ctx); pg.GoBack(ctx); pg.SetContent(ctx, "<h1>hi</h1>")

// Network interception
pg.Route("*/ads/*", func(r *camoufox.Route) { r.Abort("blockedbyclient") })

// Cookies & storage
cs, _ := pg.Cookies(ctx); pg.SetCookies(ctx, cs); pg.ClearCookies(ctx)
st, _ := pg.StorageState(ctx); pg.SetStorageState(ctx, st)

// Events, emulation, screenshots
pg.OnConsole(func(m camoufox.ConsoleMessage) { /* ... */ })
pg.OnDialog(func(d *camoufox.Dialog) { d.Accept(ctx, "") })
pg.SetViewportSize(ctx, 1280, 800); pg.SetColorScheme(ctx, "dark")
full, _ := pg.ScreenshotFull(ctx); el, _ := pg.Locator("h1").Screenshot(ctx)

// Isolated contexts (own cookies/cache; per-context proxy)
c, _ := br.NewContext(ctx, camoufox.ContextOptions{Proxy: &camoufox.Proxy{Server: "host:3128"}})
cp, _ := c.NewPage(ctx); defer c.Close(ctx)
```

### Clearing Cloudflare Turnstile (headless)

```go
br, _ := camoufox.Launch(ctx, camoufox.Options{Headless: true, OS: "windows", Humanize: true, DisableCOOP: true})
pg, _ := br.NewPage(ctx)
pg.Goto(ctx, "https://example-protected-site/")

// The Turnstile iframe is cross-origin inside a closed shadow root — page JS can't
// see it, but the Juggler frame tree can. Read its real geometry and click the box.
f, _ := pg.WaitForFrameByURL(ctx, "challenges.cloudflare.com")
box, _ := f.BoundingBox(ctx)
pg.Click(ctx, box.X+30, box.Y+box.Height/2)      // Humanize makes the click pass
```

The `fetch`, `fingerprint`, `config`, `geoip`, `locale`, `webgl`, and `addons` packages are usable on
their own.

## Verified stealth (Camoufox 135, headless, Windows)

Driving the **real** browser via this pure-Go SDK, against live detectors:

- `navigator.webdriver` → **false**
- **bot.sannysoft.com** — WebDriver (passed), WebDriver Advanced (passed), spoofed WebGL vendor/renderer,
  plugins, languages, and all PHANTOM_* checks pass. (The only "missing" is `window.chrome`, which is
  correct for a genuine Firefox.)
- **CreepJS** — `chromium: false`, **0% headless, 0% stealth** detected; consistent `Segoe UI:Windows`
  platform hints; full fingerprint computed.
- Process audit: **no `node`/`python` process is ever spawned** — the only child process is `camoufox.exe`.

See [docs/E2E_RESULTS.md](docs/E2E_RESULTS.md) for the full run.

## Supported platforms

Cross-compiles (`CGO_ENABLED=0`) to: `windows/amd64`, `windows/arm64`, `linux/amd64`, `linux/arm64`,
`darwin/amd64`, `darwin/arm64`. The browser itself is downloaded for the matching OS/arch.

## How it works

1. **`fetch/`** — resolves the supported Camoufox release from GitHub `daijro/camoufox`, downloads the
   OS/arch asset, extracts to `os.UserCacheDir()/camoufox`, and returns the executable (idempotent).
2. **`fingerprint/`** — picks a real bundled fingerprint preset for the target OS and flattens it into a
   `config.Map` (UA, screen, WebGL, fonts, voices, seeds). Deterministic given a seed.
3. **`config/`** — serializes the map and chunks it into `CAMOU_CONFIG_1..N` (2047 codepoints/var on
   Windows, 32767 elsewhere), byte-for-byte compatible with the Python library.
4. **`launch/`** — spawns the browser with `-juggler-pipe`, injects `CAMOU_CONFIG`, and wires the pipe:
   FD 3/4 on Unix; inherited HANDLEs via `PW_PIPE_READ`/`PW_PIPE_WRITE` on Windows.
5. **`juggler/`** — pure-Go Juggler client (adapted from foxbridge): `\0`-framed JSON over the pipe,
   JSON-RPC with session routing, events.
6. **`camoufox` (root)** — `Browser`/`Context`/`Page` driver: navigation lifecycle, evaluate, locators &
   element handles, waits, frames, network interception, input, dialogs, emulation, events, cookies/
   storage, and screenshots. Optional `geoip`/`locale`/`webgl`/`addons` packages back the launch options.

## Implemented / not yet

**Implemented:** fingerprint presets + per-launch noise, proxy, GeoIP (timezone/locale/geolocation via
MaxMind), locale, WebGL sampling, addons, fonts, screen/window constraints, resource blocking, COOP
toggle, cache, main-world eval, persistent profiles, virtual display (Xvfb), humanize; navigation +
response, evaluate, waits (`load`/`domcontentloaded`/`networkidle`/selector/function/url), locators &
element handles, frames + bounding boxes, network interception, `setExtraHTTPHeaders`, cookies + storage
state + localStorage, init scripts, keyboard/mouse/wheel input, dialogs, multiple contexts (+ per-context
proxy), emulation (viewport/geolocation/permissions/offline/color-scheme), page events
(console/pageerror/popup), full-page + element screenshots, file upload, and headless Cloudflare
Turnstile clearance.

**Not yet:** BrowserForge synthetic-fingerprint generator (real presets are used instead), rich locators
(`getByRole`/`getByText`/`.filter`/`.nth`), strict actionability auto-waits, downloads,
`exposeFunction`, tracing/video/HAR, accessibility snapshots, cross-frame locators, and PDF output
(Firefox/Juggler has no `printToPDF`). Contributions welcome.

## Credits

- **[Camoufox](https://github.com/daijro/camoufox)** by daijro (browser + Python library) — MIT / MPL-2.0.
- **[foxbridge](https://github.com/VulpineOS/foxbridge)** by VulpineOS — MIT. The Juggler pipe transport
  and JSON-RPC client in `juggler/` are adapted from it.
- **[playwright](https://github.com/microsoft/playwright)** — Apache-2.0 / MPL-2.0. Juggler protocol
  schema reference.

See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for license texts and obligations.
