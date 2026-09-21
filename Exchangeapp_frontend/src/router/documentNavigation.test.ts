// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest';
import {
  initialDocumentNavigation,
  isInitialDocumentEntryForRoute,
  type InitialDocumentNavigation,
} from './documentNavigation';

const initialPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;

afterEach(() => {
  window.history.replaceState({}, '', initialPath || '/');
});

describe('document navigation', () => {
  const navigationCases: Array<[InitialDocumentNavigation['type'], boolean]> = [
    ['navigate', true],
    ['reload', true],
    ['back_forward', false],
    ['unknown', false],
  ];

  it.each(navigationCases)(
    'classifies %s navigation correctly for the initial route',
    (type, expected) => {
      expect(isInitialDocumentEntryForRoute('/', { type, url: '/' })).toBe(expected);
    },
  );

  it('requires the initial document URL to match the mounted route', () => {
    expect(isInitialDocumentEntryForRoute('/', {
      type: 'navigate',
      url: '/search',
    })).toBe(false);
  });

  it('snapshots the initial document URL once instead of following SPA URL changes', () => {
    const snapshot = { ...initialDocumentNavigation };
    window.history.replaceState({}, '', '/search?query=reload-test#results');

    expect(initialDocumentNavigation).toEqual(snapshot);
  });
});
