import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getGuestRecommendationSessionID,
  GUEST_RECOMMENDATION_SESSION_STORAGE_KEY,
} from './guestRecommendationSession';

const generatedSessionID = '4ca3706b-197e-4f63-8f51-f99176f8b61c';

const createSessionStorage = (): Storage => {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
    clear: () => values.clear(),
    key: (index: number) => Array.from(values.keys())[index] ?? null,
    get length() {
      return values.size;
    },
  } as Storage;
};

let testSessionStorage: Storage;
let hadOriginalWindow = false;
let originalWindow: unknown;

describe('guest recommendation session', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    if (hadOriginalWindow) {
      Object.defineProperty(globalThis, 'window', {
        configurable: true,
        value: originalWindow,
      });
    } else {
      Reflect.deleteProperty(globalThis, 'window');
    }
  });

  beforeEach(() => {
    hadOriginalWindow = Object.prototype.hasOwnProperty.call(globalThis, 'window');
    originalWindow = (globalThis as { window?: unknown }).window;
    testSessionStorage = createSessionStorage();
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        sessionStorage: testSessionStorage,
        crypto: { randomUUID: vi.fn(() => generatedSessionID) },
      },
    });
  });

  it('creates and reuses one UUID in sessionStorage', () => {
    const first = getGuestRecommendationSessionID();
    const second = getGuestRecommendationSessionID();

    expect(first).toBe(generatedSessionID);
    expect(second).toBe(generatedSessionID);
    expect(testSessionStorage.getItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY))
      .toBe(generatedSessionID);
  });

  it('replaces malformed stored values', () => {
    testSessionStorage.setItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY, 'not-a-uuid');

    expect(getGuestRecommendationSessionID()).toBe(generatedSessionID);
    expect(testSessionStorage.getItem(GUEST_RECOMMENDATION_SESSION_STORAGE_KEY))
      .toBe(generatedSessionID);
  });

  it('fails open when session storage is unavailable', () => {
    vi.spyOn(testSessionStorage, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });

    expect(getGuestRecommendationSessionID()).toBeNull();
  });

  it('fails open when crypto is unavailable', () => {
    Object.defineProperty(globalThis.window, 'crypto', {
      configurable: true,
      value: undefined,
    });

    expect(getGuestRecommendationSessionID()).toBeNull();
  });

  it('returns null during SSR', () => {
    const originalWindow = globalThis.window;
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: undefined,
    });
    try {
      expect(getGuestRecommendationSessionID()).toBeNull();
    } finally {
      Object.defineProperty(globalThis, 'window', {
        configurable: true,
        value: originalWindow,
      });
    }
  });
});
