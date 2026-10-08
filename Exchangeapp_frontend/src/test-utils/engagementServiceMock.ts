import type {
  PostBatchBookmarkStateItem,
  PostBatchBookmarkStatesResponse,
} from '../services/bookmarkService';
import type {
  BookmarkEngagementState,
  LikeEngagementState,
  PostEngagementStatesResponse,
  RepostEngagementState,
} from '../services/engagementService';

type PostBatchLikeStateItem = { post_id: number; likes: number; liked: boolean };
type PostBatchRepostStateItem = { post_id: number; reposts: number; reposted: boolean };
type BatchResponse<T> = { items: T[]; unavailable_post_ids: number[] };
type PostBatchLikeStatesResponse = BatchResponse<PostBatchLikeStateItem>;
type PostBatchRepostStatesResponse = BatchResponse<PostBatchRepostStateItem>;

type BatchLoaders = Partial<{
  likes: (postIDs: number[]) => Promise<PostBatchLikeStatesResponse>;
  reposts: (postIDs: number[]) => Promise<PostBatchRepostStatesResponse>;
  bookmarks: (postIDs: number[]) => Promise<PostBatchBookmarkStatesResponse>;
  getPostLikeStates: (postIDs: number[]) => Promise<PostBatchLikeStatesResponse>;
  getPostRepostStates: (postIDs: number[]) => Promise<PostBatchRepostStatesResponse>;
  getPostBookmarkStates: (postIDs: number[]) => Promise<PostBatchBookmarkStatesResponse>;
}>;

const loadState = <T extends { post_id: number }, S>(
  result: PromiseSettledResult<{ items: T[]; unavailable_post_ids: number[] }>,
  postID: number,
  toState: (item: T) => S,
): S | { status: 'unavailable' } => {
  if (result.status === 'rejected') return { status: 'unavailable' };
  const item = result.value.items?.find(candidate => candidate.post_id === postID);
  return item ? toState(item) : { status: 'unavailable' };
};

export async function engagementResponseFromBatchMocks(
  postIDs: number[],
  loaders: BatchLoaders,
): Promise<PostEngagementStatesResponse> {
  const emptyResponse = async () => ({ items: [], unavailable_post_ids: [] });
  const [likes, reposts, bookmarks] = await Promise.allSettled([
    (loaders.getPostLikeStates ?? loaders.likes)?.(postIDs) ?? emptyResponse(),
    (loaders.getPostRepostStates ?? loaders.reposts)?.(postIDs) ?? emptyResponse(),
    (loaders.getPostBookmarkStates ?? loaders.bookmarks)?.(postIDs) ?? emptyResponse(),
  ]);
  const items: PostEngagementStatesResponse['items'] = postIDs.map((post_id) => ({
    post_id,
    like: loadState<PostBatchLikeStateItem, LikeEngagementState>(likes, post_id, item => ({
      status: 'ready',
      likes: item.likes,
      liked: item.liked,
    })),
    repost: loadState<PostBatchRepostStateItem, RepostEngagementState>(reposts, post_id, item => ({
      status: 'ready',
      reposts: item.reposts,
      reposted: item.reposted,
    })),
    bookmark: loadState<PostBatchBookmarkStateItem, BookmarkEngagementState>(bookmarks, post_id, item => ({
      status: 'ready',
      bookmarked: item.bookmarked,
    })),
  }));
  return { items };
}

type DetailLoaders = Partial<{
  like: (postID: number) => Promise<{ likes: number; liked: boolean }>;
  repost: (postID: number) => Promise<{ reposts: number; reposted: boolean }>;
  bookmark: (postIDs: number[]) => Promise<PostBatchBookmarkStatesResponse>;
}>;

export async function engagementResponseFromDetailMocks(
  postIDs: number[],
  loaders: DetailLoaders,
): Promise<PostEngagementStatesResponse> {
  const items = await Promise.all(postIDs.map(async (post_id) => {
    const [like, repost, bookmark] = await Promise.allSettled([
      loaders.like?.(post_id) ?? Promise.reject(new Error('unavailable')),
      loaders.repost?.(post_id) ?? Promise.reject(new Error('unavailable')),
      loaders.bookmark?.([post_id]) ?? Promise.reject(new Error('unavailable')),
    ]);
    const bookmarkItem = bookmark.status === 'fulfilled'
      ? bookmark.value.items?.find(item => item.post_id === post_id)
      : undefined;
    return {
      post_id,
      like: like.status === 'fulfilled' && like.value
        ? { status: 'ready' as const, likes: like.value.likes, liked: like.value.liked }
        : { status: 'unavailable' as const },
      repost: repost.status === 'fulfilled' && repost.value
        ? { status: 'ready' as const, reposts: repost.value.reposts, reposted: repost.value.reposted }
        : { status: 'unavailable' as const },
      bookmark: bookmarkItem
        ? { status: 'ready' as const, bookmarked: bookmarkItem.bookmarked }
        : { status: 'unavailable' as const },
    };
  }));
  return { items };
}
