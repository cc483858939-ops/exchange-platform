import { describe, expect, it } from 'vitest';
import { formatPostDate } from './time';

describe('formatPostDate', () => {
  const now = new Date(2026, 7, 17, 12, 0, 0);

  it('renders the month and day for posts from the current year', () => {
    expect(formatPostDate(new Date(2026, 7, 16, 12, 0, 0), now)).toBe('Aug 16');
  });

  it('includes the year for posts from a previous year', () => {
    expect(formatPostDate(new Date(2025, 7, 16, 12, 0, 0), now)).toBe('Aug 16, 2025');
  });

  it('returns an empty string for missing or invalid dates', () => {
    expect(formatPostDate(null, now)).toBe('');
    expect(formatPostDate('not-a-date', now)).toBe('');
  });
});