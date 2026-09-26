package controllers

import (
	"math"
	"strconv"
	"strings"

	"Go.exchange/recommendation"
)

const (
	recommendationAcceptLanguageMaxBytes  = 512
	recommendationAcceptLanguageMaxRanges = 20
)

// parseRecommendationAcceptLanguage parses the bounded Accept-Language HTTP
// hint. Unsupported or invalid ranges are ignored without failing serving.
func parseRecommendationAcceptLanguage(raw string) recommendation.LanguagePrior {
	prior, _ := parseRecommendationAcceptLanguageWithPrimary(raw)
	return prior
}

func parseRecommendationAcceptLanguageWithPrimary(raw string) (recommendation.LanguagePrior, string) {
	if len(raw) > recommendationAcceptLanguageMaxBytes {
		raw = raw[:recommendationAcceptLanguageMaxBytes]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return recommendation.LanguagePrior{}, ""
	}

	ranges := strings.Split(raw, ",")
	if len(ranges) > recommendationAcceptLanguageMaxRanges {
		ranges = ranges[:recommendationAcceptLanguageMaxRanges]
	}
	weights := make(map[string]float64, 3)
	order := make([]string, 0, 3)
	for _, rawRange := range ranges {
		parts := strings.Split(rawRange, ";")
		language, ok := recommendation.CanonicalLanguage(strings.TrimSpace(parts[0]))
		if !ok {
			continue
		}
		quality, valid := recommendationLanguageQuality(parts[1:])
		if !valid || quality <= 0 {
			continue
		}
		if _, seen := weights[language]; !seen {
			order = append(order, language)
			weights[language] = quality
		} else if quality > weights[language] {
			weights[language] = quality
		}
	}

	total := 0.0
	for _, language := range order {
		total += weights[language]
	}
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return recommendation.LanguagePrior{}, ""
	}
	prior := recommendation.LanguagePrior{}
	for _, language := range order {
		weight := weights[language] / total
		switch language {
		case recommendation.LanguageZH:
			prior.ZH = weight
		case recommendation.LanguageJA:
			prior.JA = weight
		case recommendation.LanguageEN:
			prior.EN = weight
		}
	}

	primary := ""
	bestQuality := -1.0
	for _, language := range order {
		quality := weights[language]
		if quality > bestQuality {
			bestQuality = quality
			primary = language
		}
	}
	return prior, primary
}

func recommendationLanguageQuality(parameters []string) (float64, bool) {
	quality := 1.0
	qualitySeen := false
	for _, rawParameter := range parameters {
		parameter := strings.TrimSpace(rawParameter)
		if parameter == "" {
			continue
		}
		parts := strings.SplitN(parameter, "=", 2)
		if len(parts) != 2 {
			if strings.EqualFold(strings.TrimSpace(parameter), "q") {
				return 0, false
			}
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "q") {
			continue
		}
		if qualitySeen {
			return 0, false
		}
		qualitySeen = true
		value, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return 0, false
		}
		quality = value
	}
	return quality, true
}
