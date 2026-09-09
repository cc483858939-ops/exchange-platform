package postlanguage

import "testing"

func TestDetectClearPostContent(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "Chinese", text: "这个推荐系统现在越来越稳定了", want: "zh"},
		{name: "Chinese second sample", text: "今天准备继续优化内容搜索功能", want: "zh"},
		{name: "Japanese", text: "今日は新しい推薦システムを試しています", want: "ja"},
		{name: "Japanese second sample", text: "この機能はとても便利だと思います", want: "ja"},
		{name: "English", text: "The recommendation system is working much better now.", want: "en"},
		{name: "English second sample", text: "I am testing a new search feature today.", want: "en"},
		{name: "empty", text: "", want: "und"},
		{name: "whitespace", text: " \n\t ", want: "und"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Detect(test.text); got != test.want {
				t.Fatalf("Detect(%q)=%q want %q", test.text, got, test.want)
			}
		})
	}
}

func TestDetectIsDeterministicAndCanonical(t *testing.T) {
	text := "This is a deliberately ordinary English sentence for a stable test."
	want := Detect(text)
	if !IsCanonical(want) {
		t.Fatalf("Detect(%q) returned non-canonical value %q", text, want)
	}
	for i := 0; i < 20; i++ {
		if got := Detect(text); got != want {
			t.Fatalf("Detect(%q) changed from %q to %q", text, want, got)
		}
	}
}

func TestNormalizeSource(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: "en", want: "en"},
		{raw: "en-US", want: "en"},
		{raw: "EN_us", want: "en"},
		{raw: "ja", want: "ja"},
		{raw: "ja-JP", want: "ja"},
		{raw: "zh", want: "zh"},
		{raw: "zh-CN", want: "zh"},
		{raw: "zh-TW", want: "zh"},
		{raw: "zh-Hans", want: "zh"},
		{raw: "zh-Hant", want: "zh"},
		{raw: "", want: "und"},
		{raw: "fr", want: "und"},
		{raw: "english", want: "und"},
		{raw: "jp", want: "und"},
		{raw: "zh-", want: "und"},
	} {
		if got := NormalizeSource(test.raw); got != test.want {
			t.Fatalf("NormalizeSource(%q)=%q want %q", test.raw, got, test.want)
		}
	}
}

func TestResolveSourcePrefersSupportedSourceAndDetectsMissingSource(t *testing.T) {
	chinese := "这个推荐系统现在越来越稳定了"
	japanese := "今日は新しい推薦システムを試しています"
	english := "The recommendation system is improving steadily today."
	for _, test := range []struct {
		raw  string
		text string
		want string
	}{
		{raw: "en", text: chinese, want: "en"},
		{raw: "ja", text: english, want: "ja"},
		{raw: "zh-TW", text: english, want: "zh"},
		{raw: "", text: chinese, want: "zh"},
		{raw: "und", text: japanese, want: "ja"},
		{raw: "fr", text: english, want: "und"},
		{raw: "de", text: "Das ist ein deutscher Beispielsatz zum Testen.", want: "und"},
	} {
		if got := ResolveSource(test.raw, test.text); got != test.want {
			t.Fatalf("ResolveSource(%q,%q)=%q want %q", test.raw, test.text, got, test.want)
		}
	}
}

func TestIsCanonicalRequiresExactStoredValue(t *testing.T) {
	for _, language := range []string{"zh", "ja", "en", "und"} {
		if !IsCanonical(language) {
			t.Fatalf("canonical value %q was rejected", language)
		}
	}
	for _, language := range []string{"", "ZH-cn", "EN", "unknown", "english", "jp", "cn", " fr "} {
		if IsCanonical(language) {
			t.Fatalf("non-canonical value %q was accepted", language)
		}
	}
}
