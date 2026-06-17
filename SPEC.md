# camoufox-go — Pure-Go Camoufox SDK — Architecture Spec

> This is the **shared brief** for every agent working on this repo. Read it fully before writing code.
> Module path: `github.com/yx-zero/camoufox-go`  (Go 1.23, target toolchain go1.26.3 on windows/amd64 dev box)

## 1. Goal & HARD constraints (non-negotiable)

Build a **pure-Go SDK** that downloads, configures, launches and drives the **Camoufox** anti-detect
browser (a patched-Firefox fork) entirely from Go — replacing the official Python `camoufox` library
+ Playwright.

Hard constraints — every PR/agent must respect these:

1. **PURE GO.** `CGO_ENABLED=0` MUST build. No cgo. **No bundled Node.js, no Python, no Playwright
   driver, no geckodriver.** The final artifact is a single statically-linked Go binary per OS/arch.
2. **Cross-compiles** to at least: `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/amd64`,
   `darwin/arm64`. CI-style check: `GOOS/GOARCH` matrix `go build` must pass.
3. **Every dependency must be pure Go** (no cgo transitively). See the dependency whitelist (§8).
4. **No stealth loss.** We use the REAL official Camoufox binary (downloaded from daijro's GitHub
   releases). All engine-level stealth (headless undetectability, fingerprint spoofing, anti-graphical
   fingerprinting) lives in that binary and is driven by `CAMOU_CONFIG*` env vars — protocol-agnostic.
   We must reproduce the Python lib's config/fingerprint generation faithfully so stealth is identical.
5. We drive the browser via the **Juggler protocol** (Playwright's Firefox protocol) so the automation
   path matches what Camoufox is built and tested against. Reuse foxbridge's pure-Go Juggler client.

## 2. Control-protocol decision

Camoufox is driven by Python via Playwright's **Juggler** protocol over a **pipe** (FD 3/4, JSON
objects null-byte (`\0`) framed; launched with `-juggler-pipe`). We do the same in pure Go by reusing
**foxbridge's** Juggler backend (it already implements the pipe transport + protocol). A WebDriver BiDi
fallback (also in foxbridge) is acceptable as a secondary backend but Juggler is the default for
maximum fidelity. We do NOT use Playwright's Node driver and we do NOT use Marionette.

## 3. Architecture (layers, bottom→top)

```
 cmd/camoufox            CLI + optional MCP server (thin)
        │
 camoufox  (root pkg)    Public SDK API: Launch(opts) -> *Browser; Browser/Context/Page/Locator
        │
 driver/                 High-level: contexts, pages/targets, navigation lifecycle, frames,
        │                exec contexts, input (mouse/keyboard), cookies, screenshots, network.
        │                (port of playwright-core/src/server/firefox client layer, on top of juggler)
 juggler/                Pure-Go Juggler client: pipe transport (FD3/4, \0-framed JSON), JSON-RPC,
        │                session routing, generated protocol types (Browser/Page/Runtime/Network/Heap).
        │                ADAPTED FROM foxbridge (MIT). May also expose a BiDi backend behind same iface.
        │
 launch/                 Builds the os/exec.Cmd: executable path, args (-juggler-pipe, -headless,
        │                -profile…), env (CAMOU_CONFIG_N chunks), pipe FD wiring, readiness wait.
        │
 config/                 Flattens a Fingerprint into Camoufox's dotted/colon key config_map and
        │                chunks it into CAMOU_CONFIG_1..N env vars (OS-specific chunk sizes).
 fingerprint/           Generates realistic fingerprints (BrowserForge-style + bundled presets):
        │                navigator/screen/webgl/fonts/voices/webrtc/headers/locale/geo. Port from
        │                pythonlib camoufox fingerprints.py + browserforge.
 fetch/                  Binary management: resolve version (CONSTRAINTS), pick release asset per
                         OS/arch from daijro/camoufox GitHub releases, download, verify, extract to
                         cache dir (os.UserCacheDir), resolve executable path. Port from pkgman.py.
```

