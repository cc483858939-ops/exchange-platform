export type DraftSnapshotMedia = {
  id: string;
  name: string;
  type: string;
  size: number;
  lastModified: number;
};

export type DraftSnapshot = {
  content: string;
  quotePostID: number | null;
  media: DraftSnapshotMedia[];
};

export const isValidQuotePostID = (value: unknown): value is number => (
  typeof value === 'number'
  && Number.isSafeInteger(value)
  && value > 0
);

export const createPostDraftSnapshot = (
  content: string,
  media: readonly DraftSnapshotMedia[],
  quotePostID: number | null = null,
): DraftSnapshot => {
  if (quotePostID !== null && !isValidQuotePostID(quotePostID)) {
    throw new TypeError('A quote post ID must be a positive safe integer.');
  }

  return {
    content,
    quotePostID,
    media: media.map(item => ({
      id: item.id,
      name: item.name,
      type: item.type,
      size: item.size,
      lastModified: item.lastModified,
    })),
  };
};

export const postDraftSnapshotsEqual = (
  left: DraftSnapshot | null | undefined,
  right: DraftSnapshot | null | undefined,
): boolean => Boolean(
  left
  && right
  && left.content === right.content
  && left.quotePostID === right.quotePostID
  && left.media.length === right.media.length
  && left.media.every((item, index) => {
    const other = right.media[index];
    return Boolean(
      other
      && item.id === other.id
      && item.name === other.name
      && item.type === other.type
      && item.size === other.size
      && item.lastModified === other.lastModified,
    );
  }),
);
