package locale

import (
	_ "embed"
	"encoding/xml"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed territoryInfo.xml
var territoryInfoXML []byte

// territoryInfo mirrors the root <territoryInfo> element.
type territoryInfo struct {
	Territories []territory `xml:"territory"`
}

// territory mirrors a <territory> element.
type territory struct {
	Type            string               `xml:"type,attr"`
	LiteracyPercent string               `xml:"literacyPercent,attr"`
	Population      string               `xml:"population,attr"`
	Languages       []languagePopulation `xml:"languagePopulation"`
}

// languagePopulation mirrors a <languagePopulation> element.
type languagePopulation struct {
	Type              string `xml:"type,attr"`
	PopulationPercent string `xml:"populationPercent,attr"`
}

var (
	parsedInfo *territoryInfo
	parseOnce  sync.Once
	parseErr   error
)

// loadTerritoryInfo lazily parses the embedded territoryInfo.xml once.
func loadTerritoryInfo() (*territoryInfo, error) {
	parseOnce.Do(func() {
		var info territoryInfo
		if err := xml.Unmarshal(territoryInfoXML, &info); err != nil {
			parseErr = fmt.Errorf("failed to parse territoryInfo.xml: %w", err)
			return
		}
		parsedInfo = &info
	})
	return parsedInfo, parseErr
}

// asFloat converts an attribute string to a float, defaulting to 0.
// Ports _as_float.
func asFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// rngOrDefault returns the supplied rng, or a new time-seeded one when nil.
func rngOrDefault(rng *rand.Rand) *rand.Rand {
	if rng != nil {
		return rng
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// weightedChoice performs a cumulative weighted-random pick over items using
// the parallel weights slice. Weights are normalized internally. Returns the
// chosen item. items and weights must be the same non-zero length.
func weightedChoice(items []string, weights []float64, rng *rand.Rand) string {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		// Degenerate case: fall back to a uniform pick.
		return items[rng.Intn(len(items))]
	}

	target := rng.Float64() * total
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if target < cumulative {
			return items[i]
		}
	}
	// Floating point fallback.
	return items[len(items)-1]
}

// FromRegion returns a statistically plausible locale for a territory ISO code.
//
// It finds territory[@type=isoCode] in territoryInfo.xml, reads each
// languagePopulation's type and populationPercent, performs a weighted-random
// pick of the language, and returns Normalize(lang-REGION). If rng is nil a
// time-seeded generator is used. Ports StatisticalLocaleSelector.from_region.
func FromRegion(isoCode string, rng *rand.Rand) (Locale, error) {
	info, err := loadTerritoryInfo()
	if err != nil {
		return Locale{}, err
	}

	region := strings.ToUpper(strings.TrimSpace(isoCode))
	var terr *territory
	for i := range info.Territories {
		if strings.EqualFold(info.Territories[i].Type, region) {
			terr = &info.Territories[i]
			break
		}
	}
	if terr == nil {
		return Locale{}, fmt.Errorf("unknown territory: %s", isoCode)
	}
	if len(terr.Languages) == 0 {
		return Locale{}, fmt.Errorf("no language data found for region: %s", isoCode)
	}

	languages := make([]string, 0, len(terr.Languages))
	weights := make([]float64, 0, len(terr.Languages))
	for _, lp := range terr.Languages {
		languages = append(languages, lp.Type)
		weights = append(weights, asFloat(lp.PopulationPercent))
	}

	chosen := weightedChoice(languages, weights, rngOrDefault(rng))
	// CLDR uses '_' to join language+script (e.g. uz_Arab); BCP-47 uses '-'.
	chosen = strings.ReplaceAll(chosen, "_", "-")

	return Normalize(fmt.Sprintf("%s-%s", chosen, region))
}

// FromLanguage returns a statistically plausible locale for a language code.
//
// It finds every territory containing a languagePopulation of the given
// language, weights each region by populationPercent*literacyPercent/10000*
// population, performs a weighted-random pick of the region, and returns
// Normalize(lang-REGION). If rng is nil a time-seeded generator is used.
// Ports StatisticalLocaleSelector.from_language.
func FromLanguage(lang string, rng *rand.Rand) (Locale, error) {
	info, err := loadTerritoryInfo()
	if err != nil {
		return Locale{}, err
	}

	language := strings.TrimSpace(lang)

	regions := make([]string, 0)
	weights := make([]float64, 0)
	for i := range info.Territories {
		terr := &info.Territories[i]
		if terr.Type == "" {
			continue
		}
		var langPop *languagePopulation
		for j := range terr.Languages {
			if terr.Languages[j].Type == language {
				langPop = &terr.Languages[j]
				break
			}
		}
		if langPop == nil {
			continue
		}
		weight := asFloat(langPop.PopulationPercent) *
			asFloat(terr.LiteracyPercent) / 10000.0 *
			asFloat(terr.Population)
		regions = append(regions, terr.Type)
		weights = append(weights, weight)
	}

	if len(regions) == 0 {
		return Locale{}, fmt.Errorf("no region data found for language: %s", lang)
	}

	region := weightedChoice(regions, weights, rngOrDefault(rng))
	return Normalize(fmt.Sprintf("%s-%s", language, region))
}
