package translation

import (
	"strings"
	"testing"
)

func TestCacheKeyUsesHashedContentAndConfigurationIdentity(t *testing.T) {
	content := "do not put this post in a cache key"
	key := CacheKey(42, content, "zh", "en", "qwen/qwen3.6-27b", "social_v1")

	if strings.Contains(key, content) {
		t.Fatalf("cache key contains raw post content: %q", key)
	}
	if !strings.Contains(key, ContentHash(content)) {
		t.Fatalf("cache key does not contain the content hash: %q", key)
	}
	if key == CacheKey(42, content+"!", "zh", "en", "qwen/qwen3.6-27b", "social_v1") {
		t.Fatal("different post content produced the same cache key")
	}
	if key == CacheKey(42, content, "zh", "en", "another-model", "social_v1") {
		t.Fatal("different model identity produced the same cache key")
	}
	if key == CacheKey(42, content, "zh", "en", "qwen/qwen3.6-27b", "social_v2") {
		t.Fatal("different prompt version produced the same cache key")
	}
}

func TestNormalizeLanguages(t *testing.T) {
	for _, testCase := range []struct {
		raw      string
		expected string
		valid    bool
	}{
		{raw: "zh", expected: "zh", valid: true},
		{raw: "ja", expected: "ja", valid: true},
		{raw: "en", expected: "en", valid: true},
		{raw: "ZH", expected: "", valid: false},
		{raw: " zh ", expected: "", valid: false},
		{raw: "zh-CN", expected: "", valid: false},
		{raw: "fr", expected: "", valid: false},
	} {
		got, valid := NormalizeTargetLanguage(testCase.raw)
		if got != testCase.expected || valid != testCase.valid {
			t.Errorf("NormalizeTargetLanguage(%q) = %q, %v; want %q, %v", testCase.raw, got, valid, testCase.expected, testCase.valid)
		}
	}

	if got := NormalizeSourceLanguage("fr"); got != LanguageUndetermined {
		t.Fatalf("unknown source language = %q, want %q", got, LanguageUndetermined)
	}
}
