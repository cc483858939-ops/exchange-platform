// @vitest-environment jsdom

import { describe, expect, it } from 'vitest';
import { decodeAuthIdentity, decodeAuthTokenMetadata, normalizeAuthIdentity } from './authIdentity';

const tokenWithSubject = (sub: unknown) => {
  const payload = btoa(JSON.stringify({ sub }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `header.${payload}.signature`;
};

const tokenWithMetadata = (sub: unknown, sid: unknown) => {
  const payload = btoa(JSON.stringify({ sub, sid }))
    .replace(/=/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '_');
  return `header.${payload}.signature`;
};

describe('normalizeAuthIdentity', () => {
  it('normalizes a legacy identity to the canonical shape', () => {
    expect(normalizeAuthIdentity({ id: 7, username: 'alice' })).toEqual({
      id: 7,
      username: 'alice',
      display_name: '',
      avatar_url: '',
    });
  });

  it('preserves a complete identity', () => {
    expect(normalizeAuthIdentity({
      id: 7,
      username: 'alice',
      display_name: 'Alice',
      avatar_url: '/api/files/profile-avatars/7/test.webp',
    })).toEqual({
      id: 7,
      username: 'alice',
      display_name: 'Alice',
      avatar_url: '/api/files/profile-avatars/7/test.webp',
    });
  });

  it.each([
    { username: 'alice' },
    { id: 0, username: 'alice' },
    { id: -1, username: 'alice' },
    { id: 1.5, username: 'alice' },
    { id: 7 },
    { id: 7, username: 42 },
  ])('rejects an invalid identity: %o', (candidate) => {
    expect(normalizeAuthIdentity(candidate)).toBeNull();
  });
});

describe('decodeAuthIdentity', () => {
  it('builds a canonical identity from only the JWT subject', () => {
    expect(decodeAuthIdentity(tokenWithSubject('7'))).toEqual({
      id: 7,
      username: '',
      display_name: '',
      avatar_url: '',
    });
  });
});

describe('decodeAuthTokenMetadata', () => {
  it('decodes a positive subject and stable session id', () => {
    expect(decodeAuthTokenMetadata(tokenWithMetadata('7', 'sid-1'))).toEqual({
      userId: 7,
      sessionId: 'sid-1',
    });
  });

  it.each([
    ['missing sid', tokenWithMetadata('7', undefined)],
    ['blank sid', tokenWithMetadata('7', '  ')],
    ['invalid subject', tokenWithMetadata('0', 'sid-1')],
    ['unsafe subject', tokenWithMetadata('9007199254740992', 'sid-1')],
    ['malformed token', 'not-a-jwt'],
  ])('rejects %s', (_name, token) => {
    expect(decodeAuthTokenMetadata(token)).toBeNull();
  });

  it('keeps the legacy identity decoder compatible with tokens without sid', () => {
    expect(decodeAuthIdentity(tokenWithSubject('7'))).toMatchObject({ id: 7 });
  });
});
