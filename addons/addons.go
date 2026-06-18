// Package addons implements Camoufox addon (browser extension) management.
//
// Default addons (currently just uBlock Origin) are downloaded from
// addons.mozilla.org as .xpi files (which are ZIP archives) and extracted into
// a shared, version-independent cache folder: <cacheDir>/addons/<NAME>. A valid
// addon path is a directory containing a manifest.json. User-supplied addon
// paths are validated but never downloaded.
//
// Ported from daijro/camoufox pythonlib/camoufox/addons.py (MIT License).
// Original authors: daijro and contributors.
// See THIRD_PARTY_LICENSES.md for full license text.
package addons

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// AddonsDirName is the name of the shared addons subdirectory under the cache
// directory. Addons are stored here, not per-browser-version, mirroring
// ADDONS_DIR in addons.py.
const AddonsDirName = "addons"

// manifestFile is the file that must be present for an extracted directory to
// be considered a valid addon (confirm_paths in addons.py).
const manifestFile = "manifest.json"

// ─── Default addon enum ─────────────────────────────────────────────────────────

// Default is an extensible enumeration of built-in addons that Camoufox
// downloads automatically. It mirrors the DefaultAddons enum in addons.py.
//
// To add a new default addon, append a constant in the const block below and
// extend the Name and URL methods plus DefaultList.
type Default int

const (
	// UBO is uBlock Origin.
	UBO Default = iota
)

// defaultMeta holds the static metadata for each Default addon.
type defaultMeta struct {
	name string
	url  string
}

// defaults is the authoritative table of built-in addons, indexed by Default.
var defaults = [...]defaultMeta{
	UBO: {
		name: "UBO",
		url:  "https://addons.mozilla.org/firefox/downloads/latest/ublock-origin/latest.xpi",
	},
}

// Name returns the addon's enum name (e.g. "UBO"). This name is used as the
// subdirectory under <cacheDir>/addons/.
func (d Default) Name() string {
	if d < 0 || int(d) >= len(defaults) {
		return fmt.Sprintf("Default(%d)", int(d))
	}
	return defaults[d].name
}

// URL returns the .xpi download URL for the addon.
func (d Default) URL() string {
	if d < 0 || int(d) >= len(defaults) {
		return ""
	}
	return defaults[d].url
}

// DefaultList returns all built-in default addons, mirroring iteration over the
// DefaultAddons enum in addons.py.
func DefaultList() []Default {
	out := make([]Default, len(defaults))
	for i := range defaults {
		out[i] = Default(i)
	}
	return out
}

// ─── Path validation ────────────────────────────────────────────────────────────

// InvalidAddonPathError indicates a user-supplied addon path that is not a
// directory or is missing manifest.json. It corresponds to InvalidAddonPath in
// the Python library.
type InvalidAddonPathError struct {
	Path   string
	Reason string
}

func (e *InvalidAddonPathError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("invalid addon path %q: %s", e.Path, e.Reason)
	}
	return fmt.Sprintf("invalid addon path %q", e.Path)
}

// ConfirmPaths confirms that each path is a valid extracted addon: it must be a
// directory and must contain a manifest.json. It returns an
// *InvalidAddonPathError for the first invalid path. Ports confirm_paths.
func ConfirmPaths(paths []string) error {
	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil || !fi.IsDir() {
			return &InvalidAddonPathError{Path: path}
		}
		if _, err := os.Stat(filepath.Join(path, manifestFile)); err != nil {
			return &InvalidAddonPathError{
				Path:   path,
				Reason: "manifest.json is missing. Addon path must be a path to an extracted addon.",
			}
		}
	}
	return nil
}

// ─── Cache path helpers ─────────────────────────────────────────────────────────

// addonsDir returns <cacheDir>/addons.
func addonsDir(cacheDir string) string {
	return filepath.Join(cacheDir, AddonsDirName)
}

