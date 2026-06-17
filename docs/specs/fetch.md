# fetch/ — Browser Binary Download & Management Spec

> Porting target: `reference/camoufox/pythonlib/camoufox/pkgman.py` +
> `__version__.py` + `repos.yml` + `multiversion.py`
> Author: fetch-spec agent | camoufox-go

---

## 1. GitHub Organisation, Repo, and Release API

**Primary repo:** `daijro/camoufox`
**Fallback repo:** `camoufox/camoufox`

These are listed as a comma-separated string in `repos.yml`:

```yaml
repo: daijro/camoufox, camoufox/camoufox
```

`pkgman.py` splits on comma and tries each in order:

```python
repos = [r.strip() for r in raw_repo.split(',')] if isinstance(raw_repo, str) else raw_repo
```

**Releases API endpoint (per repo):**

```
GET https://api.github.com/repos/{owner}/{repo}/releases
```

Response: JSON array of release objects. Each release object has:
- `prerelease` (bool)
- `assets` (array): each asset has `name`, `browser_download_url`, `id`, `size`, `updated_at`

Optional auth header (from `GITHUB_TOKEN` env var):
```python
headers = {"Authorization": f"Bearer {GITHUB_TOKEN}"} if GITHUB_TOKEN else {}
```

In Go: read `os.Getenv("GITHUB_TOKEN")`.

---

## 2. Version Pin and CONSTRAINTS Logic

### 2a. Hard-coded constraints (`__version__.py`)

```python
class CONSTRAINTS:
    MIN_VERSION = 'alpha.1'
    MAX_VERSION = '1'
```

`VERSION_MIN = Version(build='alpha.1')`
`VERSION_MAX = Version(build='1')`

Any installed or candidate build must satisfy: `VERSION_MIN <= version < VERSION_MAX`.

The `Version.is_supported()` method encodes this:

```python
def is_supported(self) -> bool:
    return VERSION_MIN <= self < VERSION_MAX
```

### 2b. Per-release channel constraints (`repos.yml` + `pkgman.py`)

`repos.yml` contains a `versions` list under each browser entry. Each entry maps a `python_library` semver range to a `browser` constraint block with separate `stable` and `prerelease` sub-blocks:

```yaml
versions:
  - python_library:
      min: "0.5.0"
      max: "1"
    browser:
      stable:
        min: "beta.19"
        max: "1"
      # prerelease: absent = unconstrained
```

**Version constraint resolution:**

`_find_version_constraints(versions, library_version)` iterates entries and finds the one whose `python_library.min <= lib_version < python_library.max`. If none match (e.g. source checkout `0.0.0`), the entry with the highest `min` is used as fallback.

In Go, the SDK version string should be embedded at build time (ldflags or a `version.go` constant). When the Go SDK version is unknown or zero, fall back to the newest entry.

`_channel_bounds()` handles both the flat `{min, max}` form and the `{stable: {min, max}, prerelease: {min, max}}` per-channel form. A missing bound means unconstrained.

`RepoConfig.is_version_supported(version, is_prerelease)` enforces bounds per channel:

```python
def is_version_supported(self, version: 'Version', is_prerelease: bool = False) -> bool:
    if is_prerelease:
        build_min, build_max = self.prerelease_min, self.prerelease_max
    else:
        build_min, build_max = self.stable_min, self.stable_max
    if build_min is None or build_max is None:
        return True
    return Version(build=build_min) <= version <= Version(build=build_max)
```

**Alpha detection:** builds whose first `.`-separated component is `"alpha"` (case-insensitive) are always treated as prerelease regardless of the GitHub `prerelease` flag:

```python
@property
def is_alpha(self) -> bool:
    return self.build.split('.')[0].lower() == 'alpha'
```

### 2c. Version comparison (`Version` struct)

Version builds are split on `.` and each component is compared as either int (if all digits) or `ord(c[0]) - 1024` for alpha components, padded to 5 parts:

