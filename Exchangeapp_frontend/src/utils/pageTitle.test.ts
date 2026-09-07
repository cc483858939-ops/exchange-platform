// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from 'vitest';
import { formatPageTitle, setPageTitle } from './pageTitle';

describe('page title utility', () => {
  beforeEach(() => {
    document.title = 'Exchange';
  });

  it('formats a page title with the Exchange brand suffix', () => {
    expect(formatPageTitle('Home')).toBe('Home — Exchange');
    expect(formatPageTitle(' Notifications ')).toBe('Notifications — Exchange');
  });

  it('falls back to the base brand title for empty values', () => {
    expect(formatPageTitle('')).toBe('Exchange');
    expect(formatPageTitle('   ')).toBe('Exchange');
    expect(formatPageTitle(undefined)).toBe('Exchange');
    expect(formatPageTitle(null)).toBe('Exchange');
  });

  it('sets document.title without HTML interpolation', () => {
    setPageTitle('Search');
    expect(document.title).toBe('Search — Exchange');

    setPageTitle();
    expect(document.title).toBe('Exchange');

    setPageTitle('<b>unsafe</b>');
    expect(document.title).toBe('<b>unsafe</b> — Exchange');
  });
});
