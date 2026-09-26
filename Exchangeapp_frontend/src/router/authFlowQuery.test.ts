import { describe, expect, it } from 'vitest';
import { buildAuthFlowQuery } from './authFlowQuery';

describe('buildAuthFlowQuery', () => {
  it('preserves a string returnTo and the profile intent', () => {
    expect(buildAuthFlowQuery({
      returnTo: '/posts/42?reply=1#conversation',
      intent: 'profile',
    })).toEqual({
      returnTo: '/posts/42?reply=1#conversation',
      intent: 'profile',
    });
  });

  it('drops unrelated fields and unsupported query values', () => {
    expect(buildAuthFlowQuery({
      returnTo: ['/notifications', '/history'],
      intent: ['profile'],
      source: 'feed',
      campaign: 'spring',
    })).toEqual({});
  });

  it('keeps a valid field when the other field is unsupported', () => {
    expect(buildAuthFlowQuery({
      returnTo: '/notifications',
      intent: 'settings',
      source: 'feed',
    })).toEqual({ returnTo: '/notifications' });
  });
});
