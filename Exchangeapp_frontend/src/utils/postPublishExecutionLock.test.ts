import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  PublishCoordinationUnavailableError,
  withPostPublishExecutionLock,
} from './postPublishExecutionLock';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('post publish execution lock', () => {
  it('uses one exclusive lock name per viewer and returns the callback result', async () => {
    const request = vi.fn(async (
      _name: string,
      _options: { mode: 'exclusive' },
      callback: () => unknown,
    ) => callback());
    vi.stubGlobal('navigator', { locks: { request } });

    await expect(withPostPublishExecutionLock(7, async () => 'done')).resolves.toBe('done');

    expect(request).toHaveBeenCalledWith(
      'exchange-post-publish:7',
      { mode: 'exclusive' },
      expect.any(Function),
    );
  });

  it('fails closed when Web Locks are unavailable or the request fails', async () => {
    vi.stubGlobal('navigator', {});
    const action = vi.fn();
    await expect(withPostPublishExecutionLock(7, action))
      .rejects.toBeInstanceOf(PublishCoordinationUnavailableError);
    expect(action).not.toHaveBeenCalled();

    const request = vi.fn().mockRejectedValue(new Error('lock request rejected'));
    vi.stubGlobal('navigator', { locks: { request } });
    await expect(withPostPublishExecutionLock(7, action))
      .rejects.toBeInstanceOf(PublishCoordinationUnavailableError);
    expect(action).not.toHaveBeenCalled();
  });

  it('does not classify a callback failure as a lock failure', async () => {
    const callbackError = new Error('publish failed');
    const request = vi.fn(async (
      _name: string,
      _options: { mode: 'exclusive' },
      callback: () => unknown,
    ) => callback());
    vi.stubGlobal('navigator', { locks: { request } });

    await expect(withPostPublishExecutionLock(7, () => Promise.reject(callbackError)))
      .rejects.toBe(callbackError);
  });
});
