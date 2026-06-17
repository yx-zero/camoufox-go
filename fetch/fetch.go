// Package fetch implements Camoufox binary download and management.
//
// It resolves the latest supported Camoufox release from GitHub, downloads the
// correct asset for the current (or overridden) OS/architecture, extracts it to
// the user cache directory, and returns the path to the executable.
//
// Ported from daijro/camoufox pythonlib/camoufox/pkgman.py and
// pythonlib/camoufox/multiversion.py (MIT License).
// Original authors: daijro and contributors.
// See THIRD_PARTY_LICENSES.md for full license text.
package fetch

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ─── Public constants ──────────────────────────────────────────────────────────

const (
	CacheDirName    = "camoufox"
	BrowsersDirName = "browsers"
	VersionFileName = "version.json"
	ConfigFileName  = "config.json"
	CompatFlagName  = ".0.5_FLAG"
	RepoName        = "official" // name for daijro/camoufox or camoufox/camoufox

	// Executable names per OS token (relative to the version install dir).
	ExecWin = "camoufox.exe"
	ExecLin = "camoufox-bin"
	ExecMac = "Camoufox.app/Contents/MacOS/camoufox"

	// CONSTRAINTS — must satisfy: MIN_VERSION <= version < MAX_VERSION.
	ConstraintsMinVersion = "alpha.1"
	ConstraintsMaxVersion = "1"
)

// Primary repos tried in order; on failure the next is used.
var githubRepos = []string{"daijro/camoufox", "camoufox/camoufox"}

// asset filename template from repos.yml:
// "{name}-{version}-{build}-{os}.{arch}.zip"
const assetPatternTemplate = `(?P<name>\w+)-(?P<version>[^-]+)-(?P<build>[^-]+)-{os}\.{arch}\.zip`

// osArchMatrix mirrors OS_ARCH_MATRIX from pkgman.py.
var osArchMatrix = map[string][]string{
	"win": {"x86_64", "i686"},
	"mac": {"x86_64", "arm64"},
	"lin": {"x86_64", "arm64", "i686"},
}

// ─── Sentinel errors ───────────────────────────────────────────────────────────

var (
	ErrNotInstalled      = errors.New("camoufox is not installed; run Fetch first")
	ErrUnsupportedOS     = errors.New("unsupported OS")
	ErrUnsupportedArch   = errors.New("unsupported architecture")
	ErrNoMatchingRelease = errors.New("no matching release found for this OS/arch/version")
	ErrOutdated          = errors.New("installed camoufox version is outdated")
)

// ─── ProgressFunc ─────────────────────────────────────────────────────────────

// ProgressFunc is called periodically during download.
// downloaded and total are in bytes; total may be 0 if Content-Length is absent.
type ProgressFunc func(downloaded, total int64)

// ─── Options ──────────────────────────────────────────────────────────────────

