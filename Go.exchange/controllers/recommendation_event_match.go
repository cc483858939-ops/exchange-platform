package controllers

import "Go.exchange/models"

func recommendationReadPayloadMatches(existing, incoming models.RecommendationEvent) bool {
	return equalInt64Pointers(existing.ForegroundTimeMS, incoming.ForegroundTimeMS) &&
		equalIntPointers(existing.MaxScrollDepth, incoming.MaxScrollDepth) &&
		equalStringPointers(existing.ExitType, incoming.ExitType) &&
		existing.QualifiedRead == incoming.QualifiedRead &&
		existing.QuickBounce == incoming.QuickBounce
}

func equalInt64Pointers(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalIntPointers(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