```python
self.sorted_rel = tuple(
    [
        *(int(x) if x.isdigit() else ord(x[0]) - 1024 for x in self.build.split('.')),
        *(0 for _ in range(5 - self.build.count('.'))),
    ]
)
```

In Go, implement as `[5]int` comparison. Non-numeric component: `int(rune(s[0])) - 1024`.

---

## 3. OS Detection

Python uses `sys.platform`. The mapping is:

```python
OS_MAP: Dict[str, Literal['mac', 'win', 'lin']] = {
    'darwin': 'mac',
    'linux': 'lin',
    'win32': 'win',
}
```

In Go, use `runtime.GOOS`:

| `runtime.GOOS` | Camoufox OS token |
|---|---|
| `darwin` | `mac` |
| `linux` | `lin` |
| `windows` | `win` |

Any other GOOS → return `ErrUnsupportedOS`.

---

## 4. Architecture Map

```python
ARCH_MAP: Dict[str, str] = {
    'amd64':   'x86_64',
    'x86_64':  'x86_64',
    'x86':     'x86_64',
    'i686':    'i686',
    'i386':    'i686',
    'arm64':   'arm64',
    'aarch64': 'arm64',
    'armv5l':  'arm64',
    'armv6l':  'arm64',
    'armv7l':  'arm64',
}
```

In Go, use `runtime.GOARCH`:

| `runtime.GOARCH` | Camoufox arch token |
|---|---|
| `amd64` | `x86_64` |
| `386` | `i686` |
| `arm64` | `arm64` |

Any other GOARCH → `ErrUnsupportedArch`.

**Valid OS/arch combinations (`OS_ARCH_MATRIX`):**

```python
OS_ARCH_MATRIX: Dict[str, List[str]] = {
    'win': ['x86_64', 'i686'],
    'mac': ['x86_64', 'arm64'],
    'lin': ['x86_64', 'arm64', 'i686'],
}
```

Validate: if the mapped arch is not in `OS_ARCH_MATRIX[osToken]`, return `ErrUnsupportedArch`.

---

## 5. Release Asset Filename Pattern

From `repos.yml`:

```yaml
pattern: "{name}-{version}-{build}-{os}.{arch}.zip"
```

`pkgman.py` builds a regex from this pattern by substituting:

```python
replacements = {
    'name':    r'(?P<name>\w+)',
    'version': r'(?P<version>[^-]+)',
    'build':   r'(?P<build>[^-]+)',
    'os':      re.escape(os_token),    # e.g. 'win', 'lin', 'mac'
    'arch':    re.escape(arch_token),  # e.g. 'x86_64', 'arm64', 'i686'
}
# dots in the pattern template are escaped first:
pattern = self.pattern.replace('.', r'\.')
```

**Concrete filename examples:**

```
camoufox-134.0.2-beta.20-win.x86_64.zip
camoufox-134.0.2-beta.20-lin.x86_64.zip
camoufox-134.0.2-beta.20-lin.arm64.zip
camoufox-134.0.2-beta.20-mac.x86_64.zip
camoufox-134.0.2-beta.20-mac.arm64.zip
camoufox-134.0.2-beta.20-win.i686.zip
camoufox-134.0.2-beta.20-lin.i686.zip
```

Named capture groups extracted from the match:
- `name` → `camoufox`
- `version` → e.g. `134.0.2`
- `build` → e.g. `beta.20`

**Archive format:** ALL assets use `.zip`. The Python code uses `zipfile.ZipFile` exclusively; there is no `.tar.xz` or `.tar.bz2` path in the current release structure. In Go, use `archive/zip` from stdlib. The `github.com/ulikunitz/xz` dependency (whitelisted in SPEC §8) is NOT required unless a future release adds `.tar.xz` assets — verify at implementation time.

---

## 6. Cache Directory

Python:

```python
from platformdirs import user_cache_dir
INSTALL_DIR: Path = Path(user_cache_dir("camoufox"))
```

