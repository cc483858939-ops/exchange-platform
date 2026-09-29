export type DraftSnapshotMedia = {
  id: string;
  name: string;
  type: string;
  size: number;
  lastModified: number;
};

export type DraftSnapshot = {
  content: string;
  media: DraftSnapshotMedia[];
};

export const createPostDraftSnapshot = (
  content: string,
  media: readonly DraftSnapshotMedia[],
): DraftSnapshot => ({
  content,
  media: media.map(item => ({
    id: item.id,
    name: item.name,
    type: item.type,
    size: item.size,
    lastModified: item.lastModified,
  })),
});

export const postDraftSnapshotsEqual = (
  left: DraftSnapshot | null | undefined,
  right: DraftSnapshot | null | undefined,
): boolean => Boolean(
  left
  && right
  && left.content === right.content
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