Supporting: `proxy/`, `locale/`, `geoip/` (optional, can be stubbed initially), `internal/util`.

## 4. Reference projects (clone into ./reference/, gitignored — read & adapt, mind licenses)

| What | Repo | License | Use for |
|---|---|---|---|
| Juggler pipe client (pure Go) | `github.com/VulpineOS/foxbridge` | MIT | **Adapt** `pkg/firefox`, `pkg/backend`, juggler transport + protocol. This is the spine of `juggler/`. |
| Camoufox glue reference | `github.com/ehmo/gomoufox` | MIT | Reference for fingerprint structs, CLI shape, MCP. NOT pure-Go (uses Node) — do not copy its driver. |
| Official Camoufox Python lib | `github.com/daijro/camoufox` (pythonlib/camoufox/*) | MIT | **Port** pkgman.py→fetch, fingerprints.py→fingerprint, utils.py→config, addons/locales/ip/geolocation. |
| Browser binaries + releases | `github.com/daijro/camoufox/releases` | MPL-2.0 (browser) | Download target. We do NOT vendor the browser; we download it at runtime like the Python lib. |
| Playwright Firefox client (TS) | `microsoft/playwright` `packages/playwright-core/src/server/firefox` | Apache-2.0 | Reference for driver/ state machine (frames, nav, input, network). |
| Juggler protocol schema | `microsoft/playwright` `browser_patches/firefox/juggler/protocol/Protocol.js` | MPL-2.0 | Generate Go protocol types from this. |
| playwright-go (API ergonomics) | `github.com/playwright-community/playwright-go` | Apache-2.0/MIT | Reference for public API ergonomics ONLY. Do not depend on it (Node driver). |
| Marionette (fallback ref) | `github.com/njasm/marionette_client` | MIT | Reference only. |

License hygiene: keep a `NOTICES` / `THIRD_PARTY_LICENSES.md`. Adapted MIT code (foxbridge) needs its
copyright + MIT text preserved. Apache/MPL references are read-for-understanding; if any code is copied
verbatim, attribute and preserve license headers.

## 5. Key technical facts (verified during research — agents must re-verify exact values from source)

- **Juggler transport**: launch flag `-juggler-pipe`; browser writes "Juggler listening to the pipe";
  JSON messages over inherited FDs (3=write-to-browser, 4=read-from-browser on Unix; Windows uses
  inherited pipe handles). Framing = JSON objects separated by `\0`. Root session + per-target sessions
  keyed by `sessionId`. Special close message id `-9999` (`kBrowserCloseMessageId`).
- **Juggler domains** (5): `Browser`, `Page`, `Runtime`, `Network`, `Heap`. ~100–150 methods/events.
  Schema: `browser_patches/firefox/juggler/protocol/Protocol.js` (~955 lines).
- **CAMOU_CONFIG**: fingerprint config flattened to dotted/colon keys (e.g. `navigator.userAgent`,
  `screen.width`, `webgl:vendor`, `webrtc:ipv4`, `fonts`, `canvas:seed`, `audio:seed`, `headers.*`).
  Serialized JSON then **chunked** across `CAMOU_CONFIG_1`, `CAMOU_CONFIG_2`, … env vars because of env
  length limits — chunk size ~2047 on Windows, ~32767 elsewhere (VERIFY exact constants in utils.py).
- **Binary download**: `daijro/camoufox` GitHub releases. Asset naming approx
  `camoufox-{version}-{release}-{os}.{arch}.zip` (os ∈ {win,lin,mac}; arch ∈ {x86_64,arm64,i686}).
  Cache dir via platformdirs `user_cache_dir("camoufox")`; in Go use `os.UserCacheDir()/camoufox`.
  Executable name: `camoufox.exe` (win), `camoufox-bin` (lin), `Camoufox.app/Contents/MacOS/camoufox`
  (mac). Version pin in `pythonlib/camoufox/__version__.py` CONSTRAINTS. VERIFY all of this from source.
- **Stealth is in the binary**: do NOT attempt to reimplement fingerprint spoofing in Go. Our job is to
  generate the correct CAMOU_CONFIG and pass it via env. Verify with CreepJS / bot tests at the end.

## 6. Per-module acceptance criteria

- `fetch/`: `Fetch(ctx, opts) (execPath string, err error)` — resolves version, downloads correct asset
  for runtime GOOS/GOARCH (and override), verifies, extracts to cache, idempotent (skip if installed),
  returns executable path. Progress callback. Pure stdlib `archive/zip`; for `.tar.xz`/`.tar.bz2` use
  whitelisted pure-Go libs (§8) only if releases actually use them (verify).
- `fingerprint/`: generate a `Fingerprint` struct (typed) + accept user partial overrides; reproduce the
  Python presets/BrowserForge behavior closely enough that CreepJS shows a consistent realistic FF
  fingerprint. Deterministic given a seed (for reproducibility/testing).
- `config/`: `EnvVars(fp, os) ([]string, error)` producing `CAMOU_CONFIG_N=...` exactly like Python.
  Round-trip test: concatenating chunks → original JSON.
- `juggler/`: launch Camoufox, complete handshake, create context+page, `Navigate`, `Evaluate` JS,
  dispatch mouse+keyboard input, read cookies, take screenshot, observe network requests. Clean shutdown.
- `driver/` + root `camoufox`: ergonomic API, e.g.:
  ```go
  br, _ := camoufox.Launch(ctx, camoufox.Options{Headless: true, Fingerprint: ...})
  pg, _ := br.NewPage(ctx)
  pg.Goto(ctx, "https://abrahamjuliot.github.io/creepjs/")
  txt, _ := pg.Evaluate(ctx, "navigator.userAgent")
  pg.Screenshot(ctx, "out.png")
  br.Close()
  ```
- `cmd/camoufox`: `camoufox fetch`, `camoufox run --url ... --screenshot out.png`, `camoufox version`.

## 7. Build & verification (definition of done)

1. `CGO_ENABLED=0 go build ./...` passes on the dev box.
2. Cross matrix: `GOOS=linux/darwin/windows GOARCH=amd64/arm64 go build ./...` passes.
3. `go vet ./...` clean; basic unit tests for config chunking + fingerprint determinism + fetch asset
   selection (table tests, no network).
4. **End-to-end**: download a real Camoufox, launch headless, navigate to a detection page
   (e.g. CreepJS / bot.sannysoft / browserleaks), evaluate `navigator.webdriver` (must be false/undefined),
   dump UA + a screenshot proving render. Verify no Node/Python process is spawned (process tree check).
5. No transitive cgo: `go list -deps` audit / `CGO_ENABLED=0` clean build is the gate.

## 8. Pure-Go dependency whitelist (add others only if verified cgo-free)

- stdlib: `net/http`, `archive/zip`, `encoding/json`, `os/exec`, `os` (UserCacheDir), `compress/*`.
- xz (if Linux assets are `.tar.xz`): `github.com/ulikunitz/xz` (pure Go).
- websocket (only if BiDi backend used): `github.com/coder/websocket` (pure Go) — Juggler is a pipe, so
  likely NOT needed for the default path.
- Prefer stdlib `encoding/json`. Avoid anything pulling cgo (e.g. no sqlite-cgo, no go-sqlite3).

## 9. Working agreement for parallel agents

- Each agent owns a directory/module; do not edit another agent's files without need.
- Write porting notes/specs you produce into `docs/specs/<topic>.md` so later agents can read them.
- Clone references into `./reference/<name>` (gitignored). Never commit the downloaded browser binary.
- Keep the build green: if you add a package, make sure `go build ./...` still works for the slice you own.
- Preserve license headers on any adapted code; update `THIRD_PARTY_LICENSES.md`.
