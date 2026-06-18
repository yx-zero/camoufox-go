// Package locale provides locale parsing, normalization, and statistical
// locale selection ported from the Python camoufox library (locales.py).
//
// It exposes a Locale type plus helpers to normalize BCP-47 tags and to pick
// a statistically plausible locale for a given territory (ISO country code) or
// language, weighted by CLDR territory/language population data embedded from
// territoryInfo.xml.
package locale

import (
	"fmt"
	"strings"
)

// Locale stores locale, region, and script information.
type Locale struct {
	Language string
	Region   string
	Script   string
}

// String returns "language-REGION" when a region is present, otherwise just
// the language. Ports Locale.as_string.
func (l Locale) String() string {
	if l.Region != "" {
		return l.Language + "-" + l.Region
	}
	return l.Language
}

// ConfigKeys converts the locale to an intl config map. Ports Locale.as_config.
// A region is required (mirrors the Python assert self.region).
func (l Locale) ConfigKeys() map[string]any {
	data := map[string]any{
		"locale:region":   l.Region,
		"locale:language": l.Language,
	}
	if l.Script != "" {
		data["locale:script"] = l.Script
	}
	return data
}

// Normalize parses and normalizes a BCP-47 locale tag of the form
// language[-script][-region]. It accepts '-' or '_' separators. A region is
// required; an error is returned if one cannot be determined.
//
// Ports normalize_locale. Uses a pragmatic parser instead of the full
// language-tags registry: the first subtag is the language, a 4-letter subtag
// is the script, and a 2-letter or 3-digit subtag is the region. The language
// is lower-cased, the region upper-cased, and the script Title-cased.
func Normalize(tag string) (Locale, error) {
	cleaned := strings.TrimSpace(tag)
	if cleaned == "" {
		return Locale{}, fmt.Errorf("invalid locale: %q", tag)
	}

	parts := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return Locale{}, fmt.Errorf("invalid locale: %q", tag)
	}

	loc := Locale{Language: strings.ToLower(parts[0])}

	for _, part := range parts[1:] {
		switch {
		case len(part) == 4 && isAlpha(part):
			// Script subtag (e.g. "Latn", "Hans").
			loc.Script = titleCase(part)
		case len(part) == 2 && isAlpha(part):
			// Region subtag (e.g. "US", "GB").
			loc.Region = strings.ToUpper(part)
		case len(part) == 3 && isDigit(part):
			// Numeric region subtag (e.g. "001", "419").
			loc.Region = part
		}
	}

	if loc.Region == "" {
		return Locale{}, fmt.Errorf("invalid locale (missing region): %q", tag)
	}
	if loc.Language == "" {
		return Locale{}, fmt.Errorf("invalid locale (missing language): %q", tag)
	}

	return loc, nil
}

// Handle handles a list of locales, merging the result into cfg.
//
// The first locale is normalized and its ConfigKeys merged into cfg. If more
// than one locale is supplied, cfg["locale:all"] is set to a comma-joined,
// de-duplicated list of each locale's String(); for the extras a language-only
// value is acceptable (ignoreRegion handling). Ports handle_locales.
func Handle(locales []string, cfg map[string]any) error {
	if len(locales) == 0 {
		return fmt.Errorf("no locales provided")
	}

	first, err := handleLocale(locales[0], false)
	if err != nil {
		return err
	}
	for k, v := range first.ConfigKeys() {
		cfg[k] = v
	}

	if len(locales) < 2 {
		return nil
	}

	values := make([]string, 0, len(locales))
	for _, l := range locales {
		loc, err := handleLocale(l, true)
		if err != nil {
			return err
		}
		values = append(values, loc.String())
	}
	cfg["locale:all"] = joinUnique(values)

	return nil
}

// handleLocale normalizes a single locale input. Ports handle_locale.
//
// If the input is longer than 3 characters it is treated as a full locale and
// normalized. Otherwise it is treated as a region first (from_region); failing
// that, when ignoreRegion is set a language-only Locale is returned; otherwise
// it is resolved by language (from_language).
func handleLocale(input string, ignoreRegion bool) (Locale, error) {
	trimmed := strings.TrimSpace(input)

	if len(trimmed) > 3 {
		return Normalize(trimmed)
	}

	// Case: user passed in a region and needs a full locale.
	if loc, err := FromRegion(trimmed, nil); err == nil {
		return loc, nil
	}

	// Case: user passed in a language and doesn't care about the region.
	if ignoreRegion {
		return Locale{Language: strings.ToLower(trimmed)}, nil
	}

	// Case: user passed in a language and wants a region.
	if loc, err := FromLanguage(trimmed, nil); err == nil {
		return loc, nil
	}

	return Locale{}, fmt.Errorf("invalid locale: %q", input)
}

// joinUnique joins strings with ", " dropping duplicates while preserving
// order. Ports _join_unique.
func joinUnique(seq []string) string {
	seen := make(map[string]struct{}, len(seq))
	out := make([]string, 0, len(seq))
	for _, x := range seq {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return strings.Join(out, ", ")
}

func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func isDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// titleCase upper-cases the first rune and lower-cases the rest (ASCII).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
