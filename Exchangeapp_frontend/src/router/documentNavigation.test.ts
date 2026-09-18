// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest';
import {
  initialDocumentNavigation,
  isInitialDocumentReloadForRoute,
  type InitialDocumentNavigation,
} from './documentNavigation';

const initialPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;

afterEach(() => {
  window.history.replaceState({}, '', initialPath || '/');
});

describe('document navigation', () => {
  it.each<Array<[InitialDocumentNavigation['type'], boolean]>>([
    ['navigate', false],
    ['reload', true],
    ['back_forward', false],
    ['unknown', false],
  ])('classifies %s navigation correctly for the initial route', (type, expected) => {
    expect(isInitialDocumentReloadForRoute('/', { type, url: '/' })).toBe(expected);
  });

  it('requires the reloaded document URL to match the mounted route', () => {
    expect(isInitialDocumentReloadForRoute('/', {
      type: 'reload',
      url: '/search',
    })).toBe(false);
  });

  it('snapshots the initial document URL once instead of following SPA URL changes', () => {
    const snapshot = { ...initialDocumentNavigation };
    window.history.replaceState({}, '', '/search?query=reload-test#results');

    expect(initialDocumentNavigation).toEqual(snapshot);
  });
});
