package controllers

import (
	"math"
	"strconv"
	"strings"

	"Go.exchange/config"
)

const (
	recommendationLanguageZH              = "zh"
	recommendationLanguageJA              = "ja"
	recommendationLanguageEN              = "en"
	recommendationLanguageUnd             = "und"
	recommendationAcceptLanguageMaxBytes  = 512
	recommendationAcceptLanguageMaxRanges = 20
)

type recommendationLanguagePrior struct {
	ZH float64
	JA float64
	EN float64
}

type recommendationLanguageContext struct {
	Browser          recommendationLanguagePrior
	Behavior         recommendationLanguagePrior
	Combined         recommendationLanguagePrior
	BrowserPrimary   string
	BehaviorEvidence float64
	BehaviorShare    float64
	Source           string
}

// parseRecommendationAcceptLanguage parses only the bounded, supported part
// of Accept-Language. It intentionally returns no error because an invalid or
// unsupported client hint must never make the recommendation request fail.
func parseRecommendationAcceptLanguage(raw string) recommendationLanguagePrior {
	prior, _ := parseRecommendationAcceptLanguageWithPrimary(raw)
	return prior
}

func parseRecommendationAcceptLanguageWithPrimary(raw string) (recommendationLanguagePrior, string) {
	if len(raw) > recommendationAcceptLanguageMaxBytes {
		raw = raw[:recommendationAcceptLanguageMaxBytes]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return recommendationLanguagePrior{}, ""
	}

	ranges := strings.Split(raw, ",")
	if len(ranges) > recommendationAcceptLanguageMaxRanges {
		ranges = ranges[:recommendationAcceptLanguageMaxRanges]
	}
	weights := make(map[string]float64, 3)
	order := make([]string, 0, 3)
	for _, rawRange := range ranges {
		parts := strings.Split(rawRange, ";")
		language, ok := canonicalRecommendationLanguage(strings.TrimSpace(parts[0]))
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
		return recommendationLanguagePrior{}, ""
	}
	prior := recommendationLanguagePrior{}
	for _, language := range order {
		weight := weights[language] / total
		switch language {
		case recommendationLanguageZH:
			prior.ZH = weight
		case recommendationLanguageJA:
			prior.JA = weight
		case recommendationLanguageEN:
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

func canonicalRecommendationLanguage(raw string) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "_", "-")))
	if raw == "" {
		return "", false
	}
	parts := strings.Split(raw, "-")
	if len(parts) == 0 || len(parts[0]) < 2 || len(parts[0]) > 8 {
		return "", false
	}
	for index, part := range parts {
		if part == "" || len(part) > 8 {
			return "", false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return "", false
			}
			if index == 0 && (character < 'a' || character > 'z') {
				return "", false
			}
		}
	}
	switch parts[0] {
	case recommendationLanguageZH, recommendationLanguageJA, recommendationLanguageEN:
		return parts[0], true
	default:
		return "", false
	}
}

func normalizeRecommendationLanguagePrior(prior recommendationLanguagePrior) recommendationLanguagePrior {
	zh := validPositiveRecommendationLanguageValue(prior.ZH)
	ja := validPositiveRecommendationLanguageValue(prior.JA)
	en := validPositiveRecommendationLanguageValue(prior.EN)
	total := zh + ja + en
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return recommendationLanguagePrior{}
	}
	return recommendationLanguagePrior{ZH: zh / total, JA: ja / total, EN: en / total}
}

func validPositiveRecommendationLanguageValue(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func recommendationLanguagePriorPresent(prior recommendationLanguagePrior) bool {
	return prior.ZH > 0 || prior.JA > 0 || prior.EN > 0
}

func normalizedMaterializedRecommendationLanguagePrior(zh, ja, en, evidence float64) (recommendationLanguagePrior, float64) {
	if evidence <= 0 || math.IsNaN(evidence) || math.IsInf(evidence, 0) {
		return recommendationLanguagePrior{}, 0
	}
	prior := recommendationLanguagePrior{
		ZH: validPositiveRecommendationLanguageValue(zh) / evidence,
		JA: validPositiveRecommendationLanguageValue(ja) / evidence,
		EN: validPositiveRecommendationLanguageValue(en) / evidence,
	}
	prior = normalizeRecommendationLanguagePrior(prior)
	if !recommendationLanguagePriorPresent(prior) {
		return recommendationLanguagePrior{}, 0
	}
	return prior, evidence
}

func buildRecommendationLanguageContext(browser recommendationLanguageContext, behavior recommendationLanguagePrior, behaviorEvidence float64, cfg config.RecommendationConfig) recommendationLanguageContext {
	browserPrior := normalizeRecommendationLanguagePrior(browser.Browser)
	behaviorPrior, evidence := normalizedMaterializedRecommendationLanguagePrior(
		behavior.ZH, behavior.JA, behavior.EN, behaviorEvidence,
	)
	result := recommendationLanguageContext{
		Browser:          browserPrior,
		Behavior:         behaviorPrior,
		BrowserPrimary:   normalizeRecommendationLanguagePrimary(browser.BrowserPrimary),
		BehaviorEvidence: evidence,
		Source:           "none",
	}
	browserPresent := recommendationLanguagePriorPresent(browserPrior)
	behaviorPresent := recommendationLanguagePriorPresent(behaviorPrior)
	switch {
	case browserPresent && behaviorPresent:
		scale := cfg.LanguageAffinity.EvidenceSaturationScale
		if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
			scale = 5
		}
		maxShare := cfg.LanguageAffinity.MaxBehaviorShare
		if maxShare < 0 || maxShare > 1 || math.IsNaN(maxShare) || math.IsInf(maxShare, 0) {
			maxShare = 0.95
		}
		share := evidence / (evidence + scale)
		if share < 0 || math.IsNaN(share) || math.IsInf(share, 0) {
			share = 0
		}
		if share > maxShare {
			share = maxShare
		}
		result.BehaviorShare = share
		result.Combined = recommendationLanguagePrior{
			ZH: share*behaviorPrior.ZH + (1-share)*browserPrior.ZH,
			JA: share*behaviorPrior.JA + (1-share)*browserPrior.JA,
			EN: share*behaviorPrior.EN + (1-share)*browserPrior.EN,
		}
		result.Source = "blended"
	case browserPresent:
		result.Combined = browserPrior
		result.Source = "browser"
	case behaviorPresent:
		result.Combined = behaviorPrior
		result.BehaviorShare = 1
		result.Source = "behavior"
	default:
		result.Browser = recommendationLanguagePrior{}
		result.Behavior = recommendationLanguagePrior{}
		result.Combined = recommendationLanguagePrior{}
		result.BrowserPrimary = ""
		result.BehaviorEvidence = 0
		result.BehaviorShare = 0
	}
	return result
}

func normalizeRecommendationLanguagePrimary(primary string) string {
	if primary == recommendationLanguageZH || primary == recommendationLanguageJA || primary == recommendationLanguageEN {
		return primary
	}
	return ""
}

func recommendationPostLanguage(postLanguage string) string {
	language, ok := canonicalRecommendationLanguage(postLanguage)
	if !ok {
		return recommendationLanguageUnd
	}
	return language
}

func recommendationLanguageAffinityForPost(postLanguage string, context recommendationLanguageContext) float64 {
	switch recommendationPostLanguage(postLanguage) {
	case recommendationLanguageZH:
		return clampUnit(context.Combined.ZH)
	case recommendationLanguageJA:
		return clampUnit(context.Combined.JA)
	case recommendationLanguageEN:
		return clampUnit(context.Combined.EN)
	default:
		return 0
	}
}