// Options controls the Fetch operation.
type Options struct {
	// ForceOS overrides runtime.GOOS detection (Camoufox token: "win"/"lin"/"mac").
	ForceOS string
	// ForceArch overrides runtime.GOARCH detection (Camoufox token: "x86_64"/"arm64"/"i686").
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

// ─── Version ──────────────────────────────────────────────────────────────────

// Version holds parsed build metadata.
type Version struct {
	Version    string // Firefox version string, e.g. "134.0.2"
	Build      string // Build/channel string, e.g. "beta.20"
	Prerelease bool
}

// FullString returns "{Version}-{Build}", e.g. "134.0.2-beta.20".
func (v Version) FullString() string {
	return v.Version + "-" + v.Build
}

// IsSupported reports whether the build falls within [MIN_VERSION, MAX_VERSION).
func (v Version) IsSupported() bool {
	vMin := parseVersion(ConstraintsMinVersion)
	vMax := parseVersion(ConstraintsMaxVersion)
	vParsed := parseVersion(v.Build)
	return vParsed >= vMin && vParsed < vMax
}

// isAlpha reports whether this build's first component is "alpha" (case-insensitive).
// Alpha builds are always treated as prerelease regardless of the GitHub flag.
func (v Version) isAlpha() bool {
	parts := strings.SplitN(v.Build, ".", 2)
	return strings.EqualFold(parts[0], "alpha")
}

// IsPrerelease reports whether the build should be treated as prerelease.
func (v Version) IsPrerelease() bool {
	return v.Prerelease || v.isAlpha()
}

// ─── AvailableVersion ─────────────────────────────────────────────────────────

// AvailableVersion describes a downloadable release asset.
type AvailableVersion struct {
	Version        Version
	URL            string
	Prerelease     bool
	AssetID        int64
	AssetSize      int64
	AssetUpdatedAt string // RFC3339
}

// ─── Version comparison ───────────────────────────────────────────────────────

// parseVersion converts a build string into a comparable int64 representation.
//
// Algorithm from pkgman.py Version.__post_init__:
//   - Split build on "."
//   - Each component: if all digits → int; else → int(rune(s[0])) - 1024
//   - Pad to 5 parts with zeros
//   - Pack into a single int64 (each part clamped to [-1023,9999])
//
// We encode as a single comparable int64 using base 100000.
func parseVersion(build string) int64 {
	parts := strings.Split(build, ".")
	vals := make([]int64, 5)
	for i := 0; i < 5; i++ {
		if i < len(parts) {
			p := parts[i]
			if isAllDigits(p) {
				var n int64
				for _, c := range p {
					n = n*10 + int64(c-'0')
				}
				vals[i] = n
			} else if len(p) > 0 {
				// Non-numeric: ord(first_char) - 1024, which is typically negative for letters
				vals[i] = int64(rune(p[0])) - 1024
			}
		}
	}
	// Pack 5 values into a single int64.
	// Offset each by 10000 so negative values work correctly.
	const offset = int64(10000)
	const base = int64(20001) // range per slot: [-10000, 10000]
	result := int64(0)
	for i := 0; i < 5; i++ {
		result = result*base + (vals[i] + offset)
	}
	return result
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ─── OS / Arch mapping ────────────────────────────────────────────────────────

// mapOS converts runtime.GOOS → "win"/"lin"/"mac".
func mapOS(goos string) (string, error) {
	switch goos {
	case "windows":
		return "win", nil
	case "linux":
		return "lin", nil
	case "darwin":
		return "mac", nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedOS, goos)
	}
}

// mapArch converts runtime.GOARCH → "x86_64"/"arm64"/"i686".
func mapArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "386":
		return "i686", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedArch, goarch)
	}
}

// resolveOSArch returns the OS/arch tokens to use, applying ForceOS/ForceArch overrides.
func resolveOSArch(opts Options) (osToken, archToken string, err error) {
	if opts.ForceOS != "" {
		osToken = opts.ForceOS
	} else {
		osToken, err = mapOS(runtime.GOOS)
		if err != nil {
			return
		}
	}
	if opts.ForceArch != "" {
		archToken = opts.ForceArch
	} else {
		archToken, err = mapArch(runtime.GOARCH)
		if err != nil {
			return
		}
	}
	// Validate combination.
	valid, ok := osArchMatrix[osToken]
	if !ok {
		err = fmt.Errorf("%w: %s", ErrUnsupportedOS, osToken)
		return
	}
	for _, a := range valid {
		if a == archToken {
			return
		}
	}
	err = fmt.Errorf("%w: %s is not supported on %s", ErrUnsupportedArch, archToken, osToken)
	return
}

// ─── Asset pattern ────────────────────────────────────────────────────────────

// buildAssetPattern returns a *regexp.Regexp matching asset filenames for the given os/arch.
// Template: "{name}-{version}-{build}-{os}.{arch}.zip"
func buildAssetPattern(osToken, archToken string) *regexp.Regexp {
	pattern := assetPatternTemplate
	pattern = strings.ReplaceAll(pattern, "{os}", regexp.QuoteMeta(osToken))
	pattern = strings.ReplaceAll(pattern, "{arch}", regexp.QuoteMeta(archToken))
	return regexp.MustCompile(`^` + pattern + `$`)
}

// ─── Cache directory helpers ─────────────────────────────────────────────────

