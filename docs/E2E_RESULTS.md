# End-to-End Stealth Proof

Real run of the pure-Go SDK driving the **actual** Camoufox browser. Date: 2026-06-17.
Host: Windows 11, amd64, Go 1.26.3. Browser: Camoufox `135.0.1-beta.24` (auto-downloaded by `fetch/`).

## What was exercised

1. `fetch.Fetch` downloaded the real Camoufox release (~300 MB) and extracted it to
   `%LocalAppData%\camoufox\browsers\official\135.0.1-beta.24\camoufox.exe`.
2. `camoufox.Launch` generated a fingerprint, injected it via `CAMOU_CONFIG`, spawned the browser
   **headless**, and connected over the Juggler pipe (Windows `PW_PIPE_READ/WRITE` handle inheritance).
3. Navigated to live detectors, evaluated JS, and captured screenshots — all over pure-Go Juggler.

## Results

### Core signals (multiple runs)

| Signal | Value |
|---|---|
| `navigator.webdriver` | **false** |
| `navigator.userAgent` | `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:135.0) Gecko/20100101 Firefox/135.0` |
| `navigator.platform` | matches fingerprint OS (`Win32` / `MacIntel` / `Linux x86_64`) |
| Fingerprint rotation | consecutive launches produced Windows, then macOS, then Linux identities |

### bot.sannysoft.com

All meaningful anti-bot checks **pass**:

- WebDriver (New): **missing (passed)**; WebDriver Advanced: **passed**
- WebGL Vendor: `Google Inc. (Intel)`; Renderer: `ANGLE (Intel, Intel(R) HD Graphics Direct3D11 …)`
  — spoofed and consistent with a Windows machine
- Plugins length 5, PluginArray type passed, Languages `en-US,en`, Broken Image Dimensions 24×24
- PHANTOM_UA / PHANTOM_PROPERTIES / PHANTOM_ETSL / PHANTOM_LANGUAGE: **ok**
- Only red: `Chrome (New): missing` — **expected and correct** for a genuine Firefox (no `window.chrome`).

### CreepJS (https://abrahamjuliot.github.io/creepjs/)

- Full fingerprint computed: FP ID `c11c8a669b94eb05aec477c3ec56285ebad628b582b5b887609600335fc422cc`
- **Headless detection: `chromium: false`, 0% like headless, 0% headless, 0% stealth**
- Platform hints: `Segoe UI:Windows` (consistent with the spoofed Windows fingerprint)
- Note: WebRTC IP and timezone reflected the real host values in this run because `WebRTCIP`,
  `Timezone`, and `Locale` were not set. Set those options (or a proxy + `WebRTCIP`) for full coverage.

### Process purity (the "pure Go" guarantee)

While the browser was held open, a full process-tree audit was run:

- **No `node` or `python` process has any `camoufox` ancestor** — confirmed by walking every process's
  ancestry. The only processes spawned are `camoufox.exe` (the browser and its content/GPU children).
- Source audit: no `import "C"` anywhere; the only non-stdlib dependency is `golang.org/x/sys`
  (pure Go); no `exec.Command` of node/python/playwright/geckodriver exists in the codebase.
- `CGO_ENABLED=0 go build ./...` succeeds; cross-compiles to windows/{amd64,arm64},
  linux/{amd64,arm64}, darwin/{amd64,arm64}.

## Conclusion

The SDK launches and drives the real Camoufox entirely from Go, with **no Node, Python, or Playwright
runtime**, and the browser's engine-level stealth (headless undetectability + fingerprint spoofing) is
fully preserved through pure-Go Juggler automation.

## Reproduce

```sh
CGO_ENABLED=0 go build -o bin/camoufox.exe ./cmd/camoufox
./bin/camoufox.exe run -os windows -url https://bot.sannysoft.com/ -screenshot shot.png
```
