import { describe, expect, it } from 'vitest';
import {
  canAbandonDurableSubmission,
  canMarkDurableSubmissionFailed,
  canRetryDurableSubmission,
  classifySubmissionFailure,
  hasValidDurableSubmissionFailureShape,
  isDurableSubmissionFailureKind,
} from './durableSubmissionContract';
import { matchesDurableAuthOwner } from '../auth/authRequestBinding';

describe('durable submission contract', () => {
  it.each([
    ['uploading', null, true, false, false],
    ['publishing', null, true, false, false],
    ['succeeded', null, true, false, false],
    ['failed', 'retryable', true, true, true],
    ['failed', 'validation_error', true, false, true],
    ['failed', 'idempotency_conflict', true, false, true],
    ['failed', 'auth_context_changed', true, false, true],
    ['failed', null, false, false, false],
    ['uploading', 'retryable', false, false, false],
    ['publishing', 'retryable', false, false, false],
    ['succeeded', 'retryable', false, false, false],
    ['succeeded', 'auth_context_changed', false, false, false],
  ] as const)(
    'applies the lifecycle contract to %s/%s',
    (phase, failureKind, valid, retryable, abandonable) => {
      expect(hasValidDurableSubmissionFailureShape(phase, failureKind)).toBe(valid);
      expect(canRetryDurableSubmission(phase, failureKind)).toBe(retryable);
      expect(canAbandonDurableSubmission(phase, failureKind)).toBe(abandonable);
    },
  );

  it('validates only the canonical failure kinds', () => {
    expect(isDurableSubmissionFailureKind(null)).toBe(true);
    expect(isDurableSubmissionFailureKind('retryable')).toBe(true);
    expect(isDurableSubmissionFailureKind('validation_error')).toBe(true);
    expect(isDurableSubmissionFailureKind('idempotency_conflict')).toBe(true);
    expect(isDurableSubmissionFailureKind('auth_context_changed')).toBe(true);
    expect(isDurableSubmissionFailureKind('unknown')).toBe(false);
    expect(isDurableSubmissionFailureKind(undefined)).toBe(false);
  });

  it.each([
    ['uploading', true],
    ['publishing', true],
    ['failed', true],
    ['succeeded', false],
  ])('marks phase %s as eligible for a failure transition: %s', (phase, expected) => {
    expect(canMarkDurableSubmissionFailed(phase)).toBe(expected);
  });

  it('matches durable auth ownership by user and session without comparing sessionVersion', () => {
    const binding = { userID: 7, sessionID: 'session-a', sessionVersion: 99 } as const;
    expect(matchesDurableAuthOwner(binding, 7, 'session-a')).toBe(true);
    expect(matchesDurableAuthOwner({ ...binding, sessionVersion: 1 }, 7, 'session-a')).toBe(true);
    expect(matchesDurableAuthOwner(binding, 8, 'session-a')).toBe(false);
    expect(matchesDurableAuthOwner(binding, 7, 'session-b')).toBe(false);
    expect(matchesDurableAuthOwner(null, 7, 'session-a')).toBe(false);
    expect(matchesDurableAuthOwner(binding, 7, null)).toBe(false);
    expect(matchesDurableAuthOwner(binding, 7, '')).toBe(false);
    expect(matchesDurableAuthOwner(binding, 7, '  ')).toBe(false);
  });

  it.each([
    ['POST_MENTION_LIMIT_EXCEEDED', 400],
    ['CONTENT_IS_REQUIRED', 400],
    ['REPLY_CONTENT_EXCEEDS_1000_UNICODE_CODE_POINTS', 400],
    ['INVALID_REPLY_TO_POST_ID', 400],
    ['INVALID_QUOTE_POST_ID', 400],
    ['A_POST_CANNOT_BE_BOTH_A_REPLY_AND_A_QUOTE', 400],
    ['TOO_MANY_MENTION_CANDIDATES', 400],
    ['INVALID_MENTION_TEXT', 400],
    ['INVALID_MEDIA', 400],
    ['POST_REFERENCE_UNAVAILABLE', 404],
  ])('allows only the verified editable rejection %s/%s', (code, status) => {
    const classified = classifySubmissionFailure({
      response: { status, data: { code, error: 'INTERNAL_TEST_DETAIL' } },
    }, 'post');
    expect(classified).toMatchObject({
      category: 'validation',
      failureKind: 'validation_error',
      code,
      canRetryOriginal: false,
      canEdit: true,
    });
    expect(classified.message).not.toContain('INTERNAL_TEST_DETAIL');
  });

  it('uses the post contract code as a diagnostic while allowing safe recovery with a new key', () => {
    expect(classifySubmissionFailure({
      response: { status: 400, data: { code: 'POST_INVALID_IDEMPOTENCY_KEY' } },
    }, 'post')).toMatchObject({
      category: 'request_contract',
      failureKind: 'validation_error',
      code: 'POST_INVALID_IDEMPOTENCY_KEY',
      canRetryOriginal: false,
      canEdit: true,
      message: expect.stringContaining('idempotency key'),
    });
  });

  it('uses distinct safe text for the shared mention-limit code', () => {
    const error = { response: { status: 400, data: { code: 'POST_MENTION_LIMIT_EXCEEDED' } } };
    expect(classifySubmissionFailure(error, 'post').message).toContain('A post can mention');
    expect(classifySubmissionFailure(error, 'reply').message).toContain('A reply can mention');
  });

  it.each([
    { status: 400, code: 'INVALID_REQUEST' },
    { status: 422, code: 'POST_MENTION_LIMIT_EXCEEDED' },
    { status: 404, code: 'UNKNOWN_NOT_FOUND' },
    { status: 500, code: 'INTERNAL_ERROR' },
  ])('keeps unverified rejection shapes conservative: %o', response => {
    expect(classifySubmissionFailure({
      response: { ...response, data: { code: response.code } },
    }, 'post')).toMatchObject({
      category: 'unknown',
      failureKind: 'retryable',
      code: null,
      canRetryOriginal: true,
      canEdit: false,
    });
  });

  it('distinguishes idempotency conflict, recognized rate limits, and auth changes', () => {
    expect(classifySubmissionFailure({
      response: { status: 409, data: { code: 'POST_IDEMPOTENCY_CONFLICT' } },
    }, 'reply')).toMatchObject({ category: 'idempotency_conflict', failureKind: 'idempotency_conflict', canRetryOriginal: false });
    expect(classifySubmissionFailure({
      response: { status: 429, data: { code: 'RATE_LIMITED' } },
    }, 'post')).toMatchObject({ category: 'rate_limited', failureKind: 'retryable', canRetryOriginal: true });
    expect(classifySubmissionFailure(Object.assign(new Error('session changed'), { name: 'AuthSessionChangedError' }), 'reply'))
      .toMatchObject({ category: 'auth_context_changed', failureKind: 'auth_context_changed', canEdit: false });
  });

  it('allows the mention validation failure shape but never retries it', () => {
    expect(hasValidDurableSubmissionFailureShape('failed', 'validation_error')).toBe(true);
    expect(canRetryDurableSubmission('failed', 'validation_error')).toBe(false);
    expect(canAbandonDurableSubmission('failed', 'validation_error')).toBe(true);
  });
});
