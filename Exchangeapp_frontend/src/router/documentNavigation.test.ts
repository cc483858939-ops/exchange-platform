// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  initialDocumentNavigation,
  isInitialDocumentEntryForRoute,
  type InitialDocumentNavigation,
} from './documentNavigation';

const initialPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
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

  it('uses the navigation entry URL when the module initializes after an SPA URL change', async () => {
    vi.resetModules();
    vi.stubGlobal('performance', {
      getEntriesByType: vi.fn(() => [{
        type: 'navigate',
        name: 'https://example.test/search?q=abc',
      }]),
    } as unknown as Performance);
    window.history.replaceState({}, '', '/');

    const module = await import('./documentNavigation');

    expect(module.initialDocumentNavigation).toEqual({
      type: 'navigate',
      url: 'https://example.test/search?q=abc',
    });
    expect(module.isInitialDocumentEntryForRoute('/')).toBe(false);
    expect(module.isInitialDocumentEntryForRoute('/search?q=abc')).toBe(true);
  });

  it('keeps a reload entry tied to the navigation entry URL', async () => {
    vi.resetModules();
    vi.stubGlobal('performance', {
      getEntriesByType: vi.fn(() => [{
        type: 'reload',
        name: 'https://example.test/',
      }]),
    } as unknown as Performance);

    const module = await import('./documentNavigation');

    expect(module.isInitialDocumentEntryForRoute('/')).toBe(true);
  });

  it('does not classify back_forward navigation as a cold document entry', async () => {
    vi.resetModules();
    vi.stubGlobal('performance', {
      getEntriesByType: vi.fn(() => [{
        type: 'back_forward',
        name: 'https://example.test/',
      }]),
    } as unknown as Performance);

    const module = await import('./documentNavigation');

    expect(module.isInitialDocumentEntryForRoute('/')).toBe(false);
  });

  it('uses a conservative unknown snapshot when no navigation entry exists', async () => {
    vi.resetModules();
    vi.stubGlobal('performance', {
      getEntriesByType: vi.fn(() => []),
    } as unknown as Performance);

    const module = await import('./documentNavigation');

    expect(module.initialDocumentNavigation).toEqual({ type: 'unknown', url: '' });
    expect(module.isInitialDocumentEntryForRoute('/')).toBe(false);
  });

  it.each([
    ['https://example.test/search', '/search#results'],
    ['https://example.test/search?q=test', '/search?q=test#results'],
  ])('matches path and query when a navigation entry omits the hash: %s -> %s', (url, routeFullPath) => {
    expect(isInitialDocumentEntryForRoute(routeFullPath, {
      type: 'navigate',
      url,
    })).toBe(true);
  });

  it('keeps query strings significant while ignoring only the hash', () => {
    expect(isInitialDocumentEntryForRoute('/search?q=other#results', {
      type: 'navigate',
      url: 'https://example.test/search?q=test',
    })).toBe(false);
  });

  it('snapshots the initial document URL once instead of following SPA URL changes', () => {
    const snapshot = { ...initialDocumentNavigation };
    window.history.replaceState({}, '', '/search?query=reload-test#results');

    expect(initialDocumentNavigation).toEqual(snapshot);
  });
});
