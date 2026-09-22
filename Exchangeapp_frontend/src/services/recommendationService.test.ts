import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock('../axios', () => ({
  default: {
    get: mocks.get,
  },
}));

import { getPostRecommendations, getPublicPostRecommendations } from './recommendationService';

describe('recommendation service', () => {
  beforeEach(() => {
    mocks.get.mockReset();
  });

  it('requests and returns the recommendation page envelope', async () => {
    const page = {
      items: [],
      request_id: 'request-42',
      depleted: true,
    };
    mocks.get.mockResolvedValue({ data: page });

    await expect(getPostRecommendations(20)).resolves.toEqual(page);

    expect(mocks.get).toHaveBeenCalledWith('/recommendations/posts', {
      params: { limit: 20 },
    });
  });

  it.each([0, -1, 1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1])(
    'rejects invalid recommendation limit %s',
    async (limit) => {
      await expect(getPostRecommendations(limit)).rejects.toThrow('Invalid recommendation limit');
      expect(mocks.get).not.toHaveBeenCalled();
    },
  );

  it('requests guest recommendations with the tab session header', async () => {
    const page = { items: [], request_id: 'guest-request', depleted: true };
    mocks.get.mockResolvedValue({ data: page });

    await expect(getPublicPostRecommendations({
      limit: 20,
      guestSessionId: '4ca3706b-197e-4f63-8f51-f99176f8b61c',
    }))
      .resolves.toEqual(page);

    expect(mocks.get).toHaveBeenCalledWith('/public/recommendations/posts', {
      params: { limit: 20 },
      headers: {
        'X-Guest-Recommendation-Session': '4ca3706b-197e-4f63-8f51-f99176f8b61c',
      },
    });
  });

  it('does not send a guest session header when unavailable', async () => {
    const page = { items: [], request_id: 'guest-request', depleted: true };
    mocks.get.mockResolvedValue({ data: page });

    await expect(getPublicPostRecommendations({ limit: 20 })).resolves.toEqual(page);

    expect(mocks.get).toHaveBeenCalledWith('/public/recommendations/posts', {
      params: { limit: 20 },
    });
  });
});
