// Package postmention contains the bounded lexical rules for mentions in post
// bodies. Username resolution remains an exact database lookup.
package postmention

import (
	"errors"
	"strings"
	"unicode"
)

const (
	MaxCandidateTokens = 50
	MaxUsernameRunes   = 64
)

var ErrTooManyCandidates = errors.New("too many mention candidates")

// ExtractTokens recognizes @names made from Unicode letters, numbers and
// combining marks plus underscore and hyphen. Matching is exact and
// case-sensitive; usernames containing other punctuation cannot be mentioned
// in V1. Repeated spellings are returned once, in text order.
func ExtractTokens(body string) ([]string, error) {
	if !strings.ContainsRune(body, '@') {
		return nil, nil
	}
	runes := []rune(body)
	seen := make(map[string]struct{})
	tokens := make([]string, 0, 4)
	for i, current := range runes {
		if current != '@' || i+1 >= len(runes) || !validAtBoundary(runes, i) || inURL(runes, i) {
			continue
		}
		start := i + 1
		end := start
		for end < len(runes) && usernameRune(runes[end]) {
			end++
		}
		if end == start {
			continue
		}
		// Do not turn the prefix of a dotted handle or domain into a mention.
		if end < len(runes) && runes[end] == '.' && end+1 < len(runes) && usernameRune(runes[end+1]) {
			continue
		}
		if end-start > MaxUsernameRunes {
			continue
		}
		token := string(runes[start:end])
		if _, ok := seen[token]; ok {
			continue
		}
		if len(seen) == MaxCandidateTokens {
			return nil, ErrTooManyCandidates
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	return tokens, nil
}

// ValidPrefix applies the same character and length bounds to suggestion API
// prefixes. A single leading @ is accepted for callers that pass the editor
// token directly.
func ValidPrefix(raw string) (string, bool) {
	prefix := strings.TrimPrefix(raw, "@")
	if len([]rune(prefix)) > MaxUsernameRunes {
		return "", false
	}
	for _, r := range prefix {
		if !usernameRune(r) {
			return "", false
		}
	}
	return prefix, true
}

// ValidUsername reports whether username is fully recognizable by ExtractTokens.
func ValidUsername(username string) bool {
	if username == "" || len([]rune(username)) > MaxUsernameRunes {
		return false
	}
	for _, r := range username {
		if !usernameRune(r) {
			return false
		}
	}
	return true
}

func usernameRune(r rune) bool {
	switch {
	case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), r == '_', r == '-':
		return true
	default:
		return false
	}
}

func validAtBoundary(runes []rune, at int) bool {
	if at == 0 {
		return true
	}
	previous := runes[at-1]
	if previous == '@' || previous == '_' || previous == '-' || unicode.IsNumber(previous) || unicode.IsMark(previous) {
		return false
	}
	if unicode.IsLetter(previous) && !isCJK(previous) {
		return false
	}
	return true
}

func isCJK(r rune) bool {
	return (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func inURL(runes []rune, at int) bool {
	start := at
	for start > 0 && !unicode.IsSpace(runes[start-1]) && !strings.ContainsRune("([{<\"'", runes[start-1]) {
		start--
	}
	segment := strings.ToLower(string(runes[start:at]))
	if strings.Contains(segment, "://") || strings.HasPrefix(segment, "www.") {
		return true
	}
	if colon := strings.IndexRune(segment, ':'); colon > 0 && uriScheme(segment[:colon]) {
		return true
	}
	hostEnd := len(segment)
	if delimiter := strings.IndexAny(segment, "/?#"); delimiter >= 0 {
		hostEnd = delimiter
	}
	if strings.ContainsRune(segment[:hostEnd], '.') {
		return true
	}
	return false
}

func uriScheme(value string) bool {
	for i, r := range value {
		if i == 0 {
			if r < 'a' || r > 'z' {
				return false
			}
			continue
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '.' && r != '-' {
			return false
		}
	}
	return value != ""
}
