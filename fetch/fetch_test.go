// Tests for the fetch package.
// All tests are table-driven and require NO network access.
package fetch

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// ─── mapOS tests ──────────────────────────────────────────────────────────────

func TestMapOS(t *testing.T) {
	tests := []struct {
		goos    string
		want    string
		wantErr bool
	}{
		{"windows", "win", false},
		{"linux", "lin", false},
		{"darwin", "mac", false},
		{"freebsd", "", true},
		{"plan9", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got, err := mapOS(tt.goos)
			if (err != nil) != tt.wantErr {
				t.Fatalf("mapOS(%q) error = %v, wantErr %v", tt.goos, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("mapOS(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}

// ─── mapArch tests ────────────────────────────────────────────────────────────

func TestMapArch(t *testing.T) {
	tests := []struct {
		goarch  string
		want    string
		wantErr bool
	}{
		{"amd64", "x86_64", false},
		{"386", "i686", false},
		{"arm64", "arm64", false},
		{"mips", "", true},
		{"riscv64", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.goarch, func(t *testing.T) {
			got, err := mapArch(tt.goarch)
			if (err != nil) != tt.wantErr {
				t.Fatalf("mapArch(%q) error = %v, wantErr %v", tt.goarch, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("mapArch(%q) = %q, want %q", tt.goarch, got, tt.want)
			}
		})
	}
}

// ─── buildAssetPattern tests ──────────────────────────────────────────────────

func TestBuildAssetPattern(t *testing.T) {
	tests := []struct {
		osToken   string
		archToken string
		// filenames that should match
		matches []string
		// filenames that should NOT match
		noMatch []string
	}{
		{
			osToken:   "win",
			archToken: "x86_64",
			matches: []string{
				"camoufox-134.0.2-beta.20-win.x86_64.zip",
				"camoufox-130.0-beta.1-win.x86_64.zip",
				"camoufox-1.0.0-alpha.1-win.x86_64.zip",
			},
			noMatch: []string{
				"camoufox-134.0.2-beta.20-lin.x86_64.zip",
				"camoufox-134.0.2-beta.20-win.arm64.zip",
				"camoufox-134.0.2-beta.20-mac.x86_64.zip",
				"camoufox-134.0.2-beta.20-win.i686.zip",
				"camoufox-134.0.2-beta.20-win.x86_64.tar.gz",
				// Suffix after the zip extension.
				"camoufox-134.0.2-beta.20-win.x86_64.zip.sig",
			},
		},
		{
			osToken:   "lin",
			archToken: "arm64",
			matches: []string{
				"camoufox-134.0.2-beta.20-lin.arm64.zip",
				"camoufox-134.0.2-alpha.26-lin.arm64.zip",
			},
			noMatch: []string{
				"camoufox-134.0.2-beta.20-win.arm64.zip",
				"camoufox-134.0.2-beta.20-lin.x86_64.zip",
				"camoufox-134.0.2-beta.20-mac.arm64.zip",
			},
		},
		{
			osToken:   "mac",
			archToken: "arm64",
			matches: []string{
				"camoufox-134.0.2-beta.20-mac.arm64.zip",
			},
			noMatch: []string{
				"camoufox-134.0.2-beta.20-mac.x86_64.zip",
				"camoufox-134.0.2-beta.20-lin.arm64.zip",
			},
		},
		{
			osToken:   "win",
			archToken: "i686",
			matches: []string{
				"camoufox-134.0.2-beta.20-win.i686.zip",
			},
			noMatch: []string{
				"camoufox-134.0.2-beta.20-win.x86_64.zip",
				"camoufox-134.0.2-beta.20-lin.i686.zip",
			},
		},
		{
			osToken:   "lin",
			archToken: "i686",
			matches: []string{
				"camoufox-134.0.2-beta.20-lin.i686.zip",
			},
			noMatch: []string{
				"camoufox-134.0.2-beta.20-win.i686.zip",
				"camoufox-134.0.2-beta.20-lin.x86_64.zip",
			},
		},
	}

	for _, tt := range tests {
		pat := buildAssetPattern(tt.osToken, tt.archToken)
		t.Run(tt.osToken+"/"+tt.archToken, func(t *testing.T) {
			for _, name := range tt.matches {
				if !pat.MatchString(name) {
					t.Errorf("pattern(%s,%s) should match %q but did not", tt.osToken, tt.archToken, name)
				}
			}
			for _, name := range tt.noMatch {
				if pat.MatchString(name) {
					t.Errorf("pattern(%s,%s) should NOT match %q but did", tt.osToken, tt.archToken, name)
				}
			}
		})
	}
}

// ─── Named capture groups ─────────────────────────────────────────────────────

func TestBuildAssetPatternCaptures(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	name := "camoufox-134.0.2-beta.20-win.x86_64.zip"
	m := pat.FindStringSubmatch(name)
	if m == nil {
		t.Fatalf("expected match for %q", name)
	}
	groups := make(map[string]string)
	for i, gname := range pat.SubexpNames() {
		if i != 0 && gname != "" {
			groups[gname] = m[i]
		}
	}
	if groups["name"] != "camoufox" {
		t.Errorf("name = %q, want %q", groups["name"], "camoufox")
	}
	if groups["version"] != "134.0.2" {
		t.Errorf("version = %q, want %q", groups["version"], "134.0.2")
	}
	if groups["build"] != "beta.20" {
		t.Errorf("build = %q, want %q", groups["build"], "beta.20")
	}
}

// ─── parseVersion tests ───────────────────────────────────────────────────────

func TestParseVersionOrdering(t *testing.T) {
	// These must be in strictly ascending order according to parseVersion.
	ordered := []string{
		"alpha.1",
		"alpha.26",
		"beta.1",
		"beta.19",
		"beta.20",
		"beta.100",
	}
	for i := 0; i < len(ordered)-1; i++ {
		a := parseVersion(ordered[i])
		b := parseVersion(ordered[i+1])
		if a >= b {
			t.Errorf("parseVersion(%q)=%d >= parseVersion(%q)=%d; expected strictly less",
				ordered[i], a, ordered[i+1], b)
		}
	}
}

func TestParseVersionNumericParts(t *testing.T) {
	// "1" should be a large positive number (greater than any "beta.*").
	one := parseVersion("1")
	beta20 := parseVersion("beta.20")
	if one <= beta20 {
		t.Errorf("parseVersion(\"1\")=%d should be > parseVersion(\"beta.20\")=%d", one, beta20)
	}

	// "alpha.1" should be less than "beta.1".
	a1 := parseVersion("alpha.1")
	b1 := parseVersion("beta.1")
	if a1 >= b1 {
		t.Errorf("parseVersion(\"alpha.1\")=%d should be < parseVersion(\"beta.1\")=%d", a1, b1)
	}
}

// ─── Version.IsSupported tests ────────────────────────────────────────────────

func TestVersionIsSupported(t *testing.T) {
	tests := []struct {
		build string
		want  bool
	}{
		// alpha.1 is the MIN_VERSION (inclusive)
		{"alpha.1", true},
		{"alpha.26", true},
		{"beta.1", true},
		{"beta.19", true},
		{"beta.20", true},
		// "1" is MAX_VERSION (exclusive)
		{"1", false},
		// anything >= 1 is unsupported
		{"2", false},
	}
	for _, tt := range tests {
		v := Version{Build: tt.build}
		got := v.IsSupported()
		if got != tt.want {
			t.Errorf("Version{Build:%q}.IsSupported() = %v, want %v", tt.build, got, tt.want)
		}
	}
}

// ─── Version.isAlpha tests ────────────────────────────────────────────────────

func TestVersionIsAlpha(t *testing.T) {
	tests := []struct {
		build string
		want  bool
	}{
		{"alpha.1", true},
		{"Alpha.26", true},
		{"ALPHA.5", true},
		{"beta.20", false},
		{"stable.1", false},
		{"1", false},
		{"beta.alpha", false},
	}
	for _, tt := range tests {
		v := Version{Build: tt.build}
		got := v.isAlpha()
		if got != tt.want {
			t.Errorf("Version{Build:%q}.isAlpha() = %v, want %v", tt.build, got, tt.want)
		}
	}
}

// ─── resolveOSArch tests ──────────────────────────────────────────────────────

func TestResolveOSArchForce(t *testing.T) {
	tests := []struct {
		opts         Options
		wantOS       string
		wantArch     string
		wantErr      bool
	}{
		{
			opts:     Options{ForceOS: "win", ForceArch: "x86_64"},
			wantOS:   "win",
			wantArch: "x86_64",
		},
		{
			opts:     Options{ForceOS: "lin", ForceArch: "arm64"},
			wantOS:   "lin",
			wantArch: "arm64",
		},
		{
			opts:     Options{ForceOS: "mac", ForceArch: "arm64"},
			wantOS:   "mac",
			wantArch: "arm64",
		},
		// Invalid combination: mac/i686 doesn't exist.
		{
			opts:    Options{ForceOS: "mac", ForceArch: "i686"},
			wantErr: true,
		},
		// Invalid OS.
		{
			opts:    Options{ForceOS: "bsd", ForceArch: "x86_64"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		osT, archT, err := resolveOSArch(tt.opts)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolveOSArch(%v) error = %v, wantErr %v", tt.opts, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if osT != tt.wantOS {
				t.Errorf("resolveOSArch OS = %q, want %q", osT, tt.wantOS)
			}
			if archT != tt.wantArch {
				t.Errorf("resolveOSArch arch = %q, want %q", archT, tt.wantArch)
			}
		}
	}
}

// ─── resolveExecPath tests ────────────────────────────────────────────────────

func TestResolveExecPath(t *testing.T) {
	tests := []struct {
		osToken string
		want    string // just the suffix relative to installDir
	}{
		{"win", "camoufox.exe"},
		{"lin", "camoufox-bin"},
		{"mac", filepath.Join("Camoufox.app", "Contents", "MacOS", "camoufox")},
	}
	for _, tt := range tests {
		got := resolveExecPath("/install/dir", tt.osToken)
		wantFull := filepath.Join("/install/dir", tt.want)
		if got != wantFull {
			t.Errorf("resolveExecPath(_, %q) = %q, want %q", tt.osToken, got, wantFull)
		}
	}
}

// ─── Version.FullString tests ─────────────────────────────────────────────────

func TestVersionFullString(t *testing.T) {
	v := Version{Version: "134.0.2", Build: "beta.20"}
	got := v.FullString()
	want := "134.0.2-beta.20"
	if got != want {
		t.Errorf("FullString() = %q, want %q", got, want)
	}
}

// ─── writeVersionJSON / readVersionJSON round-trip ────────────────────────────

func TestVersionJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := int64(12345)
	sz := int64(99999)
	av := AvailableVersion{
		Version:        Version{Version: "134.0.2", Build: "beta.20"},
		URL:            "https://example.com/asset.zip",
		Prerelease:     false,
		AssetID:        id,
		AssetSize:      sz,
		AssetUpdatedAt: "2024-01-01T00:00:00Z",
	}
	if err := writeVersionJSON(dir, av); err != nil {
		t.Fatalf("writeVersionJSON: %v", err)
	}
	v, err := readVersionJSON(dir)
	if err != nil {
		t.Fatalf("readVersionJSON: %v", err)
	}
	if v.Version != "134.0.2" {
		t.Errorf("Version = %q, want %q", v.Version, "134.0.2")
	}
	if v.Build != "beta.20" {
		t.Errorf("Build = %q, want %q", v.Build, "beta.20")
	}
	if v.Prerelease != false {
		t.Errorf("Prerelease = %v, want false", v.Prerelease)
	}
}

func TestVersionJSONLegacyAliases(t *testing.T) {
	dir := t.TempDir()
	// Write a "legacy" version.json with "release" key instead of "build".
	legacy := map[string]interface{}{
		"version": "130.0",
		"release": "beta.10",
	}
	b, _ := json.Marshal(legacy)
	_ = os.WriteFile(filepath.Join(dir, VersionFileName), b, 0644)

	v, err := readVersionJSON(dir)
	if err != nil {
		t.Fatalf("readVersionJSON (legacy release): %v", err)
	}
	if v.Build != "beta.10" {
		t.Errorf("Build (from release) = %q, want %q", v.Build, "beta.10")
	}

	// Write with "tag" key.
	legacy2 := map[string]interface{}{
		"version": "130.0",
		"tag":     "beta.11",
	}
	b, _ = json.Marshal(legacy2)
	_ = os.WriteFile(filepath.Join(dir, VersionFileName), b, 0644)

	v2, err := readVersionJSON(dir)
	if err != nil {
		t.Fatalf("readVersionJSON (legacy tag): %v", err)
	}
	if v2.Build != "beta.11" {
		t.Errorf("Build (from tag) = %q, want %q", v2.Build, "beta.11")
	}
}

// ─── scanReleases (no-network) ────────────────────────────────────────────────

func makeFakeReleases(assets []string, prerelease bool) []ghRelease {
	var assetList []ghAsset
	for i, name := range assets {
		assetList = append(assetList, ghAsset{
			Name:               name,
			BrowserDownloadURL: "https://example.com/" + name,
			ID:                 int64(i + 1),
			Size:               1000,
			UpdatedAt:          "2024-01-01T00:00:00Z",
		})
	}
	return []ghRelease{{Prerelease: prerelease, Assets: assetList}}
}

func TestScanReleasesFindsMatch(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-beta.20-lin.x86_64.zip",
		"camoufox-134.0.2-beta.20-win.x86_64.zip", // should match
		"camoufox-134.0.2-beta.20-mac.x86_64.zip",
	}, false)

	av, err := scanReleases(releases, pat, false)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av == nil {
		t.Fatal("expected a match, got nil")
	}
	if av.Version.Build != "beta.20" {
		t.Errorf("Build = %q, want beta.20", av.Version.Build)
	}
	if av.Version.Version != "134.0.2" {
		t.Errorf("Version = %q, want 134.0.2", av.Version.Version)
	}
}

func TestScanReleasesNoMatch(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-beta.20-lin.x86_64.zip",
		"camoufox-134.0.2-beta.20-mac.arm64.zip",
	}, false)

	av, err := scanReleases(releases, pat, false)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av != nil {
		t.Errorf("expected nil match, got %+v", av)
	}
}

func TestScanReleasesSkipsAlphaWhenPrereleaseDisabled(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-alpha.26-win.x86_64.zip", // alpha → prerelease
	}, false) // GitHub prerelease flag is false, but alpha name forces prerelease

	av, err := scanReleases(releases, pat, false /* includePrerelease=false */)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av != nil {
		t.Errorf("expected nil (alpha skipped), got %+v", av)
	}
}

