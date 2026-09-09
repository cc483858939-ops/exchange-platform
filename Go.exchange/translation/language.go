package translation

import "strings"

const (
	LanguageChinese      = "zh"
	LanguageJapanese     = "ja"
	LanguageEnglish      = "en"
	LanguageUndetermined = "und"
)

func NormalizeTargetLanguage(raw string) (string, bool) {
	switch raw {
	case LanguageChinese:
		return LanguageChinese, true
	case LanguageJapanese:
		return LanguageJapanese, true
	case LanguageEnglish:
		return LanguageEnglish, true
	default:
		return "", false
	}
}

func NormalizeSourceLanguage(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case LanguageChinese:
		return LanguageChinese
	case LanguageJapanese:
		return LanguageJapanese
	case LanguageEnglish:
		return LanguageEnglish
	case LanguageUndetermined:
		return LanguageUndetermined
	default:
		return LanguageUndetermined
	}
}

func TargetLanguageName(language string) string {
	switch language {
	case LanguageChinese:
		return "Simplified Chinese"
	case LanguageJapanese:
		return "Japanese"
	case LanguageEnglish:
		return "English"
	default:
		return ""
	}
}

func SourceLanguageName(language string) string {
	switch NormalizeSourceLanguage(language) {
	case LanguageChinese:
		return "Chinese"
	case LanguageJapanese:
		return "Japanese"
	case LanguageEnglish:
		return "English"
	default:
		return "Unknown; infer from the post"
	}
}
