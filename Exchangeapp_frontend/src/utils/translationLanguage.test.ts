// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getPreferredTranslationLanguage,
  isPostTranslationAvailable,
  normalizeTranslationLanguage,
} from './translationLanguage';

describe('translation language helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    ['zh-CN', 'zh'],
    ['zh-TW', 'zh'],
    ['zh-Hant', 'zh'],
    ['ja-JP', 'ja'],
    ['en-US', 'en'],
  ] as const)('normalizes %s to %s', (input, expected) => {
    expect(normalizeTranslationLanguage(input)).toBe(expected);
  });

  it('uses the first supported browser language', () => {
    vi.stubGlobal('navigator', {
      languages: ['fr-FR', 'ja-JP', 'en-US'],
      language: 'fr-FR',
    });

    expect(getPreferredTranslationLanguage()).toBe('ja');
  });

  it('falls back to navigator.language when languages is unsupported', () => {
    vi.stubGlobal('navigator', {
      languages: ['fr-FR'],
      language: 'en-GB',
    });

    expect(getPreferredTranslationLanguage()).toBe('en');
  });

  it('falls back to English when every browser language is unsupported', () => {
    vi.stubGlobal('navigator', {
      languages: ['fr-FR', 'de-DE'],
      language: 'ko-KR',
    });

    expect(getPreferredTranslationLanguage()).toBe('en');
  });

  it('handles an empty browser language list', () => {
    vi.stubGlobal('navigator', { languages: [], language: '' });

    expect(getPreferredTranslationLanguage()).toBe('en');
  });

  it.each([
    ['zh', 'zh', false],
    ['zh', 'ja', true],
    ['zh', 'en', true],
    ['ja', 'zh', true],
    ['ja', 'ja', false],
    ['ja', 'en', true],
    ['en', 'zh', true],
    ['en', 'ja', true],
    ['en', 'en', false],
    ['und', 'zh', true],
    ['und', 'ja', true],
    ['und', 'en', true],
  ] as const)('uses the target language for %s to %s eligibility', (source, target, expected) => {
    expect(isPostTranslationAvailable(source, target)).toBe(expected);
  });
});