Go equivalent:

```go
base, err := os.UserCacheDir()  // e.g. ~/.cache on Linux, ~/Library/Caches on macOS, %LocalAppData% on Windows
installDir := filepath.Join(base, "camoufox")
```

---

## 7. Post-Extract Directory Layout

After extraction, `multiversion.py` installs into a versioned subdirectory:

```
{INSTALL_DIR}/
  .0.5_FLAG                          # compatibility sentinel (touch after install)
  config.json                        # active version + channel config
  repo_cache.json                    # cached available versions list
  browsers/
    {repo_name}/                     # e.g. "official"
      {version}-{build}/             # e.g. "134.0.2-beta.20"
        version.json                 # install metadata (see §8)
        camoufox.exe                 # (Windows)
        camoufox-bin                 # (Linux)
        Camoufox.app/                # (macOS)
          Contents/
            MacOS/
              camoufox               # macOS executable
            Resources/
              ...
        <other browser files>
```

The `repo_name` is derived from the GitHub repo slug:

```python
def get_repo_name(github_repo: str) -> str:
    for repo in RepoConfig.load_repos():
        if github_repo in repo.repos:
            return repo.name.lower()   # e.g. "official"
    return github_repo.split('/')[0].lower()
```

For `daijro/camoufox` or `camoufox/camoufox`, `repo_name = "official"`.

The `version_folder` is `"{version}-{build}"`, e.g. `"134.0.2-beta.20"`.

The zip archive extracts its contents directly into `install_path` (no wrapping top-level directory assumed — `unzip(temp_file, str(install_path))`).

---

## 8. Executable Path Per OS

```python
LAUNCH_FILE = {
    'win': 'camoufox.exe',
    'mac': '../MacOS/camoufox',
    'lin': 'camoufox-bin',
}
```

`launch_path()` resolves:

```python
def launch_path(browser_path: Optional[Path] = None) -> str:
    if browser_path:
        if OS_NAME == 'mac':
            exec_path = os.path.abspath(
                browser_path / 'Camoufox.app' / 'Contents' / 'Resources' / LAUNCH_FILE[OS_NAME]
            )
        else:
            exec_path = str(browser_path / LAUNCH_FILE[OS_NAME])
    else:
        exec_path = get_path(LAUNCH_FILE[OS_NAME])
```

Note: `LAUNCH_FILE['mac']` is `'../MacOS/camoufox'` — this is relative to `Contents/Resources/`, so the `os.path.abspath(browser_path / 'Camoufox.app' / 'Contents' / 'Resources' / '../MacOS/camoufox')` resolves to `Camoufox.app/Contents/MacOS/camoufox`.

**Go constants:**

| OS token | Executable relative to install dir |
|---|---|
| `win` | `camoufox.exe` |
| `lin` | `camoufox-bin` |
| `mac` | `Camoufox.app/Contents/MacOS/camoufox` |

On Linux/macOS, after extraction the implementation must `chmod 755` the install directory recursively (Python does `os.system(f'chmod -R 755 ...')`). In Go use `filepath.WalkDir` + `os.Chmod`.

---

## 9. Installed Version Tracking and Verification

### version.json

Written by `install_versioned()` immediately after extraction:

```python
metadata = {
    'version':          fetcher.version,         # e.g. "134.0.2"
    'build':            fetcher.build,            # e.g. "beta.20"
    'prerelease':       fetcher.is_prerelease,    # bool
    'asset_id':         asset.get('id'),          # int or null
    'asset_size':       asset.get('size'),        # int or null
    'asset_updated_at': asset.get('updated_at'),  # RFC3339 string or null
}
```

`Version.from_path()` reads it back:

```python
with open(version_path, 'rb') as f:
    version_data = orjson.loads(f.read())
    if 'release' in version_data:         # legacy key alias
        version_data['build'] = version_data.pop('release')
    elif 'tag' in version_data:           # legacy key alias
        version_data['build'] = version_data.pop('tag')
    return Version(
        build=version_data['build'],
        version=version_data.get('version'),
    )
```

