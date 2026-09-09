export type TranslationLanguage = 'zh' | 'ja' | 'en';

export type TranslationSourceLanguage = TranslationLanguage | 'und';

export function normalizeTranslationLanguage(
  language: string,
): TranslationLanguage | null {
  const normalized = language.trim().toLowerCase();
  if (normalized === 'zh' || normalized.startsWith('zh-')) {
    return 'zh';
  }
  if (normalized === 'ja' || normalized.startsWith('ja-')) {
    return 'ja';
  }
  if (normalized === 'en' || normalized.startsWith('en-')) {
    return 'en';
  }
  return null;
}

export function getPreferredTranslationLanguage(): TranslationLanguage {
  if (typeof navigator === 'undefined') {
    return 'en';
  }

  const languages = Array.isArray(navigator.languages)
    ? navigator.languages
    : [];
  for (const language of languages) {
    const normalized = normalizeTranslationLanguage(language);
    if (normalized) {
      return normalized;
    }
  }

  const fallback = normalizeTranslationLanguage(navigator.language || '');
  return fallback || 'en';
}

export function translationLanguageName(language: TranslationSourceLanguage): string {
  switch (language) {
    case 'zh':
      return 'Chinese';
    case 'ja':
      return 'Japanese';
    case 'en':
      return 'English';
    default:
      return 'detected language';
  }
}