func TestScanReleasesIncludesAlphaWhenPrereleaseEnabled(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-alpha.26-win.x86_64.zip",
	}, false)

	av, err := scanReleases(releases, pat, true /* includePrerelease=true */)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av == nil {
		t.Fatal("expected a match for alpha build with includePrerelease=true, got nil")
	}
	if !av.Prerelease {
		t.Errorf("Prerelease should be true for alpha build")
	}
}

func TestScanReleasesSkipsOutsideConstraints(t *testing.T) {
	// "2" is >= MAX_VERSION "1" → unsupported
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-2-win.x86_64.zip",
	}, false)

	av, err := scanReleases(releases, pat, false)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av != nil {
		t.Errorf("expected nil (out-of-constraints), got %+v", av)
	}
}

func TestScanReleasesSkipsGHPrereleaseFlagWhenDisabled(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	// GitHub release itself is marked prerelease=true, asset is a stable-looking build
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-beta.20-win.x86_64.zip",
	}, true /* release.prerelease = true */)

	av, err := scanReleases(releases, pat, false /* includePrerelease=false */)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av != nil {
		t.Errorf("expected nil (GH prerelease=true skipped), got %+v", av)
	}
}

func TestScanReleasesIncludesGHPrerelease(t *testing.T) {
	pat := buildAssetPattern("win", "x86_64")
	releases := makeFakeReleases([]string{
		"camoufox-134.0.2-beta.20-win.x86_64.zip",
	}, true)

	av, err := scanReleases(releases, pat, true)
	if err != nil {
		t.Fatalf("scanReleases: %v", err)
	}
	if av == nil {
		t.Fatal("expected a match with includePrerelease=true, got nil")
	}
}