// cacheDir returns the resolved cache directory.
func cacheDir(opts Options) (string, error) {
	if opts.CacheDir != "" {
		return opts.CacheDir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not determine user cache dir: %w", err)
	}
	return filepath.Join(base, CacheDirName), nil
}

// browsersDir returns CACHE/browsers.
func browsersDir(cache string) string {
	return filepath.Join(cache, BrowsersDirName)
}

// installPath returns CACHE/browsers/official/{version}-{build}.
func installPath(cache, version, build string) string {
	return filepath.Join(browsersDir(cache), RepoName, version+"-"+build)
}

// activeConfigPath returns the path to config.json.
func activeConfigPath(cache string) string {
	return filepath.Join(cache, ConfigFileName)
}

// resolveExecPath returns the absolute path to the executable given the install dir and os token.
func resolveExecPath(installDir, osToken string) string {
	switch osToken {
	case "win":
		return filepath.Join(installDir, ExecWin)
	case "lin":
		return filepath.Join(installDir, ExecLin)
	case "mac":
		return filepath.Join(installDir, ExecMac)
	default:
		return filepath.Join(installDir, ExecLin)
	}
}

// ─── version.json I/O ────────────────────────────────────────────────────────

type versionJSON struct {
	Version        string `json:"version"`
	Build          string `json:"build"`
	Release        string `json:"release,omitempty"` // legacy alias
	Tag            string `json:"tag,omitempty"`     // legacy alias
	Prerelease     bool   `json:"prerelease"`
	AssetID        *int64 `json:"asset_id,omitempty"`
	AssetSize      *int64 `json:"asset_size,omitempty"`
	AssetUpdatedAt string `json:"asset_updated_at,omitempty"`
}

// writeVersionJSON writes version.json into installPath.
func writeVersionJSON(iPath string, av AvailableVersion) error {
	data := versionJSON{
		Version:    av.Version.Version,
		Build:      av.Version.Build,
		Prerelease: av.Prerelease,
	}
	if av.AssetID != 0 {
		id := av.AssetID
		data.AssetID = &id
	}
	if av.AssetSize != 0 {
		sz := av.AssetSize
		data.AssetSize = &sz
	}
	if av.AssetUpdatedAt != "" {
		data.AssetUpdatedAt = av.AssetUpdatedAt
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(iPath, VersionFileName), b, 0644)
}

// readVersionJSON reads and parses version.json from path.
func readVersionJSON(path string) (*Version, error) {
	b, err := os.ReadFile(filepath.Join(path, VersionFileName))
	if err != nil {
		return nil, err
	}
	var vj versionJSON
	if err := json.Unmarshal(b, &vj); err != nil {
		return nil, err
	}
	// Handle legacy key aliases.
	if vj.Build == "" && vj.Release != "" {
		vj.Build = vj.Release
	} else if vj.Build == "" && vj.Tag != "" {
		vj.Build = vj.Tag
	}
	return &Version{
		Version:    vj.Version,
		Build:      vj.Build,
		Prerelease: vj.Prerelease,
	}, nil
}

// ─── config.json I/O ─────────────────────────────────────────────────────────

type configJSON struct {
	ActiveVersion string `json:"active_version"`
	Channel       string `json:"channel"`
	Pinned        *string `json:"pinned"`
}

