import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    get: mocks.get,
    put: mocks.put,
    delete: mocks.delete,
  },
}));

import { getMyMutedUsers, muteUser, unmuteUser } from './userMuteService';

describe('user mute service', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.get.mockResolvedValue({ data: { items: [], next_cursor: null } });
    mocks.put.mockResolvedValue({ data: undefined });
    mocks.delete.mockResolvedValue({ data: undefined });
  });

  it('uses the authenticated mute endpoints and forwards pagination', async () => {
    await muteUser('42');
    await unmuteUser(42);
    await getMyMutedUsers({ limit: 20, cursor: 'page-2' });

    expect(mocks.put).toHaveBeenCalledExactlyOnceWith('/users/42/mute');
    expect(mocks.delete).toHaveBeenCalledExactlyOnceWith('/users/42/mute');
    expect(mocks.get).toHaveBeenCalledExactlyOnceWith('/me/mutes', {
      params: { limit: 20, cursor: 'page-2' },
    });
  });
});