// ─── isVersionSupported stable-channel constraints ────────────────────────────

func TestIsVersionSupportedStable(t *testing.T) {
	tests := []struct {
		build string
		want  bool
	}{
		// stable: min=beta.19, max=1 (inclusive on both ends per Python code)
		{"beta.19", true},
		{"beta.20", true},
		{"beta.100", true},
		{"beta.18", false}, // below stable min
	}
	for _, tt := range tests {
		v := Version{Build: tt.build}
		got := isVersionSupported(v, false)
		if got != tt.want {
			t.Errorf("isVersionSupported(%q, stable) = %v, want %v", tt.build, got, tt.want)
		}
	}
}

func TestIsVersionSupportedPrerelease(t *testing.T) {
	// Prerelease channel is unconstrained.
	tests := []struct {
		build string
	}{
		{"alpha.1"},
		{"alpha.26"},
		{"beta.1"},
		{"beta.18"},
		{"beta.19"},
	}
	for _, tt := range tests {
		v := Version{Build: tt.build}
		got := isVersionSupported(v, true)
		if !got {
			t.Errorf("isVersionSupported(%q, prerelease) = false, want true (unconstrained)", tt.build)
		}
	}
}

// ─── Pattern is a compile-time-valid regexp ───────────────────────────────────

func TestBuildAssetPatternIsValidRegexp(t *testing.T) {
	combos := [][2]string{
		{"win", "x86_64"}, {"win", "i686"},
		{"lin", "x86_64"}, {"lin", "arm64"}, {"lin", "i686"},
		{"mac", "x86_64"}, {"mac", "arm64"},
	}
	for _, c := range combos {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("buildAssetPattern(%q,%q) panicked: %v", c[0], c[1], r)
				}
			}()
			pat := buildAssetPattern(c[0], c[1])
			if pat == nil {
				t.Errorf("buildAssetPattern(%q,%q) returned nil", c[0], c[1])
			}
		}()
	}
}