func writeConfigJSON(cache string, cfg configJSON) error {
	if err := os.MkdirAll(cache, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(activeConfigPath(cache), b, 0644)
}

func readConfigJSON(cache string) (configJSON, error) {
	var cfg configJSON
	b, err := os.ReadFile(activeConfigPath(cache))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	err = json.Unmarshal(b, &cfg)
	return cfg, err
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

func githubToken(opts Options) string {
	if opts.GitHubToken != "" {
		return opts.GitHubToken
	}
	return os.Getenv("GITHUB_TOKEN")
}

func newHTTPRequest(ctx context.Context, url, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "camoufox-go/fetch")
	return req, nil
}

// ─── GitHub API structures ───────────────────────────────────────────────────

type ghRelease struct {
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ID                 int64  `json:"id"`
	Size               int64  `json:"size"`
	UpdatedAt          string `json:"updated_at"`
}

// fetchReleases retrieves the release list from a single GitHub repo.
func fetchReleases(ctx context.Context, repo, token string) ([]ghRelease, error) {
	url := "https://api.github.com/repos/" + repo + "/releases"
	req, err := newHTTPRequest(ctx, url, token)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s for %s", resp.Status, url)
	}
	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// ─── Version filtering ────────────────────────────────────────────────────────

// Stable channel constraints from repos.yml:
//   stable.min = "beta.19", stable.max = "1"
// Prerelease channel is unconstrained (absent in repos.yml).

const stableMin = "beta.19"
const stableMax = "1"

// isVersionSupported checks the stable/prerelease channel constraints from repos.yml.
func isVersionSupported(v Version, isPrerelease bool) bool {
	if isPrerelease {
		// Prerelease channel is unconstrained.
		return true
	}
	// Stable channel: beta.19 <= v <= "1"
	vMin := parseVersion(stableMin)
	vMax := parseVersion(stableMax)
	vParsed := parseVersion(v.Build)
	return vParsed >= vMin && vParsed <= vMax
}

// ─── Core scanning ────────────────────────────────────────────────────────────

// scanReleases returns the first AvailableVersion matching the pattern and constraints.
// If includePrerelease is false, alpha/prerelease builds are skipped.
func scanReleases(releases []ghRelease, pat *regexp.Regexp, includePrerelease bool) (*AvailableVersion, error) {
	for _, rel := range releases {
		releaseIsPrerelease := rel.Prerelease
		for _, asset := range rel.Assets {
			m := pat.FindStringSubmatch(asset.Name)
			if m == nil {
				continue
			}
			// Extract named groups.
			buildStr := ""
			versionStr := ""
			for i, name := range pat.SubexpNames() {
				if i == 0 {
					continue
				}
				switch name {
				case "build":
					buildStr = m[i]
				case "version":
					versionStr = m[i]
				}
			}
			v := Version{
				Version: versionStr,
				Build:   buildStr,
			}
			assetIsPrerelease := releaseIsPrerelease || v.isAlpha()
			v.Prerelease = assetIsPrerelease
			if assetIsPrerelease && !includePrerelease {
				continue
			}
			if !isVersionSupported(v, assetIsPrerelease) {
				continue
			}
			// CONSTRAINTS check.
			if !v.IsSupported() {
				continue
			}
			return &AvailableVersion{
				Version:        v,
				URL:            asset.BrowserDownloadURL,
				Prerelease:     assetIsPrerelease,
				AssetID:        asset.ID,
				AssetSize:      asset.Size,
				AssetUpdatedAt: asset.UpdatedAt,
			}, nil
		}
	}
	return nil, nil
}

// ─── Download & extract ───────────────────────────────────────────────────────

// downloadAndExtract downloads url into a temp file, then extracts the zip into destDir.
func downloadAndExtract(ctx context.Context, url, destDir, token string, progress ProgressFunc) error {
	// Create a temp file in the parent of destDir.
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, "camoufox-download-*.zip")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	// Download.
	req, err := newHTTPRequest(ctx, url, token)
	if err != nil {
		tmp.Close()
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tmp.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("HTTP %s downloading %s", resp.Status, url)
	}

	total := resp.ContentLength // may be -1 if unknown
	if total < 0 {
		total = 0
	}

	buf := make([]byte, 64*1024)
	var downloaded int64
	var lastUpdate int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmp.Write(buf[:n]); writeErr != nil {
				tmp.Close()
				return fmt.Errorf("writing temp file: %w", writeErr)
			}
			downloaded += int64(n)
			if progress != nil && (downloaded-lastUpdate >= 65536 || (total > 0 && downloaded == total)) {
				progress(downloaded, total)
				lastUpdate = downloaded
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			tmp.Close()
			return fmt.Errorf("reading response: %w", readErr)
		}
	}
	// Final progress callback.
	if progress != nil && downloaded > lastUpdate {
		progress(downloaded, total)
	}
	tmp.Close()

	// Extract zip.
	if err := extractZip(tmpPath, destDir); err != nil {
		return fmt.Errorf("extracting zip: %w", err)
	}
	return nil
}

