import { describe, expect, it } from 'vitest';
import { formatPostDate, formatPostDetailTimestamp } from './time';

describe('formatPostDate', () => {
  const now = new Date(2026, 7, 19, 17, 0, 0);
  const secondsAgo = (seconds: number) => new Date(now.getTime() - seconds * 1000);

  it.each([
    [0, 'now'],
    [30, 'now'],
    [59, 'now'],
  ])('renders %s seconds as now', (seconds, expected) => {
    expect(formatPostDate(secondsAgo(seconds), now)).toBe(expected);
  });

  it.each([
    [60, '1m'],
    [5 * 60, '5m'],
    [59 * 60, '59m'],
    [59 * 60 + 59, '59m'],
  ])('renders %s elapsed seconds as complete minutes', (seconds, expected) => {
    expect(formatPostDate(secondsAgo(seconds), now)).toBe(expected);
  });

  it.each([
    [60 * 60, '1h'],
    [60 * 60 + 60, '1h'],
    [60 * 60 + 59 * 60, '1h'],
    [2 * 60 * 60, '2h'],
    [23 * 60 * 60 + 59 * 60, '23h'],
  ])('renders %s elapsed seconds as complete hours', (seconds, expected) => {
    expect(formatPostDate(secondsAgo(seconds), now)).toBe(expected);
  });

  it('switches from hours to the current-year date at 24 hours', () => {
    expect(formatPostDate(secondsAgo(24 * 60 * 60 - 1), now)).toBe('23h');
    expect(formatPostDate(secondsAgo(24 * 60 * 60), now)).toBe('Aug 18');
  });

  it('includes the year for a date from a previous calendar year', () => {
    expect(formatPostDate(new Date(2025, 7, 16, 12, 0, 0), now)).toBe('Aug 16, 2025');
  });

  it('prioritizes the 24-hour threshold over the calendar year at New Year', () => {
    const newYear = new Date(2026, 0, 1, 12, 0, 0);
    expect(formatPostDate(new Date(2025, 11, 31, 12, 1, 0), newYear)).toBe('23h');
    expect(formatPostDate(new Date(2025, 11, 31, 12, 0, 0), newYear)).toBe('Dec 31, 2025');
  });

  it('clamps future timestamps to now', () => {
    expect(formatPostDate(new Date(now.getTime() + 5 * 60 * 1000), now)).toBe('now');
  });

  it('returns an empty string for missing or invalid dates', () => {
    expect(formatPostDate(null, now)).toBe('');
    expect(formatPostDate(undefined, now)).toBe('');
    expect(formatPostDate('not-a-date', now)).toBe('');
  });
});

describe('formatPostDetailTimestamp', () => {
  it('renders an absolute clock time and calendar date', () => {
    expect(formatPostDetailTimestamp(new Date(2026, 7, 27, 13, 42, 30)))
      .toBe('1:42 PM · Aug 27, 2026');
  });

  it.each([null, undefined, 'not-a-date'])('returns an empty string for %s', value => {
    expect(formatPostDetailTimestamp(value)).toBe('');
  });
});
