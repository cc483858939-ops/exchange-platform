package eventing

import (
	"errors"
	"strings"
	"unicode/utf8"

	"Go.exchange/models"
)

// NormalizeEventID preserves opaque IDs and their case, while applying the
// same whitespace and storage contract at production, decode and Inbox edges.
// Consumers must retain the returned ID for deduplication and projection.
func NormalizeEventID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.IndexByte(id, 0) >= 0 || !utf8.ValidString(id) ||
		(len(id) > models.ConsumerInboxEventIDMaxLength && utf8.RuneCountInString(id) > models.ConsumerInboxEventIDMaxLength) {
		return "", errors.New("event id must contain 1 to 128 UTF-8 characters without NUL")
	}
	return id, nil
}
