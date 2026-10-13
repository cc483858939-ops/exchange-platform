// @vitest-environment node

import { afterEach, describe, expect, it, vi } from 'vitest';
import { onRequest } from './[[path]].js';

const apiOrigin = 'https://api.example.test/api';

function createContext({
  path = [],
  method = 'GET',
  upstreamStatus = 200,
  upstreamHeaders = {},
  upstreamBody = 'upstream body',
  apiOrigin: configuredApiOrigin = apiOrigin,
  env = {},
  search = '',
  requestHeaders = { accept: 'application/json' },
  requestBody,
} = {}) {
  const upstreamResponse = new Response(upstreamBody, {
    status: upstreamStatus,
    headers: upstreamHeaders,
  });

  const fetchMock = vi.fn().mockResolvedValue(upstreamResponse);
  vi.stubGlobal('fetch', fetchMock);
  const pathValue = Array.isArray(path) ? path.join('/') : path;
  const requestInit = { method, headers: requestHeaders };
  if (requestBody !== undefined) {
    requestInit.body = requestBody;
  }

  return {
    fetchMock,
    context: {
      env: { API_ORIGIN: configuredApiOrigin, ...env },
      params: { path },
      request: new Request(`https://exchange.example.test/api/${pathValue}${search}`, requestInit),
    },
    upstreamResponse,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('Pages Function API proxy URL contract', () => {
  it.each([
    ['GET posts', 'GET', ['posts', '123'], '', 'https://api.example.test/api/posts/123'],
    ['GET search with query', 'GET', ['posts', 'search'], '?q=test', 'https://api.example.test/api/posts/search?q=test'],
    ['POST auth', 'POST', ['auth', 'login'], '', 'https://api.example.test/api/auth/login'],
    ['POST upload', 'POST', ['uploads', 'post-media'], '', 'https://api.example.test/api/uploads/post-media'],
    ['post media', 'GET', ['files', 'post-media', 'image.jpg'], '', 'https://api.example.test/api/files/post-media/image.jpg'],
    ['profile avatar', 'GET', ['files', 'profile-avatars', 'avatar.jpg'], '', 'https://api.example.test/api/files/profile-avatars/avatar.jpg'],
    ['profile cover', 'GET', ['files', 'profile-covers', 'cover.jpg'], '', 'https://api.example.test/api/files/profile-covers/cover.jpg'],
  ])('forwards %s to the matching /api upstream path', async (_name, method, path, search, expectedUrl) => {
    const { context, fetchMock } = createContext({ path, method, search });

    await onRequest(context);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe(expectedUrl);
  });

  it('accepts a trailing slash on API_ORIGIN without adding a duplicate prefix', async () => {
    const { context, fetchMock } = createContext({
      path: ['posts', '123'],
      apiOrigin: 'https://api.example.test/api/',
    });

    await onRequest(context);

    expect(fetchMock.mock.calls[0][0]).toBe('https://api.example.test/api/posts/123');
  });

  it('accepts slash-delimited Pages catch-all parameters', async () => {
    const { context, fetchMock } = createContext({ path: 'posts/123' });

    await onRequest(context);

    expect(fetchMock.mock.calls[0][0]).toBe('https://api.example.test/api/posts/123');
  });

  it('rejects a catch-all path that already contains the /api prefix', async () => {
    const { context, fetchMock } = createContext({ path: ['api', 'posts'] });

    const response = await onRequest(context);

    expect(response.status).toBe(400);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([
    ['missing API_ORIGIN', null, 'API proxy is not configured'],
    ['origin without /api', 'https://api.example.test', 'API_ORIGIN must be an absolute URL with exactly one /api path prefix'],
    ['origin with duplicate /api', 'https://api.example.test/api/api', 'API_ORIGIN must be an absolute URL with exactly one /api path prefix'],
  ])('returns a configuration error for %s', async (_name, configuredApiOrigin, expectedError) => {
    const { context, fetchMock } = createContext({ apiOrigin: configuredApiOrigin });

    const response = await onRequest(context);

    expect(response.status).toBe(500);
    expect(response.headers.get('cache-control')).toBe('no-store');
    await expect(response.json()).resolves.toEqual({ error: expectedError });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('forwards POST body, Content-Type, and Authorization and replaces browser Access credentials', async () => {
    const body = '{"email":"user@example.test"}';
    const { context, fetchMock } = createContext({
      path: ['auth', 'login'],
      method: 'POST',
      requestBody: body,
      requestHeaders: {
        'content-type': 'application/json',
        authorization: 'Bearer user-token',
        'cf-access-client-id': 'browser-id',
        'cf-access-client-secret': 'browser-secret',
      },
      env: {
        CF_ACCESS_CLIENT_ID: 'server-id',
        CF_ACCESS_CLIENT_SECRET: 'server-secret',
      },
    });

    await onRequest(context);

    const [url, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    expect(url).toBe('https://api.example.test/api/auth/login');
    expect(init.method).toBe('POST');
    expect(headers.get('content-type')).toBe('application/json');
    expect(headers.get('authorization')).toBe('Bearer user-token');
    expect(headers.get('cf-access-client-id')).toBe('server-id');
    expect(headers.get('cf-access-client-secret')).toBe('server-secret');
    await expect(new Response(init.body).text()).resolves.toBe(body);
  });

  it('does not forward forged browser Access credentials without server credentials', async () => {
    const { context, fetchMock } = createContext({
      path: ['posts'],
      requestHeaders: {
        'cf-access-client-id': 'browser-id',
        'cf-access-client-secret': 'browser-secret',
      },
    });

    await onRequest(context);

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.has('cf-access-client-id')).toBe(false);
    expect(headers.has('cf-access-client-secret')).toBe(false);
  });

  it('rejects an incomplete server Access credential pair', async () => {
    const { context, fetchMock } = createContext({
      path: ['posts'],
      env: { CF_ACCESS_CLIENT_ID: 'server-id' },
    });

    const response = await onRequest(context);

    expect(response.status).toBe(500);
    expect(response.headers.get('cache-control')).toBe('no-store');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('keeps the existing 502 response when the upstream is unreachable', async () => {
    const { context, fetchMock } = createContext({ path: ['posts'] });
    fetchMock.mockRejectedValueOnce(new Error('upstream unavailable'));

    const response = await onRequest(context);

    expect(response.status).toBe(502);
    expect(response.headers.get('cache-control')).toBe('no-store');
    await expect(response.json()).resolves.toEqual({ error: 'API upstream is temporarily unreachable' });
  });
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

  it('preserves the backend no-cache policy for successful post media', async () => {
    const cacheControl = 'private, no-cache, max-age=0, must-revalidate';
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
    const cacheControl = 'private, no-cache, max-age=0, must-revalidate';
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
