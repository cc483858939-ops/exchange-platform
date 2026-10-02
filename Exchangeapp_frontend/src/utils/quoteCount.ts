export const normalizeQuoteCount = (value: unknown): number | null => (
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
    ? value
    : null
);

export const normalizePostQuoteCountUpdate = (update: {
  postId: unknown;
  quoteCount: unknown;
}) => {
  const { postId, quoteCount } = update;
  if (typeof postId !== 'number' || !Number.isSafeInteger(postId) || postId <= 0) {
    return null;
  }
  const normalizedQuoteCount = normalizeQuoteCount(quoteCount);
  if (normalizedQuoteCount === null) return null;
  return { postId, quoteCount: normalizedQuoteCount };
};