// ─── Const sanity checks ──────────────────────────────────────────────────────

func TestConstants(t *testing.T) {
	if CacheDirName != "camoufox" {
		t.Errorf("CacheDirName = %q, want camoufox", CacheDirName)
	}
	if BrowsersDirName != "browsers" {
		t.Errorf("BrowsersDirName = %q, want browsers", BrowsersDirName)
	}
	if VersionFileName != "version.json" {
		t.Errorf("VersionFileName = %q, want version.json", VersionFileName)
	}
	if ConstraintsMinVersion != "alpha.1" {
		t.Errorf("MIN_VERSION = %q, want alpha.1", ConstraintsMinVersion)
	}
	if ConstraintsMaxVersion != "1" {
		t.Errorf("MAX_VERSION = %q, want 1", ConstraintsMaxVersion)
	}
	if ExecWin != "camoufox.exe" {
		t.Errorf("ExecWin = %q, want camoufox.exe", ExecWin)
	}
	if ExecLin != "camoufox-bin" {
		t.Errorf("ExecLin = %q, want camoufox-bin", ExecLin)
	}
	if ExecMac != "Camoufox.app/Contents/MacOS/camoufox" {
		t.Errorf("ExecMac = %q, want Camoufox.app/Contents/MacOS/camoufox", ExecMac)
	}
}

