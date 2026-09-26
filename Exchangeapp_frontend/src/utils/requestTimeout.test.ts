import axios, { AxiosError } from 'axios';
import { describe, expect, it } from 'vitest';
import {
  API_REQUEST_TIMEOUT_MS,
  AUTH_REQUEST_TIMEOUT_MS,
  UPLOAD_REQUEST_TIMEOUT_MS,
  isRequestTimeoutError,
} from './requestTimeout';

describe('request timeout policy', () => {
  it('uses bounded deadlines for API, auth, and media upload requests', () => {
    expect(API_REQUEST_TIMEOUT_MS).toBe(15_000);
    expect(AUTH_REQUEST_TIMEOUT_MS).toBe(15_000);
    expect(UPLOAD_REQUEST_TIMEOUT_MS).toBe(60_000);
  });

  it.each(['ECONNABORTED', 'ETIMEDOUT'])('recognizes Axios timeout code %s', code => {
    const error = new AxiosError('Request timed out', code);

    expect(isRequestTimeoutError(error)).toBe(true);
  });

  it('does not treat a normal Axios error as a timeout', () => {
    const error = Object.assign(new AxiosError('Bad request', 'ERR_BAD_REQUEST'), {
      response: { status: 400 },
    });

    expect(axios.isAxiosError(error)).toBe(true);
    expect(error.response.status).toBe(400);
    expect(isRequestTimeoutError(error)).toBe(false);
  });

  it('does not treat a normal Error as a timeout', () => {
    expect(isRequestTimeoutError(new Error('timeout'))).toBe(false);
  });
});
