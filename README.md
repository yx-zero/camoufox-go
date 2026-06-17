# camoufox-go

Pure-Go SDK for [Camoufox](https://github.com/daijro/camoufox) — the anti-detect Firefox browser.

- **Pure Go. No Node. No Python. No Playwright. Single static binary.**
  `CGO_ENABLED=0 go build` produces one self-contained executable per OS/arch.
- Downloads the **real** Camoufox browser at runtime — all engine-level stealth (headless
  undetectability, fingerprint spoofing, anti-graphical fingerprinting) lives in that binary and is
  driven by the `CAMOU_CONFIG` environment variables, exactly like the official Python library.
- Drives the browser over Playwright's **Juggler** protocol (the protocol Camoufox is built for), so
  the automation path — and its stealth — matches upstream. The only external dependency is
  `golang.org/x/sys` (pure Go).

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

## API

```go
br, err := camoufox.Launch(ctx, camoufox.Options{
	Headless:       true,        // recommended; engine-patched to be undetectable
	OS:             "windows",   // "windows" | "macos" | "linux" | "" (random)
	Timezone:       "Europe/London",   // optional IANA tz override
	Locale:         "en-GB",           // optional BCP-47 locale
	WebRTCIP:       "203.0.113.7",     // optional WebRTC IPv4 spoof (set this when using a proxy)
	FingerprintSeed: 0,                // non-zero => deterministic fingerprint
	// ExecutablePath, CacheDir, Args, Env, UserPrefs, Timeout, Debug, Fingerprint ...
})

pg, _ := br.NewPage(ctx)
pg.Goto(ctx, url)
val, _ := pg.Evaluate(ctx, expr)        // raw JSON result
s,  _ := pg.EvaluateString(ctx, expr)   // string result
pg.EvaluateInto(ctx, expr, &out)        // unmarshal into out
html, _ := pg.Content(ctx)
title, _ := pg.Title(ctx)
png, _ := pg.Screenshot(ctx)            // PNG bytes
cookies, _ := pg.Cookies(ctx)
pg.Click(ctx, x, y)
pg.Type(ctx, "text")                    // into the focused element
pg.Close(ctx)
br.Close()
```

The `fetch`, `fingerprint`, and `config` packages are usable on their own (binary management,
fingerprint generation, and `CAMOU_CONFIG` encoding respectively).

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
6. **`camoufox` (root)** — `Browser`/`Page` driver: targets, navigation lifecycle, evaluate, input,
   screenshots, cookies.

## Limitations

MVP scope. Not yet implemented: network request interception/fulfillment, multi-frame/iframe helpers,
downloads, workers, persistent contexts, and the BrowserForge synthetic-fingerprint generator (real
presets are used instead). Contributions welcome.

## Credits

- **[Camoufox](https://github.com/daijro/camoufox)** by daijro (browser + Python library) — MIT / MPL-2.0.
- **[foxbridge](https://github.com/VulpineOS/foxbridge)** by VulpineOS — MIT. The Juggler pipe transport
  and JSON-RPC client in `juggler/` are adapted from it.
- **[playwright](https://github.com/microsoft/playwright)** — Apache-2.0 / MPL-2.0. Juggler protocol
  schema reference.

See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for license texts and obligations.
