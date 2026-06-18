package geoip

import (
	"archive/zip"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/yx-zero/camoufox-go/locale"
)

// Geolocation stores geolocation information. Ports locales.Geolocation.
type Geolocation struct {
	Locale    locale.Locale
	Longitude float64
	Latitude  float64
	Timezone  string
	Accuracy  float64
}

// ConfigKeys converts the geolocation to a config map. Ports
// Geolocation.as_config. "geolocation:accuracy" is included only when
// Accuracy > 0.
func (g Geolocation) ConfigKeys() map[string]any {
	data := map[string]any{
		"geolocation:longitude": g.Longitude,
		"geolocation:latitude":  g.Latitude,
		"timezone":              g.Timezone,
	}
	for k, v := range g.Locale.ConfigKeys() {
		data[k] = v
	}
	if g.Accuracy > 0 {
		data["geolocation:accuracy"] = g.Accuracy
	}
	return data
}

// geoipRepo describes a GeoIP database repository (mirrors a repos.yml entry).
type geoipRepo struct {
	Name    string
	Extract bool
	// URLs maps an ip-version key ("ipv4", "ipv6", or "combined") to an
	// ordered list of candidate download URLs.
	URLs  map[string][]string
	Paths geoipPaths
}

// geoipPaths holds the dotted record paths for each resolved field.
type geoipPaths struct {
	ISOCode   string
	Longitude string
	Latitude  string
	Timezone  string
}

// defaultGeoIPName is the default database name from repos.yml.
const defaultGeoIPName = "MaxMind GeoLite2"

// geoipRepos hardcodes the two repos from repos.yml.
var geoipRepos = []geoipRepo{
	{
		Name: "MaxMind GeoLite2",
		URLs: map[string][]string{
			"ipv4": {
				"https://cdn.jsdelivr.net/npm/@ip-location-db/geolite2-city-mmdb/geolite2-city-ipv4.mmdb",
				"https://raw.githubusercontent.com/sapics/ip-location-db/refs/heads/main/geolite2-city-mmdb/geolite2-city-ipv4.mmdb",
			},
			"ipv6": {
				"https://cdn.jsdelivr.net/npm/@ip-location-db/geolite2-city-mmdb/geolite2-city-ipv6.mmdb",
				"https://raw.githubusercontent.com/sapics/ip-location-db/refs/heads/main/geolite2-city-mmdb/geolite2-city-ipv6.mmdb",
			},
		},
		Paths: geoipPaths{
			ISOCode:   "country_code",
			Longitude: "longitude",
			Latitude:  "latitude",
			Timezone:  "timezone",
		},
	},
	{
		Name:    "GeoIP AIO by daijro",
		Extract: true,
		URLs: map[string][]string{
			"combined": {
				"https://github.com/daijro/geoip-all-in-one/releases/latest/download/geoip-aio-all.mmdb.zip",
			},
		},
		Paths: geoipPaths{
			ISOCode:   "country.iso_code",
			Longitude: "location.longitude",
			Latitude:  "location.latitude",
			Timezone:  "location.time_zone",
		},
	},
}

// updateMaxAge is how old a cached mmdb may be before it is refreshed.
const updateMaxAge = 30 * 24 * time.Hour

// getRepoByName returns the repo config matching name (case-insensitive). When
// name is empty the default repo is returned. Ports _get_geoip_config_by_name.
func getRepoByName(name string) (geoipRepo, error) {
	target := name
	if target == "" {
		target = defaultGeoIPName
	}
	for _, r := range geoipRepos {
		if strings.EqualFold(r.Name, target) {
			return r, nil
		}
	}
	if name != "" {
		available := make([]string, 0, len(geoipRepos))
		for _, r := range geoipRepos {
			available = append(available, r.Name)
		}
		return geoipRepo{}, fmt.Errorf("GeoIP database %q not found. Available: %v", name, available)
	}
	if len(geoipRepos) > 0 {
		return geoipRepos[0], nil
	}
	return geoipRepo{}, fmt.Errorf("no GeoIP repos configured")
}

// isCombined reports whether the repo uses a single combined database.
func (r geoipRepo) isCombined() bool {
	_, ok := r.URLs["combined"]
	return ok
}

// mmdbPath returns the cache path for the given ip version. Ports
// get_mmdb_path: combined repos use "<name>-combined.mmdb", others use
// "<name>-<ipver>.mmdb". The name is lower-cased.
func (r geoipRepo) mmdbPath(cacheDir, ipVersion string) string {
	name := strings.ToLower(r.Name)
	mmdbDir := filepath.Join(cacheDir, "geoip", "mmdb")
	if r.isCombined() {
		return filepath.Join(mmdbDir, fmt.Sprintf("%s-combined.mmdb", name))
	}
	return filepath.Join(mmdbDir, fmt.Sprintf("%s-%s.mmdb", name, ipVersion))
}

// needsUpdate reports whether the mmdb at path is missing or older than 30 days.
// Ports needs_update.
func needsUpdate(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > updateMaxAge
}

