// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AxiosError, InternalAxiosRequestConfig } from 'axios';

const mocks = vi.hoisted(() => {
  const requestHandlers: unknown[] = [];
  const responseHandlers: unknown[] = [];
  const responseErrorHandlers: unknown[] = [];
  const authStore = {
    token: null as string | null,
    refreshToken: null as string | null,
    sessionVersion: 0,
    refreshAccessToken: vi.fn(async () => 'Bearer refreshed'),
    clearAuth: vi.fn(),
  };

  const instance = Object.assign(
    vi.fn(async (config: unknown) => {
      const requestHandler = requestHandlers[0] as ((config: unknown) => unknown) | undefined;
      const preparedConfig = requestHandler ? await requestHandler(config) : config;
      return { config: preparedConfig, status: 200 };
    }),
    {
      interceptors: {
        request: {
          use: vi.fn((fulfilled: unknown) => {
            requestHandlers.push(fulfilled);
            return requestHandlers.length - 1;
          }),
        },
        response: {
          use: vi.fn((fulfilled: unknown, rejected: unknown) => {
            responseHandlers.push(fulfilled);
            responseErrorHandlers.push(rejected);
            return responseHandlers.length - 1;
          }),
        },
      },
    },
  );

  return {
    authStore,
    instance,
    requestHandlers,
    responseErrorHandlers,
  };
});

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => mocks.instance),
  },
}));

vi.mock('./store/auth', async importOriginal => {
  const actual = await importOriginal<typeof import('./store/auth')>();
  return {
    ...actual,
    useAuthStore: () => mocks.authStore,
  };
});

type TestRequestConfig = InternalAxiosRequestConfig & {
  _retry?: boolean;
  _authSessionVersion?: number;
};

type RequestInterceptor = (config: TestRequestConfig) => TestRequestConfig | Promise<TestRequestConfig>;
type ResponseErrorInterceptor = (error: AxiosError) => Promise<unknown>;

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;

  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });

  return { promise, resolve, reject };
};

const makeRequest = (sessionVersion?: number, url = '/posts'): TestRequestConfig => ({
  url,
  headers: { Authorization: 'Bearer original' },
  ...(sessionVersion === undefined ? {} : { _authSessionVersion: sessionVersion }),
} as TestRequestConfig);

const makeUnauthorizedError = (config: TestRequestConfig): AxiosError => ({
  config,
  response: { status: 401 },
} as AxiosError);

const requestInterceptor = () => mocks.requestHandlers[0] as RequestInterceptor;
const responseErrorInterceptor = () => mocks.responseErrorHandlers[0] as ResponseErrorInterceptor;