### config.json

Tracks the active install:

```json
{
  "active_version": "browsers/official/134.0.2-beta.20",
  "channel": "official/stable",
  "pinned": null
}
```

### Idempotency check

Before downloading, `install_versioned()` checks:

```python
if install_path.exists() and (install_path / 'version.json').exists():
    # Already installed — skip unless replace=True
```

In Go: `Fetch` must stat `{installPath}/version.json`; if present and valid, skip download and return the executable path immediately.

### Compatibility sentinel

After a successful install, touch `{INSTALL_DIR}/.0.5_FLAG`. On entry to `camoufox_path()`, if `INSTALL_DIR` exists and is non-empty but the flag is absent, the old layout is considered incompatible and the directory is wiped.

### Version support check on load

`camoufox_path()` calls `Version.from_path(active).is_supported()` before returning a path. If the installed version is outside `[VERSION_MIN, VERSION_MAX)`, trigger a re-fetch.

---

## 10. Go API to Implement

### Package: `github.com/yx-zero/camoufox-go/fetch`

```go
package fetch

import (
    "context"
    "io"
)

// ProgressFunc is called periodically during download.
// downloaded and total are in bytes; total may be 0 if Content-Length is absent.
type ProgressFunc func(downloaded, total int64)

// Options controls the Fetch operation.
type Options struct {
    // ForceOS overrides runtime.GOOS detection (use Camoufox token: "win"/"lin"/"mac").
    ForceOS string
    // ForceArch overrides runtime.GOARCH detection (use Camoufox token: "x86_64"/"arm64"/"i686").
    ForceArch string
    // CacheDir overrides the default cache directory (os.UserCacheDir()/camoufox).
    CacheDir string
    // IncludePrerelease includes prerelease/alpha builds in version resolution.
    IncludePrerelease bool
    // Replace forces re-download even if a matching version is already installed.
    Replace bool
    // GitHubToken is passed as Bearer auth to GitHub API (falls back to GITHUB_TOKEN env var).
    GitHubToken string
    // Progress is called during download if non-nil.
    Progress ProgressFunc
}

// Fetch resolves the latest supported Camoufox version for the current (or overridden)
// OS/arch, downloads it from GitHub releases if not already cached, extracts it to the
// cache directory, and returns the absolute path to the browser executable.
// It is idempotent: if a valid install already exists it returns immediately.
func Fetch(ctx context.Context, opts Options) (execPath string, err error)

// ExecPath returns the absolute path to the Camoufox executable for an already-installed
// version without triggering a download. Returns ErrNotInstalled if absent.
func ExecPath(opts Options) (string, error)

// InstalledVersion returns the Version for the currently active install, or error if absent.
func InstalledVersion(opts Options) (*Version, error)

// ListAvailable fetches all available versions from GitHub that match the current
// (or overridden) OS/arch and fall within the CONSTRAINTS range.
func ListAvailable(ctx context.Context, opts Options) ([]AvailableVersion, error)

// Remove deletes the installation at the given version path.
func Remove(installPath string) error

// Version holds parsed build metadata.
type Version struct {
    Version    string // Firefox version string, e.g. "134.0.2"
    Build      string // Build/channel string, e.g. "beta.20"
    Prerelease bool
}

// FullString returns "{Version}-{Build}", e.g. "134.0.2-beta.20".
func (v Version) FullString() string

// IsSupported reports whether the build falls within [MIN_VERSION, MAX_VERSION).
func (v Version) IsSupported() bool

// AvailableVersion describes a downloadable release asset.
type AvailableVersion struct {
    Version     Version
    URL         string
    Prerelease  bool
    AssetID     int64
    AssetSize   int64
    AssetUpdatedAt string // RFC3339
}

// Sentinel errors.
var (
    ErrNotInstalled     = errors.New("camoufox is not installed; run Fetch first")
    ErrUnsupportedOS    = errors.New("unsupported OS")
    ErrUnsupportedArch  = errors.New("unsupported architecture")
    ErrNoMatchingRelease = errors.New("no matching release found for this OS/arch/version")
    ErrOutdated         = errors.New("installed camoufox version is outdated")
)
```