// downloadMMDB downloads (and, for combined repos, extracts) the mmdb for the
// given ip version into dstPath, trying each candidate URL in order. Ports the
// per-url loop in download_mmdb.
func downloadMMDB(r geoipRepo, ipVersion, dstPath string) error {
	urlKey := ipVersion
	if r.isCombined() {
		urlKey = "combined"
	}
	urls := r.URLs[urlKey]
	if len(urls) == 0 {
		return fmt.Errorf("no download URLs for %s (%s)", r.Name, urlKey)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	var lastErr error
	for _, u := range urls {
		if err := downloadOne(u, dstPath, r.Extract); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to download %s", ipVersion)
	}
	return lastErr
}

// downloadOne fetches url to a temp file and either copies it to dstPath or, if
// extract is set, unzips the archive and moves the first .mmdb inside to
// dstPath.
func downloadOne(url, dstPath string, extract bool) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}

	suffix := ".mmdb"
	if extract {
		suffix = ".zip"
	}
	tmp, err := os.CreateTemp("", "camoufox-geoip-*"+suffix)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if extract {
		return extractMMDBFromZip(tmpName, dstPath)
	}

	return copyFile(tmpName, dstPath)
}

// extractMMDBFromZip unzips zipPath and moves the first .mmdb entry to dstPath.
func extractMMDBFromZip(zipPath, dstPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".mmdb") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(dstPath)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(dst, rc)
		rc.Close()
		closeErr := dst.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	}
	return fmt.Errorf("no .mmdb file found in archive")
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// findInRecord resolves a dotted path within a nested map record. Ports
// _find_in.
func findInRecord(record map[string]any, key string) any {
	var current any = record
	for _, part := range strings.Split(key, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok || current == nil {
			return nil
		}
	}
	return current
}

// toFloat coerces a decoded mmdb numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

// toString coerces a decoded mmdb value to its string form.
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// Resolve returns the Geolocation for an IP address using the named GeoIP
// database (default "MaxMind GeoLite2" when dbName is empty).
//
// It selects the repo config, determines the ip version, ensures the mmdb is
// cached under cacheDir/geoip/mmdb (downloading when missing or older than 30
// days), looks up the ip, resolves the dotted record paths, upper-cases the ISO
// code, derives a Locale via locale.FromRegion, and returns the Geolocation.
// If rng is nil a time-seeded generator is used by locale.FromRegion.
// Ports get_geolocation.
func Resolve(ip string, dbName string, cacheDir string, rng *rand.Rand) (Geolocation, error) {
	if err := validateIP(ip); err != nil {
		return Geolocation{}, err
	}

	repo, err := getRepoByName(dbName)
	if err != nil {
		return Geolocation{}, err
	}

	ipVersion := "ipv4"
	if strings.Contains(ip, ":") {
		ipVersion = "ipv6"
	}

	mmdbPath := repo.mmdbPath(cacheDir, ipVersion)
	if needsUpdate(mmdbPath) {
		if err := downloadMMDB(repo, ipVersion, mmdbPath); err != nil {
			return Geolocation{}, fmt.Errorf("failed to download GeoIP database: %w", err)
		}
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Geolocation{}, fmt.Errorf("invalid IP address: %s", ip)
	}

	reader, err := maxminddb.Open(mmdbPath)
	if err != nil {
		return Geolocation{}, fmt.Errorf("failed to open GeoIP database: %w", err)
	}
	defer reader.Close()

	result := reader.Lookup(addr)
	if err := result.Err(); err != nil {
		return Geolocation{}, fmt.Errorf("lookup failed: %w", err)
	}
	if !result.Found() {
		return Geolocation{}, fmt.Errorf("IP not found in database: %s", ip)
	}

	var record map[string]any
	if err := result.Decode(&record); err != nil {
		return Geolocation{}, fmt.Errorf("failed to decode record: %w", err)
	}
	if len(record) == 0 {
		return Geolocation{}, fmt.Errorf("IP not found in database: %s", ip)
	}

	isoCode := strings.ToUpper(toString(findInRecord(record, repo.Paths.ISOCode)))
	if isoCode == "" || isoCode == "<NIL>" {
		return Geolocation{}, fmt.Errorf("no country code for IP: %s", ip)
	}

	longitude, ok := toFloat(findInRecord(record, repo.Paths.Longitude))
	if !ok {
		return Geolocation{}, fmt.Errorf("no longitude for IP: %s", ip)
	}
	latitude, ok := toFloat(findInRecord(record, repo.Paths.Latitude))
	if !ok {
		return Geolocation{}, fmt.Errorf("no latitude for IP: %s", ip)
	}
	timezone := toString(findInRecord(record, repo.Paths.Timezone))

	loc, err := locale.FromRegion(isoCode, rng)
	if err != nil {
		return Geolocation{}, err
	}

	return Geolocation{
		Locale:    loc,
		Longitude: longitude,
		Latitude:  latitude,
		Timezone:  timezone,
	}, nil
}

// ResolveForProxy is a convenience that resolves the public IP behind the given
// proxy (or the local public IP when proxy is nil) and returns its Geolocation.
func ResolveForProxy(proxy *Proxy, dbName string, cacheDir string, rng *rand.Rand) (Geolocation, error) {
	ip, err := PublicIP(proxy)
	if err != nil {
		return Geolocation{}, err
	}
	return Resolve(ip, dbName, cacheDir, rng)
}
