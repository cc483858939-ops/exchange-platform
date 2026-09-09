package translation

import (
	"strings"
	"testing"
)

func TestCacheKeyUsesHashedContentAndConfigurationIdentity(t *testing.T) {
	content := "do not put this post in a cache key"
	backend := BackendIdentity("https://one.example/v1/", "test-model")
	key := CacheKey(42, content, "zh", "en", backend, "social_v1")
	if !strings.HasPrefix(key, "translation:v2:") {
		t.Fatalf("cache key namespace = %q", key)
	}
	if strings.HasPrefix(key, "translation:v1:") {
		t.Fatalf("cache key still uses the contaminated v1 namespace: %q", key)
	}

	if strings.Contains(key, content) {
		t.Fatalf("cache key contains raw post content: %q", key)
	}
	if !strings.Contains(key, ContentHash(content)) {
		t.Fatalf("cache key does not contain the content hash: %q", key)
	}
	if key == CacheKey(42, content+"!", "zh", "en", backend, "social_v1") {
		t.Fatal("different post content produced the same cache key")
	}
	if key == CacheKey(42, content, "zh", "en", BackendIdentity("https://two.example/v1", "test-model"), "social_v1") {
		t.Fatal("different BaseURL identity produced the same cache key")
	}
	if key == CacheKey(42, content, "zh", "en", BackendIdentity("https://one.example/v1", "another-model"), "social_v1") {
		t.Fatal("different model identity produced the same cache key")
	}
	if key == CacheKey(42, content, "zh", "en", backend, "social_v2") {
		t.Fatal("different prompt version produced the same cache key")
	}
	// API keys are intentionally absent from CacheKey's signature and therefore
	// cannot invalidate the same endpoint/model/prompt cache identity.
	if key != CacheKey(42, content, "zh", "en", BackendIdentity("https://one.example/v1", "test-model"), "social_v1") {
		t.Fatal("equivalent endpoint identity changed the cache key")
	}
}

func TestCacheKeyDoesNotDependOnAPIKey(t *testing.T) {
	keyForAPIKey := func(apiKey string) string {
		_ = apiKey
		return CacheKey(42, "你好", "zh", "en", BackendIdentity("https://one.example/v1", "test-model"), "social_v1")
	}

	if keyForAPIKey("key-a") != keyForAPIKey("key-b") {
		t.Fatal("changing API key changed the translation cache key")
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
