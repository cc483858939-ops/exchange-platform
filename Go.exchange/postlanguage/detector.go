// Package postlanguage owns the canonical language metadata contract for
// Posts. Detection is intentionally local and limited to the languages the
// product currently stores as supported values.
package postlanguage

import (
	"strings"
	"unicode"

	"github.com/pemistahl/lingua-go"
)

const minimumRelativeDistance = 0.10

var detector = lingua.NewLanguageDetectorBuilder().
	FromLanguages(
		lingua.Chinese,
		lingua.Japanese,
		lingua.English,
	).
	WithMinimumRelativeDistance(minimumRelativeDistance).
	Build()

// Detect classifies Post content into the canonical language values.
func Detect(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "und"
	}
	language, exists := detector.DetectLanguageOf(text)
	if !exists {
		return "und"
	}
	switch language {
	case lingua.Chinese:
		return "zh"
	case lingua.Japanese:
		return "ja"
	case lingua.English:
		return "en"
	default:
		return "und"
	}
}

// NormalizeSource maps a source language tag to the canonical values. An
// empty or unsupported source is represented as und; ResolveSource is the API
// to use when an empty/und source should fall back to content detection.
func NormalizeSource(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "und"
	}
	normalized := strings.ToLower(strings.ReplaceAll(raw, "_", "-"))
	parts := strings.Split(normalized, "-")
	if len(parts) == 1 && parts[0] == "und" {
		return "und"
	}
	if len(parts) == 0 || (parts[0] != "zh" && parts[0] != "ja" && parts[0] != "en") {
		return "und"
	}
	for _, part := range parts[1:] {
		if part == "" || !isSourceTagPart(part) {
			return "und"
		}
	}
	return parts[0]
}

// ResolveSource prefers an explicitly supported source language. Missing or
// explicitly undetermined source metadata falls back to local content
// detection; an explicit unsupported source remains und rather than being
// relabeled as one of the supported languages.
func ResolveSource(raw string, text string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "und") {
		return Detect(text)
	}
	source := NormalizeSource(raw)
	if source == "zh" || source == "ja" || source == "en" {
		return source
	}
	return "und"
}

// IsCanonical reports whether language is one of the four stored values.
func IsCanonical(language string) bool {
	switch language {
	case "zh", "ja", "en", "und":
		return true
	default:
		return false
	}
}

func isSourceTagPart(part string) bool {
	for _, r := range part {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
