import { describe, expect, it } from 'vitest';
import {
  canAbandonDurableSubmission,
  canMarkDurableSubmissionFailed,
  canRetryDurableSubmission,
  hasValidDurableSubmissionFailureShape,
  isDurableSubmissionFailureKind,
  isPostIdempotencyConflictError,
} from './durableSubmissionContract';
import { matchesDurableAuthOwner } from '../auth/authRequestBinding';

describe('durable submission contract', () => {
  it.each([
    ['uploading', null, true, false, false],
    ['publishing', null, true, false, false],
    ['succeeded', null, true, false, false],
    ['failed', 'retryable', true, true, true],
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
    expect(isDurableSubmissionFailureKind('idempotency_conflict')).toBe(true);
    expect(isDurableSubmissionFailureKind('auth_context_changed')).toBe(true);
    expect(isDurableSubmissionFailureKind('unknown')).toBe(false);
    expect(isDurableSubmissionFailureKind(undefined)).toBe(false);
  });

  it.each([
    ['publishing', true],
    ['uploading', true],
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

  it('recognizes only the post idempotency conflict response shape', () => {
    expect(isPostIdempotencyConflictError({
      response: { status: 409, data: { code: 'POST_IDEMPOTENCY_CONFLICT' } },
    })).toBe(true);
    expect(isPostIdempotencyConflictError({
      response: { status: 409, data: { code: 'OTHER' } },
    })).toBe(false);
    expect(isPostIdempotencyConflictError({
      response: { status: 400, data: { code: 'POST_IDEMPOTENCY_CONFLICT' } },
    })).toBe(false);
    expect(isPostIdempotencyConflictError(null)).toBe(false);
  });
});