// addonPath returns <cacheDir>/addons/<name>, the install location for a named
// default addon. Ports get_addon_path.
func addonPath(cacheDir, name string) string {
	return filepath.Join(addonsDir(cacheDir), name)
}

// ─── Download & extract ─────────────────────────────────────────────────────────

// downloadAndExtract downloads an .xpi from url and extracts it into extractPath.
// The .xpi is a ZIP archive. The default http.Client follows the AMO redirect to
// the CDN. Ports download_and_extract + webdl/unzip (without progress bars).
func downloadAndExtract(url, extractPath string) error {
	resp, err := http.Get(url) //nolint:gosec // url is a fixed AMO endpoint
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s downloading %s", resp.Status, url)
	}

	// Buffer the body so archive/zip can read it via a ReaderAt. xpi files are
	// small (a few MB), so holding them in memory is fine and keeps this pure.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if err := extractZipBytes(data, extractPath); err != nil {
		return fmt.Errorf("extracting xpi: %w", err)
	}
	return nil
}

// extractZipBytes extracts all entries from an in-memory ZIP into destDir,
// guarding against zip-slip (entries that escape destDir).
func extractZipBytes(data []byte, destDir string) error {
	r, err := zip.NewReader(byteReaderAt(data), int64(len(data)))
	if err != nil {
		return err
	}

	cleanDest := filepath.Clean(destDir)
	for _, f := range r.File {
		target := filepath.Join(cleanDest, filepath.FromSlash(f.Name))
		// zip-slip guard: target must be within destDir.
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q would escape destination directory", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// writeZipFile copies a single zip entry to target on disk.
func writeZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil { //nolint:gosec // xpi entries are small
		return err
	}
	return nil
}

// byteReaderAt adapts a byte slice to io.ReaderAt for archive/zip.
type byteReaderAt []byte

func (b byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// ─── Resolve ────────────────────────────────────────────────────────────────────

// Resolve builds the final list of addon directories to load.
//
//  1. Each user-supplied path is validated with ConfirmPaths (must be a
//     directory containing manifest.json); an invalid path returns an error.
//  2. For each Default addon not in exclude, ensure it is downloaded and
//     extracted to <cacheDir>/addons/<Name>. If the directory already exists it
//     is reused (no download). On a download/extract failure the addon is
//     logged to stderr and skipped — the launch is not hard-failed, matching the
//     best-effort behavior of maybe_download_addons in addons.py.
//  3. The returned slice lists the successfully-present default addon
//     directories first, followed by the user addon paths.
//
// Ordering choice: defaults first, then user addons. addons.py appends defaults
// onto the list before user paths are added by the caller, so defaults precede
// user paths; we reproduce that ordering explicitly here.
func Resolve(cacheDir string, userAddons []string, exclude []Default) ([]string, error) {
	// Step 1: validate user addon paths up front.
	if err := ConfirmPaths(userAddons); err != nil {
		return nil, err
	}

	// Build an exclusion set for O(1) lookups.
	excluded := make(map[Default]struct{}, len(exclude))
	for _, d := range exclude {
		excluded[d] = struct{}{}
	}

	result := make([]string, 0, len(defaults)+len(userAddons))

	// Step 2: ensure each non-excluded default addon is present.
	for _, d := range DefaultList() {
		if _, skip := excluded[d]; skip {
			continue
		}
		path := addonPath(cacheDir, d.Name())

		// Reuse if already extracted.
		if _, err := os.Stat(path); err == nil {
			result = append(result, path)
			continue
		}

		// Download + extract; best-effort. On failure, log and skip.
		if err := os.MkdirAll(path, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to download and extract %s: %v\n", d.Name(), err)
			continue
		}
		if err := downloadAndExtract(d.URL(), path); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to download and extract %s: %v\n", d.Name(), err)
			// Remove the partially-created directory so a later run retries cleanly.
			_ = os.RemoveAll(path)
			continue
		}
		result = append(result, path)
	}

	// Step 3: append user addons after defaults.
	result = append(result, userAddons...)
	return result, nil
}
