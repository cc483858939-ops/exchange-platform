package controllers

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

const (
	recommendationReadPolicyVersion = "read_v1"

	recommendationReadOutcomeQualified   = "qualified"
	recommendationReadOutcomeQuickBounce = "quick_bounce"
	recommendationReadOutcomeNeutral     = "neutral"

	recommendationReadMinimumDwellMS         int64 = 3 * 1000
	recommendationReadMaxForegroundMS        int64 = 6 * 60 * 60 * 1000
	recommendationReadMaxProgress                  = 100
	recommendationReadMinimumProgress              = 50
	recommendationReadQuickBounceProgress          = 10
	recommendationReadCJKCharactersPerMinute       = 300
	recommendationReadLatinWordsPerMinute          = 220
	recommendationReadMinimumEstimateMS      int64 = 3 * 1000
	recommendationReadMaximumEstimateMS      int64 = 120 * 1000
)

func classifyRecommendationRead(foregroundTimeMS int64, scrollProgressPercent int, estimatedReadTimeMS int64, readPolicyVersion string) (string, error) {
	if strings.TrimSpace(readPolicyVersion) != recommendationReadPolicyVersion {
		return "", errors.New("unsupported recommendation read policy")
	}
	if foregroundTimeMS < 0 || foregroundTimeMS > recommendationReadMaxForegroundMS {
		return "", errors.New("invalid foreground time")
	}
	if scrollProgressPercent < 0 || scrollProgressPercent > recommendationReadMaxProgress {
		return "", errors.New("invalid scroll progress")
	}
	if estimatedReadTimeMS <= 0 {
		return "", errors.New("invalid estimated read time")
	}

	minimumEngagedDwell := recommendationReadDwellThreshold(estimatedReadTimeMS, 35, 20*1000)
	strongDwell := recommendationReadDwellThreshold(estimatedReadTimeMS, 80, 45*1000)
	if foregroundTimeMS >= strongDwell ||
		(foregroundTimeMS >= minimumEngagedDwell && scrollProgressPercent >= recommendationReadMinimumProgress) {
		return recommendationReadOutcomeQualified, nil
	}
	if foregroundTimeMS < recommendationReadMinimumDwellMS &&
		scrollProgressPercent < recommendationReadQuickBounceProgress {
		return recommendationReadOutcomeQuickBounce, nil
	}
	return recommendationReadOutcomeNeutral, nil
}

func recommendationReadDwellThreshold(estimatedReadTimeMS, numerator, maximum int64) int64 {
	rounded := (estimatedReadTimeMS*numerator + 50) / 100
	if rounded < recommendationReadMinimumDwellMS {
		return recommendationReadMinimumDwellMS
	}
	if rounded > maximum {
		return maximum
	}
	return rounded
}

func estimateArticleReadTime(content string) time.Duration {
	var cjkCharacters int64
	var latinWords int64
	inLatinWord := false
	for _, r := range content {
		if isRecommendationReadCJKLike(r) {
			inLatinWord = false
			cjkCharacters++
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			inLatinWord = true
			continue
		}
		if inLatinWord {
			latinWords++
			inLatinWord = false
		}
	}
	if inLatinWord {
		latinWords++
	}

	// Keep both rates as exact integer contributions, then round the mixed
	// result upward once so CJK characters are never double-counted as words.
	minuteMilliseconds := int64(time.Minute / time.Millisecond)
	denominator := int64(recommendationReadCJKCharactersPerMinute) * int64(recommendationReadLatinWordsPerMinute)
	numerator := cjkCharacters*minuteMilliseconds*int64(recommendationReadLatinWordsPerMinute) +
		latinWords*minuteMilliseconds*int64(recommendationReadCJKCharactersPerMinute)
	rawMilliseconds := (numerator + denominator - 1) / denominator
	if rawMilliseconds < recommendationReadMinimumEstimateMS {
		rawMilliseconds = recommendationReadMinimumEstimateMS
	}
	if rawMilliseconds > recommendationReadMaximumEstimateMS {
		rawMilliseconds = recommendationReadMaximumEstimateMS
	}
	return time.Duration(rawMilliseconds) * time.Millisecond
}

func isRecommendationReadCJKLike(r rune) bool {
	switch {
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0x20000 && r <= 0x2FA1F:
		return true
	case r >= 0x3040 && r <= 0x30FF:
		return true
	case r >= 0x31F0 && r <= 0x31FF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	case r >= 0x1100 && r <= 0x11FF:
		return true
	case r >= 0x3130 && r <= 0x318F:
		return true
	default:
		return false
	}
}

func recommendationReadPolicyVersionValue() string {
	return recommendationReadPolicyVersion
}

func recommendationReadOutcomeIsValid(outcome string) bool {
	switch outcome {
	case recommendationReadOutcomeQualified, recommendationReadOutcomeQuickBounce, recommendationReadOutcomeNeutral:
		return true
	default:
		return false
	}
}
