# Third-Party Licenses

This file records license obligations for code referenced or adapted in camoufox-go.

---

## foxbridge

- **Repo**: https://github.com/VulpineOS/foxbridge
- **Module**: `github.com/VulpineOS/foxbridge`
- **License**: MIT (stated in project README; no separate LICENSE file present in repository)
- **Use**: Adapted — Juggler pipe transport, BiDi transport, and protocol client code forms the basis of `juggler/`. Copyright headers must be preserved in any adapted files.
- **Reference clone**: `reference/foxbridge/`

MIT License

Copyright (c) VulpineOS contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

---

## gomoufox

- **Repo**: https://github.com/ehmo/gomoufox
- **Module**: `github.com/ehmo/gomoufox`
- **License**: MIT
- **Use**: Reference only — fingerprint structs, CLI shape, MCP integration patterns. The driver uses Node/Playwright and is NOT copied. Copyright headers must be preserved if any code is adapted.
- **Reference clone**: `reference/gomoufox/`

MIT License

Copyright (c) 2026 Rasty Turek

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

---

## camoufox (Python library)

- **Repo**: https://github.com/daijro/camoufox
- **Path**: `pythonlib/camoufox/`
- **License**: MIT (`pythonlib/pyproject.toml` declares `license = "MIT"`)
- **Use**: Ported logic — `pkgman.py` → `fetch/`, `fingerprints.py` → `fingerprint/`, `utils.py` → `config/`, `addons.py`/`locales.py`/`ip.py`/`geolocation.py` → supporting packages. Copyright headers must be preserved in ported files. The package-level doc comment in `fetch/fetch.go` carries the required attribution.
- **Redistributed data**: the bundled fingerprint data files `fingerprint/data/fingerprint-presets.json`, `fingerprint-presets-v150.json`, `fonts.json`, and `voices.json` are copied verbatim from `pythonlib/camoufox/` and embedded (via `go:embed`) under this same MIT license.
- **Reference clone**: `reference/camoufox/`

MIT License

Copyright (c) daijro and contributors (https://github.com/daijro/camoufox)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

---

## Camoufox Browser Binary

- **Repo**: https://github.com/daijro/camoufox (releases)
- **License**: MPL-2.0 (Firefox-derived browser binary)
- **Use**: Downloaded at runtime to `os.UserCacheDir()/camoufox`. NOT vendored. The binary is NOT modified or redistributed by this SDK.

The full MPL-2.0 license text is available at: https://mozilla.org/MPL/2.0/

---

## Playwright — Juggler Protocol Schema

- **Repo**: https://github.com/microsoft/playwright
- **Paths**:
  - `browser_patches/firefox/juggler/protocol/Protocol.js`
  - `browser_patches/firefox/juggler/protocol/PrimitiveTypes.js`
- **License**: Apache-2.0 (playwright core) / MPL-2.0 (browser patches under `browser_patches/`)
- **Use**: Read-for-understanding to generate Go protocol types in `juggler/`. If any schema definitions are reproduced verbatim in generated code, the Apache-2.0 / MPL-2.0 header must be included.
- **Reference clone**: `reference/juggler-protocol/`

Apache License, Version 2.0 full text: https://www.apache.org/licenses/LICENSE-2.0

---

## Playwright — Firefox Client (TypeScript)

- **Repo**: https://github.com/microsoft/playwright
- **Path**: `packages/playwright-core/src/server/firefox/`
- **License**: Apache-2.0
- **Use**: Reference for `driver/` state machine (frames, navigation, input, network). If any logic is ported verbatim, preserve the Apache-2.0 header.

Apache License, Version 2.0 full text: https://www.apache.org/licenses/LICENSE-2.0
