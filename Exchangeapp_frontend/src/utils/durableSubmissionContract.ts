export type DurableSubmissionFailureKind =
  | 'retryable'
  | 'idempotency_conflict'
  | 'auth_context_changed'
  | null;

export const isDurableSubmissionFailureKind = (
  value: unknown,
): value is DurableSubmissionFailureKind => (
  value === null
  || value === 'retryable'
  || value === 'idempotency_conflict'
  || value === 'auth_context_changed'
);

export const hasValidDurableSubmissionFailureShape = (
  phase: string,
  failureKind: DurableSubmissionFailureKind,
): boolean => (
  phase === 'failed'
    ? failureKind !== null
    : failureKind === null
);

export const canRetryDurableSubmission = (
  phase: string,
  failureKind: DurableSubmissionFailureKind,
): boolean => (
  phase === 'failed'
  && failureKind === 'retryable'
);

export const canAbandonDurableSubmission = (
  phase: string,
  failureKind: DurableSubmissionFailureKind,
): boolean => (
  phase === 'failed'
  && failureKind !== null
);

export const canMarkDurableSubmissionFailed = (phase: string): boolean => phase !== 'succeeded';

export const recoveredSubmissionMustFailClosed = (
  phase: string,
  durableSessionID: string | null | undefined,
): boolean => (
  phase !== 'succeeded'
  && (
    typeof durableSessionID !== 'string'
    || !durableSessionID.trim()
  )
);

export const isPostIdempotencyConflictError = (error: unknown): boolean => {
  if (!error || typeof error !== 'object') return false;
  const response = (error as {
    response?: {
      status?: unknown;
      data?: { code?: unknown };
    };
  }).response;
  return response?.status === 409
    && response.data?.code === 'POST_IDEMPOTENCY_CONFLICT';
};
