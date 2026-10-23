export type DurableSubmissionFailureKind =
  | 'retryable'
  | 'validation_error'
  | 'idempotency_conflict'
  | 'auth_context_changed'
  | null;

export type SubmissionFailureContext = 'post' | 'reply';
export type SubmissionFailureClassification = {
  category: 'validation' | 'request_contract' | 'idempotency_conflict' | 'rate_limited' | 'auth_context_changed' | 'unknown';
  failureKind: Exclude<DurableSubmissionFailureKind, null>;
  code: string | null;
  message: string;
  canRetryOriginal: boolean;
  canEdit: boolean;
};

export const isDurableSubmissionFailureKind = (
  value: unknown,
): value is DurableSubmissionFailureKind => (
  value === null
  || value === 'retryable'
  || value === 'validation_error'
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

type ValidationRule = {
  status: number;
  category?: 'validation' | 'request_contract';
  message: (context: SubmissionFailureContext) => string;
};

const validationRules: Readonly<Record<string, ValidationRule>> = {
  POST_MENTION_LIMIT_EXCEEDED: {
    status: 400,
    message: context => context === 'reply'
      ? 'A reply can mention at most 5 different valid users. Edit the reply before trying again.'
      : 'A post can mention at most 5 different valid users. Edit the draft before trying again.',
  },
  CONTENT_IS_REQUIRED: {
    status: 400,
    message: context => context === 'reply'
      ? 'A reply needs text before it can be sent.'
      : 'A post needs text before it can be sent.',
  },
  REPLY_CONTENT_EXCEEDS_1000_UNICODE_CODE_POINTS: {
    status: 400,
    message: () => 'Replies are limited to 1,000 Unicode code points. Edit the reply before trying again.',
  },
  INVALID_REPLY_TO_POST_ID: {
    status: 400,
    message: () => 'The reply target is invalid. Reopen the post and try again.',
  },
  INVALID_QUOTE_POST_ID: {
    status: 400,
    message: () => 'The quoted post is invalid. Choose a post to quote and try again.',
  },
  A_POST_CANNOT_BE_BOTH_A_REPLY_AND_A_QUOTE: {
    status: 400,
    message: () => 'A post cannot be both a reply and a quote. Edit the draft and try again.',
  },
  TOO_MANY_MENTION_CANDIDATES: {
    status: 400,
    message: () => 'This text contains too many @-mention candidates. Edit the text and try again.',
  },
  INVALID_MENTION_TEXT: {
    status: 400,
    message: () => 'Review the @-mentions in your text and try again.',
  },
  INVALID_MEDIA: {
    status: 400,
    message: () => 'One or more images could not be attached. Remove or reselect the image and try again.',
  },
  POST_REFERENCE_UNAVAILABLE: {
    status: 404,
    message: () => 'The post you are replying to or quoting is no longer available.',
  },
  POST_INVALID_IDEMPOTENCY_KEY: {
    status: 400,
    category: 'request_contract',
    message: () => 'The post request had an invalid idempotency key. Your content is preserved; edit and submit again to start a valid attempt.',
  },
};

const failure = (
  category: SubmissionFailureClassification['category'],
  failureKind: SubmissionFailureClassification['failureKind'],
  code: string | null,
  message: string,
  canRetryOriginal: boolean,
  canEdit: boolean,
): SubmissionFailureClassification => ({
  category,
  failureKind,
  code,
  message,
  canRetryOriginal,
  canEdit,
});

export const classifySubmissionFailure = (
  error: unknown,
  context: SubmissionFailureContext,
): SubmissionFailureClassification => {
  const unknownMessage = context === 'reply'
    ? 'Couldn’t confirm this reply. Retry safely.'
    : 'Couldn’t confirm this post. Retry safely.';
  if (error instanceof Error && (
    error.name === 'AuthSessionChangedError'
    || error.name === 'PublishAuthContextChangedError'
  )) {
    return failure(
      'auth_context_changed',
      'auth_context_changed',
      null,
      'Your session changed. Verify your account before recovering this submission.',
      false,
      false,
    );
  }

  if (!error || typeof error !== 'object') {
    return failure('unknown', 'retryable', null, unknownMessage, true, false);
  }

  const response = (error as {
    response?: { status?: unknown; data?: { code?: unknown } };
  }).response;
  const status = response?.status;
  const code = typeof response?.data?.code === 'string' ? response.data.code : null;

  if (status === 409 && code === 'POST_IDEMPOTENCY_CONFLICT') {
    return failure(
      'idempotency_conflict',
      'idempotency_conflict',
      code,
      context === 'reply'
        ? 'This reply can’t be retried safely.'
        : 'This post can’t be retried safely.',
      false,
      false,
    );
  }

  if (status === 429 && code === 'RATE_LIMITED') {
    return failure(
      'rate_limited',
      'retryable',
      code,
      'Posting is temporarily rate limited. Wait, then retry safely.',
      true,
      false,
    );
  }

  if (code) {
    const rule = validationRules[code];
    if (rule && status === rule.status) {
      const category = rule.category ?? 'validation';
      return failure(
        category,
        'validation_error',
        code,
        rule.message(context),
        false,
        true,
      );
    }
  }

  return failure(
    'unknown',
    'retryable',
    null,
    unknownMessage,
    true,
    false,
  );
};
