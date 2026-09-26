package controllers

import (
	"strings"

	"github.com/google/uuid"
)

func parseGuestRecommendationSessionID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return "", false
	}
	return parsed.String(), true
}