// ─── extractZip (local file) ──────────────────────────────────────────────────

func TestExtractZip(t *testing.T) {
	// Build a small in-memory zip and write it to a temp file.
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")
	destDir := filepath.Join(dir, "out")

	if err := createTestZip(zipPath); err != nil {
		t.Fatalf("createTestZip: %v", err)
	}
	if err := extractZip(zipPath, destDir); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	// Verify extracted file exists.
	gotPath := filepath.Join(destDir, "hello.txt")
	b, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(b) != "hello world" {
		t.Errorf("extracted content = %q, want %q", string(b), "hello world")
	}
}

func createTestZip(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	fw, err := w.Create("hello.txt")
	if err != nil {
		return err
	}
	if _, err := fw.Write([]byte("hello world")); err != nil {
		return err
	}
	return w.Close()
}

// ─── buildAssetPattern — ensure no accidental dot wildcard ───────────────────

func TestBuildAssetPatternDotIsLiteral(t *testing.T) {
	// The "." between os and arch must be a literal dot, not a regex wildcard.
	pat := buildAssetPattern("win", "x86_64")

	// Replace the dot between os and arch with a different char — must NOT match.
	bad := "camoufox-134.0.2-beta.20-winXx86_64.zip"
	if pat.MatchString(bad) {
		t.Errorf("pattern should not match %q (dot must be literal)", bad)
	}
}

// ─── Sentinel errors are distinct ────────────────────────────────────────────

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{
		ErrNotInstalled,
		ErrUnsupportedOS,
		ErrUnsupportedArch,
		ErrNoMatchingRelease,
		ErrOutdated,
	}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && a == b {
				t.Errorf("sentinel errors[%d] and [%d] are the same value", i, j)
			}
		}
	}
}

// ─── Unused import guard (regexp must be used) ────────────────────────────────

var _ = (*regexp.Regexp)(nil)
