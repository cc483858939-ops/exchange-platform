import apiClient from '../axios';

export type LikeEngagementState =
  | {
      status: 'ready';
      likes: number;
      liked: boolean;
    }
  | {
      status: 'unavailable';
    };

export type RepostEngagementState =
  | {
      status: 'ready';
      reposts: number;
      reposted: boolean;
    }
  | {
      status: 'unavailable';
    };

export type BookmarkEngagementState =
  | {
      status: 'ready';
      bookmarked: boolean;
    }
  | {
      status: 'unavailable';
    };

export type PostEngagementState = {
  post_id: number;
  like: LikeEngagementState;
  repost: RepostEngagementState;
  bookmark: BookmarkEngagementState;
};

export type PostEngagementStatesResponse = {
  items: PostEngagementState[];
};

const batchLimit = 100;

export async function getPostEngagementStates(
  postIDs: number[],
): Promise<PostEngagementStatesResponse> {
  const uniqueIDs = Array.from(new Set(postIDs));
  if (uniqueIDs.length === 0) return { items: [] };

  const result: PostEngagementStatesResponse = { items: [] };
  for (let offset = 0; offset < uniqueIDs.length; offset += batchLimit) {
    const postIDsInBatch = uniqueIDs.slice(offset, offset + batchLimit);
    const response = await apiClient.post<PostEngagementStatesResponse>(
      '/posts/engagement-states',
      { post_ids: postIDsInBatch },
    );
    result.items.push(...(response.data.items ?? []));
  }
  return result;
}