describe('Axios authentication session handling', () => {
  beforeEach(async () => {
    vi.resetModules();
    vi.clearAllMocks();
    mocks.requestHandlers.length = 0;
    mocks.responseErrorHandlers.length = 0;
    mocks.instance.mockReset().mockImplementation(async config => {
      const requestHandler = mocks.requestHandlers[0] as ((config: unknown) => unknown) | undefined;
      const preparedConfig = requestHandler ? await requestHandler(config) : config;
      return { config: preparedConfig, status: 200 };
    });
    mocks.authStore.token = null;
    mocks.authStore.refreshToken = null;
    mocks.authStore.sessionVersion = 0;
    mocks.authStore.refreshAccessToken.mockReset().mockResolvedValue('Bearer refreshed');
    mocks.authStore.clearAuth.mockReset();

    await import('./axios');
  });

  it('captures the current generation on authenticated and guest requests', async () => {
    mocks.authStore.sessionVersion = 7;
    mocks.authStore.token = 'Bearer A';
    const authenticatedRequest = makeRequest();
    await requestInterceptor()(authenticatedRequest);

    expect(authenticatedRequest._authSessionVersion).toBe(7);
    expect(authenticatedRequest.headers.Authorization).toBe('Bearer A');

    mocks.authStore.token = null;
    const guestRequest = makeRequest();
    await requestInterceptor()(guestRequest);

    expect(guestRequest._authSessionVersion).toBe(7);
  });

  it('rejects a stale 401 without refreshing, clearing auth, retrying, or rewriting headers', async () => {
    mocks.authStore.sessionVersion = 5;
    mocks.authStore.refreshToken = 'B refresh';
    mocks.authStore.token = 'Bearer B';
    const request = makeRequest(3);
    const error = makeUnauthorizedError(request);

    await expect(responseErrorInterceptor()(error)).rejects.toBe(error);

    expect(mocks.authStore.refreshAccessToken).not.toHaveBeenCalled();
    expect(mocks.authStore.clearAuth).not.toHaveBeenCalled();
    expect(mocks.instance).not.toHaveBeenCalled();
    expect(request.headers.Authorization).toBe('Bearer original');
  });

  it('shares one refresh among concurrent 401s in the same generation', async () => {
    const refresh = deferred<string>();
    mocks.authStore.sessionVersion = 8;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.token = 'Bearer A';
    mocks.authStore.refreshAccessToken.mockReturnValue(refresh.promise);

    const firstRequest = makeRequest(8);
    const secondRequest = makeRequest(8);
    const firstRetry = responseErrorInterceptor()(makeUnauthorizedError(firstRequest));
    const secondRetry = responseErrorInterceptor()(makeUnauthorizedError(secondRequest));

    expect(mocks.authStore.refreshAccessToken).toHaveBeenCalledTimes(1);
    mocks.authStore.token = 'Bearer A refreshed';
    refresh.resolve('Bearer A refreshed');
    await Promise.all([firstRetry, secondRetry]);

    expect(mocks.instance).toHaveBeenCalledTimes(2);
    expect(firstRequest._retry).toBe(true);
    expect(secondRequest._retry).toBe(true);
    expect(firstRequest._authSessionVersion).toBe(8);
    expect(secondRequest._authSessionVersion).toBe(8);
    expect(firstRequest.headers.Authorization).toBe('Bearer A refreshed');
    expect(secondRequest.headers.Authorization).toBe('Bearer A refreshed');
  });

  it('does not reuse a pending refresh from an older generation', async () => {
    const accountARefresh = deferred<string>();
    const accountBRefresh = deferred<string>();
    mocks.authStore.sessionVersion = 2;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.token = 'Bearer A';
    mocks.authStore.refreshAccessToken
      .mockReturnValueOnce(accountARefresh.promise)
      .mockReturnValueOnce(accountBRefresh.promise);

    const accountARequest = makeRequest(2);
    const accountAResult = responseErrorInterceptor()(makeUnauthorizedError(accountARequest));

    mocks.authStore.sessionVersion = 4;
    mocks.authStore.refreshToken = 'B refresh';
    mocks.authStore.token = 'Bearer B';
    const accountBRequest = makeRequest(4);
    const accountBResult = responseErrorInterceptor()(makeUnauthorizedError(accountBRequest));

    expect(mocks.authStore.refreshAccessToken).toHaveBeenCalledTimes(2);
    mocks.authStore.token = 'Bearer B refreshed';
    accountBRefresh.resolve('Bearer B refreshed');
    await accountBResult;
    accountARefresh.resolve('Bearer stale A');

    await expect(accountAResult).rejects.toMatchObject({ name: 'AuthSessionChangedError' });
    expect(mocks.instance).toHaveBeenCalledTimes(1);
    expect(accountBRequest.headers.Authorization).toBe('Bearer B refreshed');
  });

  it('does not let an old refresh completion clear a newer generation refresh state', async () => {
    const accountARefresh = deferred<string>();
    const accountBRefresh = deferred<string>();
    mocks.authStore.sessionVersion = 2;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.token = 'Bearer A';
    mocks.authStore.refreshAccessToken
      .mockReturnValueOnce(accountARefresh.promise)
      .mockReturnValueOnce(accountBRefresh.promise);

    const accountAResult = responseErrorInterceptor()(makeUnauthorizedError(makeRequest(2)));
    mocks.authStore.sessionVersion = 4;
    mocks.authStore.refreshToken = 'B refresh';
    mocks.authStore.token = 'Bearer B';
    const firstBResult = responseErrorInterceptor()(makeUnauthorizedError(makeRequest(4)));

    accountARefresh.resolve('Bearer stale A');
    await expect(accountAResult).rejects.toMatchObject({ name: 'AuthSessionChangedError' });

    const secondBRequest = makeRequest(4);
    const secondBResult = responseErrorInterceptor()(makeUnauthorizedError(secondBRequest));
    expect(mocks.authStore.refreshAccessToken).toHaveBeenCalledTimes(2);

    mocks.authStore.token = 'Bearer B refreshed';
    accountBRefresh.resolve('Bearer B refreshed');
    await Promise.all([firstBResult, secondBResult]);
    expect(mocks.instance).toHaveBeenCalledTimes(2);
    expect(secondBRequest.headers.Authorization).toBe('Bearer B refreshed');
  });

  it('does not retry or attach a stale token when the session changes during refresh', async () => {
    const refresh = deferred<string>();
    mocks.authStore.sessionVersion = 9;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.token = 'Bearer A';
    mocks.authStore.refreshAccessToken.mockReturnValue(refresh.promise);
    const request = makeRequest(9);
    const pendingRetry = responseErrorInterceptor()(makeUnauthorizedError(request));

    mocks.authStore.sessionVersion = 10;
    mocks.authStore.refreshToken = 'B refresh';
    mocks.authStore.token = 'Bearer B';
    refresh.resolve('Bearer stale A');

    await expect(pendingRetry).rejects.toMatchObject({ name: 'AuthSessionChangedError' });
    expect(mocks.instance).not.toHaveBeenCalled();
    expect(request.headers.Authorization).toBe('Bearer original');
  });

  it('does not relabel a retry or replace its header if the session changes before dispatch', async () => {
    const refresh = deferred<string>();
    mocks.authStore.sessionVersion = 9;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.token = 'Bearer A';
    mocks.authStore.refreshAccessToken.mockReturnValue(refresh.promise);
    mocks.instance.mockImplementationOnce(async config => {
      mocks.authStore.sessionVersion = 10;
      mocks.authStore.token = 'Bearer B';
      const handler = mocks.requestHandlers[0] as RequestInterceptor;
      const preparedConfig = await handler(config as TestRequestConfig);
      return { config: preparedConfig, status: 200 };
    });
    const request = makeRequest(9);
    const pendingRetry = responseErrorInterceptor()(makeUnauthorizedError(request));

    refresh.resolve('Bearer A refreshed');

    await expect(pendingRetry).rejects.toMatchObject({ name: 'AuthSessionChangedError' });
    expect(request._authSessionVersion).toBe(9);
    expect(request.headers.Authorization).toBe('Bearer A refreshed');
    expect(request.headers.Authorization).not.toBe('Bearer B');
  });

  it('propagates refresh failures without clearing auth in the interceptor', async () => {
    const refreshError = new Error('refresh failed');
    mocks.authStore.sessionVersion = 6;
    mocks.authStore.refreshToken = 'A refresh';
    mocks.authStore.refreshAccessToken.mockRejectedValue(refreshError);
    const error = makeUnauthorizedError(makeRequest(6));

    await expect(responseErrorInterceptor()(error)).rejects.toBe(refreshError);

    expect(mocks.authStore.clearAuth).not.toHaveBeenCalled();
    expect(mocks.instance).not.toHaveBeenCalled();
  });

  it('keeps auth endpoint 401s out of refresh handling', async () => {
    mocks.authStore.sessionVersion = 3;
    mocks.authStore.refreshToken = 'A refresh';
    const error = makeUnauthorizedError(makeRequest(3, '/auth/login'));

    await expect(responseErrorInterceptor()(error)).rejects.toBe(error);

    expect(mocks.authStore.refreshAccessToken).not.toHaveBeenCalled();
    expect(mocks.instance).not.toHaveBeenCalled();
  });

  it('does not run refresh handling more than once for a retried request', async () => {
    mocks.authStore.sessionVersion = 3;
    mocks.authStore.refreshToken = 'A refresh';
    const request = makeRequest(3);
    request._retry = true;
    const error = makeUnauthorizedError(request);

    await expect(responseErrorInterceptor()(error)).rejects.toBe(error);

    expect(mocks.authStore.refreshAccessToken).not.toHaveBeenCalled();
    expect(mocks.instance).not.toHaveBeenCalled();
  });

  it('rejects a current-session 401 without refreshing when there is no refresh token', async () => {
    mocks.authStore.sessionVersion = 3;
    const error = makeUnauthorizedError(makeRequest(3));

    await expect(responseErrorInterceptor()(error)).rejects.toBe(error);

    expect(mocks.authStore.refreshAccessToken).not.toHaveBeenCalled();
    expect(mocks.authStore.clearAuth).not.toHaveBeenCalled();
  });
});
