import axios from 'axios';

export const API_REQUEST_TIMEOUT_MS = 15_000;
export const AUTH_REQUEST_TIMEOUT_MS = 15_000;
export const UPLOAD_REQUEST_TIMEOUT_MS = 60_000;

export const isRequestTimeoutError = (error: unknown): boolean => (
  axios.isAxiosError(error)
  && (error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT')
);
