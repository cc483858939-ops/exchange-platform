import { afterEach, describe, expect, it, vi } from 'vitest';
import { createClientOperationID } from './clientOperationId';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('createClientOperationID', () => {
  it('uses crypto.randomUUID when available', () => {
    const randomUUID = vi.fn(() => '00000000-0000-4000-8000-000000000042');
    vi.stubGlobal('crypto', { randomUUID });

    expect(createClientOperationID()).toBe('00000000-0000-4000-8000-000000000042');
    expect(randomUUID).toHaveBeenCalledTimes(1);
  });

  it('formats cryptographic random bytes as UUID v4 when randomUUID is unavailable', () => {
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.fill(0x01);
      return bytes;
    });
    vi.stubGlobal('crypto', { getRandomValues });

    expect(createClientOperationID()).toBe('01010101-0101-4101-8101-010101010101');
    expect(getRandomValues).toHaveBeenCalledTimes(1);
  });

  it('keeps the UUID v4 fallback valid when Web Crypto is unavailable', () => {
    vi.stubGlobal('crypto', undefined);
    vi.spyOn(Math, 'random').mockReturnValue(0.5);

    expect(createClientOperationID()).toBe('80808080-8080-4080-8080-808080808080');
  });
});
