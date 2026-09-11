export class AuthRequestError extends Error {
  readonly code: string | null;

  constructor(message: string, code: string | null = null) {
    super(message);
    this.name = 'AuthRequestError';
    this.code = code;
  }
}
