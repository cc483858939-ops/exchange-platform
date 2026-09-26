package recommendation

import (
	"math"
	"strings"
)

func CanonicalLanguage(raw string) (string, bool) {
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
	case LanguageZH, LanguageJA, LanguageEN:
		return parts[0], true
	default:
		return "", false
	}
}

func NormalizeLanguagePrior(prior LanguagePrior) LanguagePrior {
	zh := validPositiveLanguageValue(prior.ZH)
	ja := validPositiveLanguageValue(prior.JA)
	en := validPositiveLanguageValue(prior.EN)
	total := zh + ja + en
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return LanguagePrior{}
	}
	return LanguagePrior{ZH: zh / total, JA: ja / total, EN: en / total}
}

func NormalizeMaterializedLanguagePrior(zh, ja, en, evidence float64) (LanguagePrior, float64) {
	if evidence <= 0 || math.IsNaN(evidence) || math.IsInf(evidence, 0) {
		return LanguagePrior{}, 0
	}
	prior := LanguagePrior{
		ZH: validPositiveLanguageValue(zh) / evidence,
		JA: validPositiveLanguageValue(ja) / evidence,
		EN: validPositiveLanguageValue(en) / evidence,
	}
	prior = NormalizeLanguagePrior(prior)
	if !languagePriorPresent(prior) {
		return LanguagePrior{}, 0
	}
	return prior, evidence
}

func BuildLanguageContext(browser LanguageContext, behavior LanguagePrior, behaviorEvidence float64, cfg LanguageConfig) LanguageContext {
	browserPrior := NormalizeLanguagePrior(browser.Browser)
	behaviorPrior, evidence := NormalizeMaterializedLanguagePrior(
		behavior.ZH, behavior.JA, behavior.EN, behaviorEvidence,
	)
	result := LanguageContext{
		Browser:          browserPrior,
		Behavior:         behaviorPrior,
		BrowserPrimary:   NormalizeLanguagePrimary(browser.BrowserPrimary),
		BehaviorEvidence: evidence,
		Source:           "none",
	}
	browserPresent := languagePriorPresent(browserPrior)
	behaviorPresent := languagePriorPresent(behaviorPrior)
	switch {
	case browserPresent && behaviorPresent:
		scale := cfg.EvidenceSaturationScale
		if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
			scale = 5
		}
		maxShare := cfg.MaxBehaviorShare
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
		result.Combined = LanguagePrior{
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
		result.Browser = LanguagePrior{}
		result.Behavior = LanguagePrior{}
		result.Combined = LanguagePrior{}
		result.BrowserPrimary = ""
		result.BehaviorEvidence = 0
		result.BehaviorShare = 0
	}
	return result
}

func NormalizeLanguagePrimary(primary string) string {
	if primary == LanguageZH || primary == LanguageJA || primary == LanguageEN {
		return primary
	}
	return ""
}

func PostLanguage(postLanguage string) string {
	language, ok := CanonicalLanguage(postLanguage)
	if !ok {
		return LanguageUnd
	}
	return language
}

func LanguageAffinity(candidateLanguage string, prior LanguagePrior) float64 {
	switch PostLanguage(candidateLanguage) {
	case LanguageZH:
		return ClampUnit(prior.ZH)
	case LanguageJA:
		return ClampUnit(prior.JA)
	case LanguageEN:
		return ClampUnit(prior.EN)
	default:
		return 0
	}
}

// ScoreLanguage returns both the unweighted affinity used for trace facts and
// its bounded weighted contribution to the ranker score.
func ScoreLanguage(candidateLanguage string, prior LanguagePrior, cfg LanguageConfig) (float64, float64) {
	affinity := LanguageAffinity(candidateLanguage, prior)
	if !cfg.Enabled {
		return 0, 0
	}
	weight := cfg.Weight
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
		weight = 0
	}
	if weight > 1 {
		weight = 1
	}
	component := weight * affinity
	if math.IsNaN(component) || math.IsInf(component, 0) || component < 0 {
		component = 0
	}
	return affinity, component
}

func validPositiveLanguageValue(value float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func languagePriorPresent(prior LanguagePrior) bool {
	return prior.ZH > 0 || prior.JA > 0 || prior.EN > 0
}
