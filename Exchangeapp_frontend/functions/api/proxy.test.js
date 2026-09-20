// @vitest-environment node

import { afterEach, describe, expect, it, vi } from 'vitest';
import { onRequest } from './[[path]].js';

const apiOrigin = 'https://api.example.test';

function createContext({
  path,
  method = 'GET',
  upstreamStatus = 200,
  upstreamHeaders = {},
  upstreamBody = 'upstream body',
} = {}) {
  const upstreamResponse = new Response(upstreamBody, {
    status: upstreamStatus,
    headers: upstreamHeaders,
  });

  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(upstreamResponse));

  return {
    context: {
      env: { API_ORIGIN: apiOrigin },
      params: { path },
      request: new Request(`https://exchange.example.test/api/${path.join('/')}`, {
        method,
        headers: {
          accept: 'application/json',
        },
      }),
    },
    upstreamResponse,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('Pages Function API proxy cache policy', () => {
  it('keeps ordinary API responses no-store', async () => {
    const { context } = createContext({
      path: ['feed', 'following'],
      upstreamHeaders: { 'Cache-Control': 'public, max-age=60' },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it('preserves the immutable cache header for successful post media', async () => {
    const cacheControl = 'public, max-age=31536000, immutable';
    const { context } = createContext({
      path: ['files', 'post-media', 'users', 'v1', '42', 'example', 'medium.jpg'],
      upstreamHeaders: {
        'Cache-Control': cacheControl,
        'Content-Type': 'image/jpeg',
      },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe(cacheControl);
    expect(response.headers.get('content-type')).toBe('image/jpeg');
  });

  it('preserves the immutable cache header for successful avatars', async () => {
    const cacheControl = 'public, max-age=31536000, immutable';
    const { context } = createContext({
      path: ['files', 'profile-avatars', 'example.jpg'],
      upstreamHeaders: { 'Cache-Control': cacheControl },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe(cacheControl);
  });

  it('preserves a backend-controlled shorter file TTL', async () => {
    const cacheControl = 'public, max-age=86400';
    const { context } = createContext({
      path: ['files', 'some-supported-file'],
      upstreamHeaders: { 'Cache-Control': cacheControl },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe(cacheControl);
  });

  it('forces file 404 responses to no-store', async () => {
    const { context } = createContext({
      path: ['files', 'post-media', 'missing.jpg'],
      upstreamStatus: 404,
      upstreamHeaders: {
        'Cache-Control': 'public, max-age=31536000, immutable',
      },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it('forces file 500 responses to no-store', async () => {
    const { context } = createContext({
      path: ['files', 'post-media', 'error.jpg'],
      upstreamStatus: 500,
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it('preserves the cache header for successful HEAD file requests', async () => {
    const cacheControl = 'public, max-age=31536000, immutable';
    const { context } = createContext({
      path: ['files', 'post-media', 'example.jpg'],
      method: 'HEAD',
      upstreamHeaders: { 'Cache-Control': cacheControl },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe(cacheControl);
  });

  it('does not match a non-file path that merely contains files', async () => {
    const { context } = createContext({
      path: ['users', 'files-settings'],
      upstreamHeaders: { 'Cache-Control': 'public, max-age=31536000, immutable' },
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe('no-store');
  });

  it('uses no-store when a successful file response omits Cache-Control', async () => {
    const { context } = createContext({
      path: ['files', 'post-media', 'missing-cache-header.jpg'],
    });

    const response = await onRequest(context);

    expect(response.headers.get('cache-control')).toBe('no-store');
  });
});