### Internal helpers (unexported)

```go
// mapOS converts runtime.GOOS → "win"/"lin"/"mac".
func mapOS(goos string) (string, error)

// mapArch converts runtime.GOARCH → "x86_64"/"arm64"/"i686".
func mapArch(goarch string) (string, error)

// buildAssetPattern returns a *regexp.Regexp matching asset filenames for the given os/arch.
// Pattern template from repos.yml: "{name}-{version}-{build}-{os}.{arch}.zip"
func buildAssetPattern(osToken, archToken string) *regexp.Regexp

// parseVersion converts a build string into a comparable [5]int representation.
func parseVersion(build string) [5]int

// downloadAndExtract downloads url into a temp file, then extracts the zip into destDir.
func downloadAndExtract(ctx context.Context, url, destDir string, token string, progress ProgressFunc) error

// writeVersionJSON writes version.json into installPath.
func writeVersionJSON(installPath string, v AvailableVersion) error

// readVersionJSON reads and parses version.json from path.
func readVersionJSON(path string) (*Version, error)

// chmodExec chmod -R 755 on non-Windows installs.
func chmodExec(path string) error

// activeConfigPath returns the path to config.json.
func activeConfigPath(cacheDir string) string

// resolveExecPath returns the absolute path to the executable given the install dir and os token.
func resolveExecPath(installDir, osToken string) string
```

### Cache layout Go constants

```go
const (
    CacheDirName    = "camoufox"
    BrowsersDirName = "browsers"
    VersionFileName = "version.json"
    ConfigFileName  = "config.json"
    CompatFlagName  = ".0.5_FLAG"

    // Executable names per OS token
    ExecWin = "camoufox.exe"
    ExecLin = "camoufox-bin"
    ExecMac = "Camoufox.app/Contents/MacOS/camoufox"
)
```

### CONSTRAINTS Go constants

```go
const (
    ConstraintsMinVersion = "alpha.1"
    ConstraintsMaxVersion = "1"
)
```

---

## 11. Implementation Notes

1. **No CGO.** Use `archive/zip` (stdlib) for extraction. Do NOT use `os/exec` to call `unzip`.
2. **HTTP.** Use `net/http` stdlib. Stream response body to a `*os.File` temp file (not `bytes.Buffer`) to avoid OOM on large downloads.
3. **Atomic install.** Extract to a temp dir under `BROWSERS_DIR`, then `os.Rename` into the final path. On failure, clean up the temp dir.
4. **Fallback repos.** Try `daijro/camoufox` first, then `camoufox/camoufox`. On any HTTP/parse error from the first repo, continue to next.
5. **Asset scanning.** Iterate all releases from the API response. For each release, iterate assets. Match with the compiled `*regexp.Regexp`. Skip assets whose parsed `Version` falls outside constraints. Take the first match (API returns newest-first).
6. **Alpha builds.** If `build.split('.')[0] == "alpha"` (case-insensitive), treat as prerelease regardless of GitHub `prerelease` flag.
7. **chmod.** On Linux/macOS, after extraction walk `installPath` and `os.Chmod(p, 0755)` all entries.
8. **Idempotency.** Check `version.json` existence before any network call. If present and version is supported, return immediately.
9. **Progress.** Call `ProgressFunc(downloaded, total)` after each chunk (target ≥64 KB intervals) and once on completion, matching Python's `if downloaded - last_update >= 65536` logic.
10. **No `.tar.xz`/.`tar.bz2`.** Current releases are `.zip` only. The `github.com/ulikunitz/xz` dep is not needed; do not add it unless asset format changes.
