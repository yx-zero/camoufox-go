// Package webgl ports camoufox's WebGL fingerprint sampling.
//
// It selects a WebGL vendor/renderer fingerprint for a given OS, either by an
// explicit vendor+renderer pair or via weighted-random selection based on the
// per-OS probabilities baked into the embedded dataset.
//
// The package is pure Go (stdlib only) and embeds its dataset via go:embed, so
// it has no sqlite or other external dependency at runtime.
package webgl

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

//go:embed webgl_data.json
var webglDataJSON []byte

// fingerprint is one row of the embedded WebGL dataset.
type fingerprint struct {
	Vendor   string          `json:"vendor"`
	Renderer string          `json:"renderer"`
	Data     json.RawMessage `json:"data"`
	Win      float64         `json:"win"`
	Mac      float64         `json:"mac"`
	Lin      float64         `json:"lin"`
}

// prob returns the probability of this row for the given OS key.
func (f *fingerprint) prob(os string) float64 {
	switch os {
	case "win":
		return f.Win
	case "mac":
		return f.Mac
	case "lin":
		return f.Lin
	default:
		return 0
	}
}

var (
	loadOnce sync.Once
	rows     []fingerprint
	loadErr  error
)

// validOS mirrors camoufox's OS_ARCH_MATRIX keys.
func validOS(os string) bool {
	return os == "win" || os == "mac" || os == "lin"
}

// load parses the embedded dataset exactly once.
func load() ([]fingerprint, error) {
	loadOnce.Do(func() {
		loadErr = json.Unmarshal(webglDataJSON, &rows)
	})
	if loadErr != nil {
		return nil, fmt.Errorf("webgl: failed to parse embedded data: %w", loadErr)
	}
	return rows, nil
}

// Sample selects a WebGL fingerprint for the given OS.
//
// os must be one of "win", "mac", or "lin".
//
// If both vendor and renderer are non-empty, the matching row is used; it is an
// error if no such row exists, or if that row's probability for os is <= 0
// ("combination not valid for <os>"). Otherwise a row is chosen by weighted-
// random selection among rows whose probability for os is > 0, using rng. If rng
// is nil, a new one is seeded from the current time.
//
// The selected row's data JSON is returned as a map[string]any with the
// "webGl2Enabled" key removed and surfaced as webgl2Enabled (default false if
// absent). The remaining keys are CAMOU_CONFIG keys ready to merge, including
// webGl:vendor and webGl:renderer.
func Sample(os string, vendor, renderer string, rng *rand.Rand) (data map[string]any, webgl2Enabled bool, err error) {
	if !validOS(os) {
		return nil, false, fmt.Errorf("webgl: invalid OS: %s. Must be one of: win, mac, lin", os)
	}

	all, err := load()
	if err != nil {
		return nil, false, err
	}

	var chosen *fingerprint

	if vendor != "" && renderer != "" {
		for i := range all {
			if all[i].Vendor == vendor && all[i].Renderer == renderer {
				chosen = &all[i]
				break
			}
		}
		if chosen == nil {
			return nil, false, fmt.Errorf(
				"webgl: no WebGL data found for vendor %q and renderer %q", vendor, renderer)
		}
		if chosen.prob(os) <= 0 {
			return nil, false, fmt.Errorf(
				"webgl: vendor %q and renderer %q combination not valid for %s", vendor, renderer, os)
		}
	} else {
		// Weighted-random selection among rows with prob > 0 for this OS.
		var (
			candidates []*fingerprint
			total      float64
		)
		for i := range all {
			if p := all[i].prob(os); p > 0 {
				candidates = append(candidates, &all[i])
				total += p
			}
		}
		if len(candidates) == 0 {
			return nil, false, fmt.Errorf("webgl: no WebGL data found for OS: %s", os)
		}

		if rng == nil {
			rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		}

		// Cumulative pick using a normalized target in [0, total).
		target := rng.Float64() * total
		var cum float64
		for _, c := range candidates {
			cum += c.prob(os)
			if target < cum {
				chosen = c
				break
			}
		}
		if chosen == nil {
			// Float rounding guard: fall back to the last candidate.
			chosen = candidates[len(candidates)-1]
		}
	}

	if err := json.Unmarshal(chosen.Data, &data); err != nil {
		return nil, false, fmt.Errorf("webgl: failed to parse fingerprint data: %w", err)
	}

	if v, ok := data["webGl2Enabled"]; ok {
		if b, ok := v.(bool); ok {
			webgl2Enabled = b
		}
		delete(data, "webGl2Enabled")
	}

	return data, webgl2Enabled, nil
}

// PossiblePairs returns the distinct vendor/renderer pairs whose probability for
// the given OS is greater than 0, ordered by that probability descending.
//
// It returns nil if os is invalid or the dataset cannot be loaded.
func PossiblePairs(os string) [][2]string {
	if !validOS(os) {
		return nil
	}
	all, err := load()
	if err != nil {
		return nil
	}

	type pair struct {
		vr   [2]string
		prob float64
	}
	var pairs []pair
	seen := make(map[[2]string]bool)
	for i := range all {
		p := all[i].prob(os)
		if p <= 0 {
			continue
		}
		vr := [2]string{all[i].Vendor, all[i].Renderer}
		if seen[vr] {
			continue
		}
		seen[vr] = true
		pairs = append(pairs, pair{vr: vr, prob: p})
	}

	sort.SliceStable(pairs, func(i, j int) bool {
		return pairs[i].prob > pairs[j].prob
	})

	out := make([][2]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.vr
	}
	return out
}