// extractZip extracts all entries from zipPath into destDir.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// Sanitize path to prevent ZipSlip.
		target := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("zip entry %q would escape destination directory", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

// ─── chmod ────────────────────────────────────────────────────────────────────

// chmodExec recursively sets 0755 on all entries under path (no-op on Windows).
func chmodExec(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chmod(p, 0755)
	})
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Fetch resolves the latest supported Camoufox version for the current (or overridden)
// OS/arch, downloads it from GitHub releases if not already cached, extracts it to the
// cache directory, and returns the absolute path to the browser executable.
// It is idempotent: if a valid install already exists it returns immediately.
func Fetch(ctx context.Context, opts Options) (string, error) {
	osToken, archToken, err := resolveOSArch(opts)
	if err != nil {
		return "", err
	}
	cache, err := cacheDir(opts)
	if err != nil {
		return "", err
	}

	token := githubToken(opts)
	pat := buildAssetPattern(osToken, archToken)

	// Compatibility sentinel: if INSTALL_DIR exists, non-empty, and flag absent → wipe.
	compatFlag := filepath.Join(cache, CompatFlagName)
	if fi, err2 := os.Stat(cache); err2 == nil && fi.IsDir() {
		entries, _ := os.ReadDir(cache)
		if len(entries) > 0 {
			if _, flagErr := os.Stat(compatFlag); errors.Is(flagErr, os.ErrNotExist) {
				_ = os.RemoveAll(cache)
			}
		}
	}

	// Try all repos in order.
	var lastErr error
	for _, repo := range githubRepos {
		releases, err2 := fetchReleases(ctx, repo, token)
		if err2 != nil {
			lastErr = err2
			continue
		}

		av, err2 := scanReleases(releases, pat, opts.IncludePrerelease)
		if err2 != nil {
			lastErr = err2
			continue
		}
		if av == nil {
			lastErr = ErrNoMatchingRelease
			continue
		}

		// Idempotency: check if this exact version is already installed.
		iPath := installPath(cache, av.Version.Version, av.Version.Build)
		if !opts.Replace {
			if _, statErr := os.Stat(filepath.Join(iPath, VersionFileName)); statErr == nil {
				// Already installed — verify it's still supported.
				existing, readErr := readVersionJSON(iPath)
				if readErr == nil && existing.IsSupported() {
					return resolveExecPath(iPath, osToken), nil
				}
			}
		} else if _, statErr := os.Stat(iPath); statErr == nil {
			// Replace mode: remove old install.
			_ = os.RemoveAll(iPath)
		}

		// Create install dir and download atomically.
		tmpInstall := iPath + ".tmp"
		_ = os.RemoveAll(tmpInstall)
		if err2 := os.MkdirAll(tmpInstall, 0755); err2 != nil {
			lastErr = err2
			continue
		}

		if err2 := downloadAndExtract(ctx, av.URL, tmpInstall, token, opts.Progress); err2 != nil {
			_ = os.RemoveAll(tmpInstall)
			lastErr = err2
			continue
		}

		if err2 := writeVersionJSON(tmpInstall, *av); err2 != nil {
			_ = os.RemoveAll(tmpInstall)
			lastErr = err2
			continue
		}

		// Atomic rename.
		if err2 := os.MkdirAll(filepath.Dir(iPath), 0755); err2 != nil {
			_ = os.RemoveAll(tmpInstall)
			lastErr = err2
			continue
		}
		if err2 := os.Rename(tmpInstall, iPath); err2 != nil {
			_ = os.RemoveAll(tmpInstall)
			lastErr = err2
			continue
		}

		// chmod on non-Windows.
		_ = chmodExec(iPath)

		// Touch compatibility flag.
		if err2 := os.MkdirAll(cache, 0755); err2 == nil {
			f, _ := os.OpenFile(compatFlag, os.O_CREATE|os.O_WRONLY, 0644)
			if f != nil {
				f.Close()
			}
		}

		// Update config.json.
		relPath := "browsers/" + RepoName + "/" + av.Version.FullString()
		cfg := configJSON{
			ActiveVersion: relPath,
			Channel:       RepoName + "/stable",
		}
		_ = writeConfigJSON(cache, cfg)

		execPath := resolveExecPath(iPath, osToken)
		return execPath, nil
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", ErrNoMatchingRelease
}

// ExecPath returns the absolute path to the Camoufox executable for an already-installed
// version without triggering a download. Returns ErrNotInstalled if absent.
func ExecPath(opts Options) (string, error) {
	osToken, _, err := resolveOSArch(opts)
	if err != nil {
		return "", err
	}
	cache, err := cacheDir(opts)
	if err != nil {
		return "", err
	}

	cfg, err := readConfigJSON(cache)
	if err != nil || cfg.ActiveVersion == "" {
		return "", ErrNotInstalled
	}
	iPath := filepath.Join(cache, filepath.FromSlash(cfg.ActiveVersion))
	if _, statErr := os.Stat(filepath.Join(iPath, VersionFileName)); statErr != nil {
		return "", ErrNotInstalled
	}
	execPath := resolveExecPath(iPath, osToken)
	if _, statErr := os.Stat(execPath); statErr != nil {
		return "", ErrNotInstalled
	}
	return execPath, nil
}

// InstalledVersion returns the Version for the currently active install, or error if absent.
func InstalledVersion(opts Options) (*Version, error) {
	cache, err := cacheDir(opts)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfigJSON(cache)
	if err != nil || cfg.ActiveVersion == "" {
		return nil, ErrNotInstalled
	}
	iPath := filepath.Join(cache, filepath.FromSlash(cfg.ActiveVersion))
	v, err := readVersionJSON(iPath)
	if err != nil {
		return nil, ErrNotInstalled
	}
	return v, nil
}

// ListAvailable fetches all available versions from GitHub that match the current
// (or overridden) OS/arch and fall within the CONSTRAINTS range.
func ListAvailable(ctx context.Context, opts Options) ([]AvailableVersion, error) {
	osToken, archToken, err := resolveOSArch(opts)
	if err != nil {
		return nil, err
	}
	token := githubToken(opts)
	pat := buildAssetPattern(osToken, archToken)

	var releases []ghRelease
	var lastErr error
	for _, repo := range githubRepos {
		r, err2 := fetchReleases(ctx, repo, token)
		if err2 != nil {
			lastErr = err2
			continue
		}
		releases = r
		break
	}
	if releases == nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, ErrNoMatchingRelease
	}

	var versions []AvailableVersion
	seen := map[string]bool{}

	for _, rel := range releases {
		releaseIsPrerelease := rel.Prerelease
		if releaseIsPrerelease && !opts.IncludePrerelease {
			continue
		}
		for _, asset := range rel.Assets {
			m := pat.FindStringSubmatch(asset.Name)
			if m == nil {
				continue
			}
			buildStr := ""
			versionStr := ""
			for i, name := range pat.SubexpNames() {
				if i == 0 {
					continue
				}
				switch name {
				case "build":
					buildStr = m[i]
				case "version":
					versionStr = m[i]
				}
			}
			v := Version{
				Version: versionStr,
				Build:   buildStr,
			}
			assetIsPrerelease := releaseIsPrerelease || v.isAlpha()
			v.Prerelease = assetIsPrerelease
			if assetIsPrerelease && !opts.IncludePrerelease {
				continue
			}
			if !isVersionSupported(v, assetIsPrerelease) {
				continue
			}
			if !v.IsSupported() {
				continue
			}
			if seen[buildStr] {
				continue
			}
			seen[buildStr] = true
			versions = append(versions, AvailableVersion{
				Version:        v,
				URL:            asset.BrowserDownloadURL,
				Prerelease:     assetIsPrerelease,
				AssetID:        asset.ID,
				AssetSize:      asset.Size,
				AssetUpdatedAt: asset.UpdatedAt,
			})
		}
	}
	return versions, nil
}

// Remove deletes the installation at the given version path.
func Remove(installPath string) error {
	return os.RemoveAll(installPath)
}
