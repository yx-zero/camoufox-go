// Tests for the addons package.
// All tests are offline and require NO network access.
package addons

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ─── Default enum ───────────────────────────────────────────────────────────────

func TestDefaultNameURL(t *testing.T) {
	if got := UBO.Name(); got != "UBO" {
		t.Errorf("UBO.Name() = %q, want %q", got, "UBO")
	}
	want := "https://addons.mozilla.org/firefox/downloads/latest/ublock-origin/latest.xpi"
	if got := UBO.URL(); got != want {
		t.Errorf("UBO.URL() = %q, want %q", got, want)
	}
}

func TestDefaultList(t *testing.T) {
	list := DefaultList()
	if len(list) != 1 {
		t.Fatalf("DefaultList() len = %d, want 1", len(list))
	}
	if list[0] != UBO {
		t.Errorf("DefaultList()[0] = %v, want UBO", list[0])
	}
}

func TestDefaultOutOfRange(t *testing.T) {
	var d Default = 99
	if got := d.Name(); got != "Default(99)" {
		t.Errorf("out-of-range Name() = %q, want %q", got, "Default(99)")
	}
	if got := d.URL(); got != "" {
		t.Errorf("out-of-range URL() = %q, want empty", got)
	}
}

// ─── ConfirmPaths ───────────────────────────────────────────────────────────────

func TestConfirmPaths(t *testing.T) {
	tmp := t.TempDir()

	// Valid addon dir with manifest.json.
	valid := filepath.Join(tmp, "valid")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dir without manifest.json.
	noManifest := filepath.Join(tmp, "no_manifest")
	if err := os.MkdirAll(noManifest, 0o755); err != nil {
		t.Fatal(err)
	}

	// A file, not a directory.
	notDir := filepath.Join(tmp, "afile")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		paths   []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"valid", []string{valid}, false},
		{"missing-manifest", []string{noManifest}, true},
		{"not-a-dir", []string{notDir}, true},
		{"nonexistent", []string{filepath.Join(tmp, "nope")}, true},
		{"mixed", []string{valid, noManifest}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ConfirmPaths(tt.paths)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConfirmPaths(%v) err = %v, wantErr = %v", tt.paths, err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				var iae *InvalidAddonPathError
				if !errors.As(err, &iae) {
					t.Errorf("err type = %T, want *InvalidAddonPathError", err)
				}
			}
		})
	}
}

// ─── zip extraction + zip-slip guard ────────────────────────────────────────────

// makeZip builds an in-memory zip with the given name→content entries.
func makeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipBytes(t *testing.T) {
	dest := t.TempDir()
	data := makeZip(t, map[string]string{
		"manifest.json":      `{"name":"x"}`,
		"js/background.js":   "console.log(1)",
		"_locales/en/x.json": "{}",
	})
	if err := extractZipBytes(data, dest); err != nil {
		t.Fatalf("extractZipBytes: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"name":"x"}` {
		t.Errorf("manifest.json content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "js", "background.js")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
}

func TestExtractZipSlipRejected(t *testing.T) {
	dest := t.TempDir()
	// Entry attempts to escape the destination via ../.
	data := makeZip(t, map[string]string{
		"../evil.txt": "pwned",
	})
	err := extractZipBytes(data, dest)
	if err == nil {
		t.Fatal("expected zip-slip to be rejected, got nil error")
	}
	// Ensure nothing was written outside dest.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); statErr == nil {
		t.Fatal("zip-slip wrote a file outside the destination directory")
	}
}

// ─── Resolve (offline) ──────────────────────────────────────────────────────────

func TestResolveReusesExistingAndOrders(t *testing.T) {
	cache := t.TempDir()

	// Pre-create the UBO addon dir so Resolve reuses it (no network).
	ubo := filepath.Join(cache, AddonsDirName, "UBO")
	if err := os.MkdirAll(ubo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ubo, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A valid user addon.
	user := filepath.Join(t.TempDir(), "myaddon")
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(cache, []string{user}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []string{ubo, user}
	if len(got) != len(want) {
		t.Fatalf("Resolve len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Resolve[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveExcludeSkipsDownload(t *testing.T) {
	cache := t.TempDir() // UBO dir does NOT exist; excluding it must avoid network.

	got, err := Resolve(cache, nil, []Default{UBO})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Resolve with UBO excluded = %v, want empty", got)
	}
}

func TestResolveInvalidUserPath(t *testing.T) {
	cache := t.TempDir()
	_, err := Resolve(cache, []string{filepath.Join(cache, "does-not-exist")}, []Default{UBO})
	if err == nil {
		t.Fatal("expected error for invalid user addon path")
	}
	var iae *InvalidAddonPathError
	if !errors.As(err, &iae) {
		t.Errorf("err type = %T, want *InvalidAddonPathError", err)
	}
}
