<template>
  <main class="detail-view">
    <header class="detail-header">
      <button class="detail-header__back" type="button" aria-label="Back" @click="goBack">
        <AppIcon name="arrow-left" :size="20" />
      </button>
      <h1 class="detail-header__title">Post</h1>
    </header>

    <template v-if="detailPresentation">
      <article class="post-detail">
        <div class="post-detail__author-row">
          <AuthorIdentity
            :author="detailPresentation.author"
            variant="post"
          />
          <button
            v-if="detailPresentation.kind === 'post' && canDeletePost"
            class="post-detail__delete"
            type="button"
            aria-label="Delete post"
            title="Delete post"
            :disabled="deletePending"
            :aria-busy="deletePending"
            @click="handleDeletePost"
          >
            <AppIcon name="trash" :size="18" />
          </button>
        </div>

        <div
          ref="postBodyRef"
          class="post-detail__body"
          :class="{ 'post-detail__body--loading': detailPresentation.kind === 'warm' }"
          :aria-busy="detailPresentation.kind === 'warm' ? 'true' : undefined"
        >
          <LinkifiedText :text="detailPresentation.body" />
          <PostMediaGrid
            v-if="detailPresentation.media.length > 0"
            :media="detailPresentation.media"
          />
        </div>

        <aside
          v-if="detailReference"
          class="post-detail__reference"
          aria-label="Referenced post"
        >
          <span class="post-detail__reference-label">{{ detailReferenceLabel }}</span>
          <template v-if="detailReference.deleted">
            <p class="post-detail__reference-tombstone">{{ detailReferenceMessage }}</p>
          </template>
          <template v-else>
            <AuthorIdentity
              v-if="detailReferenceAuthor"
              :author="detailReferenceAuthor"
              variant="compact"
            />
            <p class="post-detail__reference-content">
              <LinkifiedText :text="detailReferenceContent" />
            </p>
            <PostMediaGrid
              v-if="detailReferenceMedia.length > 0"
              :media="detailReferenceMedia"
            />
          </template>
        </aside>

        <div class="post-detail__meta">
          <span v-if="detailPostTimestamp">{{ detailPostTimestamp }}</span>
          <span
            class="post-detail__views"
            :aria-label="postViewsLabel"
            :title="postViewsLabel"
          >
            {{ formattedViews }} Views
          </span>
        </div>

        <div class="post-detail__engagement" aria-label="Post engagement">
          <template v-if="detailPresentation.kind === 'post'">
            <button
              class="post-detail__metric post-detail__reply"
              type="button"
              :aria-label="detailReplyLabel"
              @click="focusReplyComposer"
            >
              <AppIcon name="reply" :size="18" />
              <span>{{ replyCount }}</span>
            </button>

            <RepostAction
              :key="postId"
              :reposted="reposted"
              :count="repostCount"
              :disabled="!authStore.isAuthenticated || repostStateUnavailable"
              :loading="repostStateLoading"
              :pending="repostSubmitting"
              :ariaLabel="detailRepostLabel"
              variant="detail"
              @toggle="toggleRepost"
            />
            <LikeAction
              :key="postId"
              :liked="liked"
              :count="likeCount"
              :disabled="!authStore.isAuthenticated"
              :loading="likeStateLoading"
              :pending="likeSubmitting"
              :ariaLabel="detailLikeLabel"
              variant="detail"
              @toggle="toggleLike"
            />
          </template>
          <template v-else>
            <span
              class="post-detail__metric post-detail__reply"
              :aria-label="presentationReplyLabel"
              :title="presentationReplyLabel"
            >
              <AppIcon name="reply" :size="18" />
              <span>{{ detailPresentation.replyCount }}</span>
            </span>
            <span
              class="post-detail__metric post-detail__repost"
              :aria-label="presentationRepostLabel"
              :title="presentationRepostLabel"
            >
              <AppIcon name="repost" :size="18" />
              <span>{{ detailPresentation.repostCount }}</span>
            </span>
            <span
              class="post-detail__metric post-detail__like"
              :aria-label="presentationLikeLabel"
              :title="presentationLikeLabel"
            >
              <AppIcon name="heart" :size="18" />
              <span>{{ detailPresentation.likeCount }}</span>
            </span>
          </template>
        </div>

        <p
          v-if="detailPresentation.kind === 'post' && likeError"
          class="detail-inline-error"
          role="status"
        >{{ likeError }}</p>
        <p
          v-if="detailPresentation.kind === 'post' && repostError"
          class="detail-inline-error"
          role="status"
        >{{ repostError }}</p>
        <p
          v-if="detailPresentation.kind === 'post' && deleteError"
          class="detail-inline-error"
          role="alert"
        >{{ deleteError }}</p>

        <div
          v-if="detailPresentation.kind === 'warm'"
          class="detail-warm-loading"
          role="status"
          aria-live="polite"
        >
          <span class="detail-loading__spinner" aria-hidden="true"></span>
          <span class="sr-only">Loading full post</span>
        </div>
      </article>

      <section
        v-if="detailPresentation.kind === 'post'"
        class="post-conversation"
        aria-label="Conversation"
      >
        <ReplyComposer
          :key="postId"
          ref="composerRef"
          :author="replyComposerAuthor"
          v-model="replyDraftContent"
          :submitting="replySubmitting"
          @submit="handleCreateReply"
        />

        <p v-if="replyError" class="reply-error" role="alert">{{ replyError }}</p>

        <div v-if="repliesInitialLoading" class="replies-state" aria-live="polite">
          Loading replies...
        </div>
        <div v-else-if="repliesError" class="replies-state replies-state--error" role="alert">
          <p>Replies could not be loaded.</p>
          <span>{{ repliesError }}</span>
          <button type="button" @click="retryInitialReplies">Retry</button>
        </div>
        <ReplyList
          v-else
          :key="postId"
          :replies="replies"
          :current-identity="currentIdentity"
          :deleting-reply-id="deletingReplyId"
          :has-next="Boolean(nextCursor)"
          :loading-more="repliesLoadingMore"
          :load-more-error="repliesLoadMoreError"
          @load-more="loadMoreReplies"
          @retry="retryLoadMoreReplies"
          @delete="deleteOwnReply"
        />
      </section>
    </template>

    <section v-else-if="postLoading" class="detail-loading" role="status" aria-live="polite">
      <span class="detail-loading__spinner" aria-hidden="true"></span>
      <span class="sr-only">Loading post</span>
    </section>

    <section v-else class="detail-state detail-state--error">
      <h2>{{ postFailureTitle }}</h2>
      <p>{{ postFailureMessage }}</p>
      <RouterLink v-if="!authStore.isAuthenticated" class="detail-state__link" :to="{ name: 'Login' }">
        Log in
      </RouterLink>
      <button v-else type="button" @click="retryPost">Try again</button>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import AuthorIdentity from '../components/AuthorIdentity.vue';
import LinkifiedText from '../components/content/LinkifiedText.vue';
import PostMediaGrid from '../components/content/PostMediaGrid.vue';
import LikeAction from '../components/engagement/LikeAction.vue';
import RepostAction from '../components/engagement/RepostAction.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import ReplyComposer from '../components/replies/ReplyComposer.vue';
import ReplyList from '../components/replies/ReplyList.vue';
import { createPostReply, deletePostReply, getPostReplies } from '../services/replyService';
import { deletePost, getPostById } from '../services/postService';
import { getPostLikeState, likePost, unlikePost } from '../services/likeService';
import { getPostRepostState, repostPost, undoRepostPost } from '../services/repostService';
import { consumePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { createPostViewEventID, getPostViewTelemetry } from '../services/postViewTelemetry';
import { PostReadTracker, createPostReadGeometry } from '../services/postReadTracker';
import { useAuthStore } from '../store/auth';
import { usePostDetailHandoffStore } from '../store/postDetailHandoff';
import { useFeedStore } from '../store/feed';
import { useReplyDraftStore } from '../store/replyDraft';
import {
  syncExternalPostLikeState,
  syncExternalPostRepostState,
  syncExternalPostRemoval,
  syncExternalReplyCount,
} from '../store/sessionSync';
import type { Post } from '../types/Post';
import type { FeedPost } from '../types/Feed';
import type { RecommendationTracking } from '../types/Recommendation';
import type { PublicAuthor } from '../types/User';
import { formatAccessibleEngagementCount, formatCompactEngagementCount } from '../utils/engagementCount';
import { formatPostDetailTimestamp } from '../utils/time';

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const postDetailHandoff = usePostDetailHandoffStore();
const feedStore = useFeedStore();
const replyDraftStore = useReplyDraftStore();
const currentIdentity = computed(() => authStore.currentIdentity);
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);

const postId = computed(() => String(route.params.id ?? '').trim());
const post = ref<Post | null>(null);
const postLoading = ref(false);
const postError = ref('');
const handoffPost = ref<FeedPost | null>(null);
const deletePending = ref(false);
const deleteError = ref('');
const postBodyRef = ref<HTMLElement | null>(null);

const liked = ref(false);
const likeCount = ref(0);
const likeStateLoading = ref(false);
const likeSubmitting = ref(false);
const likeError = ref('');
const reposted = ref(false);
const repostCount = ref(0);
const repostStateLoading = ref(false);
const repostSubmitting = ref(false);
const repostError = ref('');
const repostStateUnavailable = ref(false);

const replies = ref<Post[]>([]);
const nextCursor = ref<string | null>(null);
const repliesInitialLoading = ref(false);
const repliesLoadingMore = ref(false);
const repliesError = ref('');
const repliesLoadMoreError = ref('');
const replySubmitting = ref(false);
const replyError = ref('');
const deletingReplyId = ref<number | null>(null);
const composerRef = ref<InstanceType<typeof ReplyComposer> | null>(null);
const replyCount = ref(0);
const viewCount = ref(0);

let detailRequestVersion = 0;
let deleteRequestVersion = 0;
let likeRequestVersion = 0;
let likeMutationVersion = 0;
let repostRequestVersion = 0;
let repostMutationVersion = 0;
let repliesRequestVersion = 0;
let replyIntentTask: Promise<void> | null = null;
let replyIntentRetryRequested = false;

let tracking: RecommendationTracking | null = null;
let trackedPostID = '';
let readEndSent = false;
let readTracker: PostReadTracker | null = null;
let readResizeObserver: ResizeObserver | null = null;
const clampCount = (value: unknown) => {
  const count = Number(value);
  return Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
};

const isValidPostID = (value: string) => {
  const parsed = Number(value);
  return /^\d+$/.test(value) && Number.isSafeInteger(parsed) && parsed > 0;
};

type DetailPresentation = {
  kind: 'warm' | 'post';
  author: PublicAuthor;
  body: string;
  media: Post['media'];
  createdAt: string;
  likeCount: number;
  repostCount: number;
  reposted: boolean;
  replyCount: number;
  viewCount: number;
};

const detailPresentation = computed<DetailPresentation | null>(() => {
  if (post.value) {
    return {
      kind: 'post',
      author: post.value.author,
      body: post.value.content,
      media: post.value.media,
      createdAt: post.value.published_at || post.value.created_at,
      likeCount: likeCount.value,
      repostCount: repostCount.value,
      reposted: reposted.value,
      replyCount: replyCount.value,
      viewCount: viewCount.value,
    };
  }

  if (postLoading.value && handoffPost.value) {
    return {
      kind: 'warm',
      author: handoffPost.value.author,
      body: handoffPost.value.content,
      media: handoffPost.value.media,
      createdAt: handoffPost.value.createdAt,
      likeCount: handoffPost.value.likeCount,
      repostCount: handoffPost.value.repostCount,
      reposted: handoffPost.value.reposted,
      replyCount: handoffPost.value.replyCount,
      viewCount: handoffPost.value.viewCount,
    };
  }

  return null;
});

const detailReference = computed(() => (
  post.value?.quote_post ?? post.value?.reply_to_post ?? null
));

const detailReferenceLabel = computed(() => (
  post.value?.quote_post ? 'Quoted post' : 'Replying to'
));

const detailReferenceContent = computed(() => {
  const reference = detailReference.value;
  if (!reference || reference.deleted) {
    return '';
  }
  return reference.content?.trim()
    || 'Post';
});
const detailReferenceAuthor = computed(() => {
  const reference = detailReference.value;
  return reference && !reference.deleted ? reference.author : null;
});
const detailReferenceMedia = computed(() => {
  const reference = detailReference.value;
  return reference && !reference.deleted ? reference.media : [];
});
const detailReferenceMessage = 'Post unavailable';

const presentationLikeLabel = computed(() => {
  const count = detailPresentation.value?.likeCount ?? 0;
  return String(count) + (count === 1 ? ' like' : ' likes');
});

const presentationReplyLabel = computed(() => {
  const count = detailPresentation.value?.replyCount ?? 0;
  return String(count) + (count === 1 ? ' reply' : ' replies');
});

const presentationRepostLabel = computed(() => {
  const count = detailPresentation.value?.repostCount ?? 0;
  return String(count) + (count === 1 ? ' repost' : ' reposts');
});

const postFailureTitle = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Log in to view this post';
  }
  return 'Post unavailable';
});

const postFailureMessage = computed(() => {
  if (!authStore.isAuthenticated) {
    return 'Sign in to open this post and join the conversation.';
  }
  return postError.value || 'The post could not be loaded.';
});

const currentViewerID = computed(() => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isFinite(id) && id > 0 ? id : null;
});
const replyComposerAuthor = computed<PublicAuthor | null>(() => {
  const identity = currentIdentity.value;
  const viewerID = currentViewerID.value;

  if (!identity || viewerID === null) {
    return null;
  }

  return {
    id: viewerID,
    username: identity.username,
    display_name: identity.display_name,
    avatar_url: identity.avatar_url,
  };
});

const replyDraftContent = computed({
  get: () => replyDraftStore.getDraft(Number(postId.value)),
  set: value => replyDraftStore.setDraft(Number(postId.value), value),
});

const focusReplyComposer = async () => {
  if (!post.value || !authStore.isAuthenticated) {
    return;
  }

  await composerRef.value?.focus();
};

const postViewTelemetry = getPostViewTelemetry();

const detailPostTimestamp = computed(() => (
  formatPostDetailTimestamp(detailPresentation.value?.createdAt)
));
const formattedViews = computed(() => (
  formatCompactEngagementCount(detailPresentation.value?.viewCount ?? 0)
));
const postViewsLabel = computed(() => (
  formatAccessibleEngagementCount(detailPresentation.value?.viewCount ?? 0, 'views')
));
const detailReplyLabel = computed(() => {
  const count = replyCount.value;
  return 'Reply to post, ' + count + (count === 1 ? ' reply' : ' replies');
});
const detailLikeLabel = computed(() => {
  const count = String(likeCount.value)
    + (likeCount.value === 1 ? ' like' : ' likes');
  return liked.value
    ? 'Unlike post, ' + count
    : 'Like post, ' + count;
});
const detailRepostLabel = computed(() => {
  const count = String(repostCount.value)
    + (repostCount.value === 1 ? ' repost' : ' reposts');
  return reposted.value
    ? 'Undo repost, ' + count
    : 'Repost post, ' + count;
});
const canDeletePost = computed(() => Boolean(
  post.value
  && authStore.isAuthenticated
  && currentViewerID.value !== null
  && post.value.author.id === currentViewerID.value,
));

const mergeReplies = (items: Post[]) => {
  const seen = new Set<number>();
  return items.filter(reply => {
    if (seen.has(reply.id)) {
      return false;
    }
    seen.add(reply.id);
    return true;
  });
};

const disconnectReadGeometryObserver = () => {
  readResizeObserver?.disconnect();
  readResizeObserver = null;
};

const getCurrentPostReadGeometry = () => {
  const element = postBodyRef.value;
  if (!element) {
    return null;
  }

  const rect = element.getBoundingClientRect();
  return {
    postTopDoc: window.scrollY + rect.top,
    postHeight: Math.max(rect.height, 1),
    currentViewportBottomDoc: window.scrollY + window.innerHeight,
  };
};

const updateReadGeometry = () => {
  const geometry = getCurrentPostReadGeometry();
  if (geometry) {
    readTracker?.updateGeometry(geometry);
  }
};

const handleReadScroll = () => {
  if (readTracker) {
    readTracker.recordScroll(window.scrollY + window.innerHeight);
  }
};

const finishRead = (exitType: string) => {
  if (!tracking || readEndSent || !trackedPostID || !readTracker) {
    return false;
  }

  const payload = readTracker.finish(exitType);
  if (!payload) {
    return false;
  }

  readEndSent = true;
  return recommendationTelemetry.recordReadEnd(Number(trackedPostID), tracking, payload);
};

const handleVisibilityChange = () => {
  if (document.visibilityState === 'hidden') {
    readTracker?.pause();
    void recommendationTelemetry.flush(true);
  } else if (tracking && !readEndSent) {
    readTracker?.resume();
  }
};

const handlePageHide = () => {
  finishRead('page_hide');
  void recommendationTelemetry.flush(true);
};

const startRead = (id: string, detailVersion: number) => {
  disconnectReadGeometryObserver();
  readTracker = null;
  tracking = null;
  trackedPostID = id;
  readEndSent = false;

  if (
    detailVersion !== detailRequestVersion
    || postId.value !== id
    || post.value?.id !== Number(id)
  ) {
    return;
  }

  const element = postBodyRef.value;
  if (!element) {
    return;
  }

  tracking = consumePendingRecommendationAttribution(Number(id));
  if (!tracking) {
    return;
  }

  const rect = element.getBoundingClientRect();
  readTracker = new PostReadTracker();
  readTracker.start(
    createPostReadGeometry({ top: rect.top, height: rect.height }, window.scrollY, window.innerHeight),
    document.visibilityState === 'visible',
  );

  if (typeof ResizeObserver !== 'undefined') {
    readResizeObserver = new ResizeObserver(updateReadGeometry);
    readResizeObserver.observe(element);
  }
};

const resetLikeState = () => {
  likeRequestVersion += 1;
  likeMutationVersion += 1;
  liked.value = false;
  likeCount.value = 0;
  likeStateLoading.value = false;
  likeSubmitting.value = false;
  likeError.value = '';
};

const resetRepostState = () => {
  repostRequestVersion += 1;
  repostMutationVersion += 1;
  reposted.value = false;
  repostCount.value = 0;
  repostStateLoading.value = false;
  repostSubmitting.value = false;
  repostError.value = '';
  repostStateUnavailable.value = false;
};

const resetRepliesState = () => {
  repliesRequestVersion += 1;
  replies.value = [];
  nextCursor.value = null;
  repliesInitialLoading.value = false;
  repliesLoadingMore.value = false;
  repliesError.value = '';
  repliesLoadMoreError.value = '';
  replySubmitting.value = false;
  replyError.value = '';
  deletingReplyId.value = null;
  replyCount.value = 0;
};

const resetPostState = () => {
  deleteRequestVersion += 1;
  post.value = null;
  postLoading.value = false;
  postError.value = '';
  viewCount.value = 0;
  deletePending.value = false;
  deleteError.value = '';
};

const getErrorStatus = (error: unknown) =>
  (error as { response?: { status?: number } }).response?.status;

const handleDeletePost = async () => {
  const currentPost = post.value;
  const viewerID = currentViewerID.value;
  if (
    !currentPost
    || viewerID === null
    || !canDeletePost.value
    || deletePending.value
  ) {
    return;
  }
  if (!window.confirm('Delete this post? This cannot be undone.')) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const requestVersion = ++deleteRequestVersion;
  const postID = currentPost.id;
  deletePending.value = true;
  deleteError.value = '';

  const isCurrentDelete = () =>
    requestVersion === deleteRequestVersion
    && detailVersion === detailRequestVersion
    && authStore.isAuthenticated
    && currentViewerID.value === viewerID
    && feedStore.viewerID === viewerID
    && post.value?.id === postID;

  const finishTerminalDelete = () => {
    if (!isCurrentDelete() || !feedStore.markPostDeleted(postID, viewerID)) {
      return false;
    }
    syncExternalPostRemoval(postID);
    replyDraftStore.clearDraft(postID);
    finishRead('route_leave');
    void recommendationTelemetry.flush(false);
    deletePending.value = false;
    deleteError.value = '';
    void router.replace({
      name: 'UserProfile',
      params: { id: String(viewerID) },
    });
    return true;
  };

  try {
    await deletePost(postID);
    finishTerminalDelete();
  } catch (error) {
    if (!isCurrentDelete()) {
      return;
    }

    const status = getErrorStatus(error);
    if (status === 404) {
      finishTerminalDelete();
      return;
    }
    deleteError.value = status === 403
      ? 'You can only delete your own posts.'
      : status === 401
        ? 'Please log in again to delete this post.'
        : 'Could not delete post. Please try again.';
    deletePending.value = false;
  } finally {
    if (requestVersion === deleteRequestVersion && detailVersion === detailRequestVersion) {
      deletePending.value = false;
    }
  }
};

const loadLikeState = async (id: string, detailVersion: number) => {
  if (!authStore.isAuthenticated) {
    return;
  }

  const requestVersion = ++likeRequestVersion;
  const mutationVersionAtStart = likeMutationVersion;
  likeStateLoading.value = true;
  likeError.value = '';

  try {
    const response = await getPostLikeState(id);
    if (
      detailVersion !== detailRequestVersion ||
      requestVersion !== likeRequestVersion ||
      mutationVersionAtStart !== likeMutationVersion
    ) {
      return;
    }

    liked.value = response.liked;
    likeCount.value = clampCount(response.likes);
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
      likeError.value = 'Like status is unavailable. You can still try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === likeRequestVersion) {
      likeStateLoading.value = false;
    }
  }
};

const loadRepostState = async (id: string, detailVersion: number) => {
  if (!authStore.isAuthenticated) {
    return;
  }

  const requestVersion = ++repostRequestVersion;
  const mutationVersionAtStart = repostMutationVersion;
  repostStateLoading.value = true;
  repostError.value = '';
  repostStateUnavailable.value = false;

  try {
    const response = await getPostRepostState(id);
    if (
      detailVersion !== detailRequestVersion
      || requestVersion !== repostRequestVersion
      || mutationVersionAtStart !== repostMutationVersion
    ) {
      return;
    }

    reposted.value = response.reposted;
    repostCount.value = clampCount(response.reposts);
    repostStateUnavailable.value = false;
  } catch {
    if (
      detailVersion === detailRequestVersion
      && requestVersion === repostRequestVersion
      && mutationVersionAtStart === repostMutationVersion
    ) {
      repostError.value = 'Repost status is unavailable. You can still try again.';
      repostStateUnavailable.value = true;
    }
  } finally {
    if (
      detailVersion === detailRequestVersion
      && requestVersion === repostRequestVersion
      && mutationVersionAtStart === repostMutationVersion
    ) {
      repostStateLoading.value = false;
    }
  }
};

const toggleLike = async () => {
  if (
    !post.value ||
    !authStore.isAuthenticated ||
    likeStateLoading.value ||
    likeSubmitting.value
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const id = postId.value;
  const mutationVersion = ++likeMutationVersion;
  const previousLiked = liked.value;
  const previousCount = likeCount.value;

  likeSubmitting.value = true;
  likeError.value = '';
  liked.value = !previousLiked;
  likeCount.value = Math.max(0, previousCount + (liked.value ? 1 : -1));

  try {
    const response = previousLiked ? await unlikePost(id) : await likePost(id);
    if (detailVersion !== detailRequestVersion || mutationVersion !== likeMutationVersion) {
      return;
    }

    liked.value = response.liked;
    likeCount.value = clampCount(response.likes);
    syncExternalPostLikeState({
      postId: Number(id),
      likes: likeCount.value,
      liked: response.liked,
      status: 'ready',
    });
  } catch {
    if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
      liked.value = previousLiked;
      likeCount.value = Math.max(0, previousCount);
      likeError.value = 'Like failed. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && mutationVersion === likeMutationVersion) {
      likeSubmitting.value = false;
    }
  }
};

const toggleRepost = async () => {
  if (
    !post.value
    || !authStore.isAuthenticated
    || repostStateLoading.value
    || repostSubmitting.value
    || repostStateUnavailable.value
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  const id = postId.value;
  const mutationVersion = ++repostMutationVersion;
  const previousReposted = reposted.value;
  const previousCount = repostCount.value;

  repostSubmitting.value = true;
  repostError.value = '';
  repostStateUnavailable.value = false;
  reposted.value = !previousReposted;
  repostCount.value = Math.max(0, previousCount + (reposted.value ? 1 : -1));

  try {
    const response = previousReposted ? await undoRepostPost(id) : await repostPost(id);
    if (detailVersion !== detailRequestVersion || mutationVersion !== repostMutationVersion) {
      return;
    }

    reposted.value = response.reposted;
    repostCount.value = clampCount(response.reposts);
    syncExternalPostRepostState({
      postId: Number(id),
      reposts: repostCount.value,
      reposted: response.reposted,
      status: 'ready',
    });
  } catch {
    if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
      reposted.value = previousReposted;
      repostCount.value = Math.max(0, previousCount);
      repostError.value = 'Could not update repost. Please try again.';
      repostStateUnavailable.value = false;
    }
  } finally {
    if (detailVersion === detailRequestVersion && mutationVersion === repostMutationVersion) {
      repostSubmitting.value = false;
    }
  }
};

const loadInitialReplies = async (id: string, detailVersion: number) => {
  const requestVersion = ++repliesRequestVersion;
  repliesInitialLoading.value = true;
  repliesLoadingMore.value = false;
  repliesError.value = '';
  repliesLoadMoreError.value = '';
  replies.value = [];
  nextCursor.value = null;

  try {
    const page = await getPostReplies(id, { limit: 20 });
    if (detailVersion !== detailRequestVersion || requestVersion !== repliesRequestVersion) {
      return;
    }

    replies.value = mergeReplies(replies.value.concat(page.items));
    nextCursor.value = page.next_cursor || null;
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
      repliesError.value = 'The replies could not be loaded.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
      repliesInitialLoading.value = false;
    }
  }
};

const loadMoreReplies = async () => {
  if (
    !nextCursor.value ||
    repliesInitialLoading.value ||
    repliesLoadingMore.value ||
    !post.value
  ) {
    return;
  }

  const id = postId.value;
  const detailVersion = detailRequestVersion;
  const requestVersion = ++repliesRequestVersion;
  const cursor = nextCursor.value;
  repliesLoadingMore.value = true;
  repliesLoadMoreError.value = '';

  try {
    const page = await getPostReplies(id, { limit: 20, cursor });
    if (detailVersion !== detailRequestVersion || requestVersion !== repliesRequestVersion) {
      return;
    }

    replies.value = mergeReplies(replies.value.concat(page.items));
    nextCursor.value = page.next_cursor || null;
  } catch {
    if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
      repliesLoadMoreError.value = 'Could not load more replies.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && requestVersion === repliesRequestVersion) {
      repliesLoadingMore.value = false;
    }
  }
};

const retryInitialReplies = () => {
  if (post.value) {
    void loadInitialReplies(postId.value, detailRequestVersion);
  }
};

const retryLoadMoreReplies = () => {
  void loadMoreReplies();
};

const handleCreateReply = async (content: string) => {
  if (!post.value || !authStore.isAuthenticated || replySubmitting.value) {
    return;
  }

  const id = postId.value;
  const numericPostID = Number(id);
  const submittingViewerID = currentViewerID.value;
  const submittedDraftSnapshot = replyDraftStore.getDraft(numericPostID);
  const detailVersion = detailRequestVersion;
  replySubmitting.value = true;
  replyError.value = '';

  try {
    const created = await createPostReply(id, content);
    if (
      replyDraftStore.viewerID === submittingViewerID
      && replyDraftStore.getDraft(numericPostID) === submittedDraftSnapshot
    ) {
      replyDraftStore.clearDraft(numericPostID);
    }

    if (detailVersion !== detailRequestVersion || postId.value !== id) {
      return;
    }

    replies.value = mergeReplies([created].concat(replies.value));
    repliesError.value = '';
    replyCount.value = clampCount(replyCount.value + 1);
    syncExternalReplyCount({
      postId: Number(id),
      replyCount: replyCount.value,
    });
  } catch {
    if (detailVersion === detailRequestVersion) {
      replyError.value = 'Reply failed. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion) {
      replySubmitting.value = false;
    }
  }
};

const deleteOwnReply = async (replyID: number) => {
  if (
    !authStore.isAuthenticated ||
    deletingReplyId.value !== null ||
    !replies.value.some(reply => reply.id === replyID)
  ) {
    return;
  }

  const detailVersion = detailRequestVersion;
  deletingReplyId.value = replyID;
  replyError.value = '';

  try {
    await deletePostReply(replyID);
    if (detailVersion !== detailRequestVersion) {
      return;
    }

    replies.value = replies.value.filter(reply => reply.id !== replyID);
    replyCount.value = Math.max(0, replyCount.value - 1);
    syncExternalReplyCount({
      postId: Number(postId.value),
      replyCount: replyCount.value,
    });
  } catch {
    if (detailVersion === detailRequestVersion) {
      replyError.value = 'Reply could not be deleted. Please try again.';
    }
  } finally {
    if (detailVersion === detailRequestVersion && deletingReplyId.value === replyID) {
      deletingReplyId.value = null;
    }
  }
};

const consumeReplyIntent = async () => {
  if (replyIntentTask) {
    replyIntentRetryRequested = true;
    return;
  }

  const task = (async () => {
    if (route.query.reply !== '1' || !authStore.isAuthenticated || !post.value) {
      return;
    }

    const detailVersion = detailRequestVersion;
    const id = postId.value;
    await nextTick();

    const isCurrentIntent = () =>
      route.query.reply === '1'
      && authStore.isAuthenticated
      && detailVersion === detailRequestVersion
      && id === postId.value
      && Boolean(post.value);

    if (!isCurrentIntent()) {
      return;
    }

    const composer = composerRef.value;
    if (!composer || !await composer.focus()) {
      return;
    }

    if (!isCurrentIntent()) {
      return;
    }

    const query = { ...route.query };
    delete query.reply;

    try {
      await router.replace({
        name: 'PostDetail',
        params: route.params,
        query,
        hash: route.hash,
      });
    } catch {
      // Keep the one-shot intent in the URL if the replacement is rejected.
    }
  })();

  replyIntentTask = task;
  try {
    await task;
  } finally {
    if (replyIntentTask !== task) {
      return;
    }

    replyIntentTask = null;
    const shouldRetry = replyIntentRetryRequested;
    replyIntentRetryRequested = false;
    if (shouldRetry && route.query.reply === '1') {
      void consumeReplyIntent();
    }
  }
};

const loadDetail = async (id: string, isAuthenticated: boolean) => {
  const detailVersion = ++detailRequestVersion;
  finishRead('navigate_to_post');
  void recommendationTelemetry.flush(false);
  resetPostState();
  resetLikeState();
  resetRepostState();
  resetRepliesState();
  handoffPost.value = null;

  if (!isAuthenticated) {
    return;
  }

  if (!isValidPostID(id)) {
    postError.value = 'This post URL is not valid.';
    return;
  }

  handoffPost.value = postDetailHandoff.consume(Number(id));
  postLoading.value = true;

  try {
    const loadedPost = await getPostById(id);
    if (detailVersion !== detailRequestVersion) {
      return;
    }
    if (loadedPost.id !== Number(id)) {
      throw new Error('post response id mismatch');
    }

    handoffPost.value = null;
    post.value = loadedPost;
    likeCount.value = clampCount(loadedPost.like_count);
    replyCount.value = clampCount(loadedPost.reply_count);
    viewCount.value = clampCount(loadedPost.view_count);
    postLoading.value = false;
    await nextTick();
    if (
      detailVersion !== detailRequestVersion
      || postId.value !== id
      || post.value?.id !== Number(id)
    ) {
      return;
    }

    postViewTelemetry.enqueue(Number(id), createPostViewEventID(), 'post_detail');
    if (postBodyRef.value) {
      startRead(id, detailVersion);
    }
    void loadLikeState(id, detailVersion);
    void loadRepostState(id, detailVersion);
    void loadInitialReplies(id, detailVersion);
  } catch (error) {
    if (detailVersion === detailRequestVersion) {
      handoffPost.value = null;
      const status = (error as { response?: { status?: number } }).response?.status;
      if (status === 404) {
        replyDraftStore.clearDraft(Number(id));
      }
      postError.value = status === 404
        ? 'This post does not exist.'
        : 'The post could not be loaded.';
    }
  } finally {
    if (detailVersion === detailRequestVersion) {
      postLoading.value = false;
    }
  }
};

const retryPost = () => {
  void loadDetail(postId.value, authStore.isAuthenticated);
};

const goBack = () => {
  const historyState = window.history.state as { back?: string | null } | null;
  if (historyState?.back) {
    router.back();
    return;
  }

  void router.push({ name: 'Home' });
};

watch(
  [postId, () => authStore.isAuthenticated],
  ([id, isAuthenticated]) => {
    void loadDetail(id, isAuthenticated);
  },
  { immediate: true },
);

watch(currentViewerID, (viewerID, previousViewerID) => {
  if (viewerID === previousViewerID) {
    return;
  }
  deleteRequestVersion += 1;
  deletePending.value = false;
  deleteError.value = '';
});

watch(
  currentViewerID,
  viewerID => {
    replyDraftStore.setViewer(viewerID);
  },
  { immediate: true },
);

watch(
  [() => route.query.reply, post, () => authStore.isAuthenticated, replySubmitting],
  () => {
    if (route.query.reply === '1') {
      void consumeReplyIntent();
    }
  },
  { flush: 'post' },
);

onBeforeRouteLeave(to => {
  finishRead(to.name === 'Recommendations' ? 'back_to_recommendation' : 'route_leave');
  void recommendationTelemetry.flush(false);
});

onMounted(() => {
  document.addEventListener('visibilitychange', handleVisibilityChange);
  window.addEventListener('scroll', handleReadScroll, { passive: true });
  window.addEventListener('resize', updateReadGeometry);
  window.addEventListener('pagehide', handlePageHide);
});

onBeforeUnmount(() => {
  finishRead('route_leave');
  void recommendationTelemetry.flush(false);
  disconnectReadGeometryObserver();
  document.removeEventListener('visibilitychange', handleVisibilityChange);
  window.removeEventListener('scroll', handleReadScroll);
  window.removeEventListener('resize', updateReadGeometry);
  window.removeEventListener('pagehide', handlePageHide);
});
</script>

<style scoped>
.detail-view {
  min-height: 100vh;
  background: var(--color-surface);
  color: var(--color-text);
}

.detail-header {
  position: sticky;
  top: 0;
  z-index: 12;
  display: flex;
  align-items: center;
  min-height: 56px;
  padding: 0 var(--space-5);
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.detail-header__back {
  display: grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  place-items: center;
  border: 0;
  border-radius: 50%;
  padding: 0;
  background: transparent;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.detail-header__back:hover,
.detail-header__back:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.detail-header__back:active {
  transform: scale(0.96);
}

.detail-header__back .app-icon {
  flex: 0 0 auto;
}

.detail-header__title {
  margin: 0 0 0 var(--space-3);
  color: var(--color-text);
  font-size: 20px;
  font-weight: 800;
  letter-spacing: -0.02em;
}

.post-detail,
.post-conversation {
  padding: var(--space-5);
}

.post-detail {
  border-bottom: 1px solid var(--color-border);
}

.post-conversation {
  padding-top: 0;
}

.post-detail__author-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 40px;
}

.post-detail__delete {
  display: grid;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  margin-left: auto;
  place-items: center;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.post-detail__delete:hover,
.post-detail__delete:focus-visible {
  background: color-mix(in srgb, var(--color-danger) 10%, transparent);
  color: var(--color-danger);
}

.post-detail__delete:active {
  transform: scale(0.96);
}

.post-detail__delete:disabled {
  cursor: wait;
  opacity: 0.64;
}

.post-detail__body {
  margin-top: var(--space-3);
  color: var(--color-text);
  font-size: 16px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.post-detail__body :deep(.post-media-grid) {
  margin-top: var(--space-4);
}

.post-detail__reference {
  display: grid;
  gap: var(--space-2);
  max-width: 100%;
  margin-top: var(--space-4);
  overflow: hidden;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  background: var(--color-surface-subtle);
}

.post-detail__reference-label {
  color: var(--color-text-tertiary);
  font-size: 12px;
  font-weight: 750;
}

.post-detail__reference-content,
.post-detail__reference-tombstone {
  display: -webkit-box;
  margin: 0;
  overflow: hidden;
  color: var(--color-text-secondary);
  line-height: 1.45;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
}

.post-detail__reference-tombstone {
  color: var(--color-text-tertiary);
  font-style: italic;
}

.post-detail__body--loading {
  color: var(--color-text-secondary);
}

.post-detail__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-3);
  color: var(--color-text-tertiary);
  font-size: 12px;
  line-height: 1.4;
}

.post-detail__meta > span + span::before {
  content: '·';
  margin-right: var(--space-2);
  color: var(--color-border-strong);
}

.post-detail__views {
  white-space: nowrap;
}

.post-detail__engagement {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.post-detail__metric {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-width: 40px;
  min-height: 40px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-3);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.post-detail__metric .app-icon {
  flex: 0 0 auto;
}

.post-detail__engagement > button.post-detail__metric {
  cursor: pointer;
  transition: color var(--transition-fast), background-color var(--transition-fast), transform var(--transition-fast);
}

.post-detail__engagement > button.post-detail__metric:hover,
.post-detail__engagement > button.post-detail__metric:focus-visible {
  background: var(--color-surface-subtle);
  color: var(--color-accent);
}

.post-detail__engagement > button.post-detail__metric:active {
  transform: scale(0.97);
}

.detail-inline-error,
.reply-error {
  margin: var(--space-3) 0 0;
  color: var(--color-danger);
  font-size: 12px;
}

.detail-state__link {
  color: var(--color-accent);
  font-weight: 750;
  text-decoration: none;
}

.detail-state__link:hover {
  text-decoration: underline;
}

.replies-state {
  padding: var(--space-8) 0;
  color: var(--color-text-secondary);
  text-align: center;
}

.replies-state--error {
  display: grid;
  justify-items: center;
  gap: var(--space-2);
}

.replies-state p {
  margin: 0;
  color: var(--color-text);
  font-weight: 700;
}

.replies-state span {
  color: var(--color-text-secondary);
  font-size: 13px;
}

.replies-state button,
.detail-state button {
  min-height: 34px;
  border: 0;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-4);
  background: var(--color-accent);
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  font-weight: 750;
}

.detail-state {
  display: grid;
  justify-items: center;
  gap: var(--space-3);
  min-height: 360px;
  padding: var(--space-8) var(--space-5);
  align-content: center;
  color: var(--color-text-secondary);
  text-align: center;
}

.detail-loading {
  display: grid;
  min-height: 220px;
  place-items: center;
  padding: var(--space-8) var(--space-5);
}

.detail-warm-loading {
  display: grid;
  min-height: 52px;
  place-items: center;
  margin-top: var(--space-4);
}

.detail-loading__spinner {
  display: block;
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-strong);
  border-top-color: var(--color-accent);
  border-radius: 50%;
  animation: detail-spin 700ms linear infinite;
}

.detail-warm-loading .detail-loading__spinner {
  width: 18px;
  height: 18px;
}

@keyframes detail-spin {
  to {
    transform: rotate(360deg);
  }
}

.detail-state p,
.detail-state h2 {
  margin: 0;
}

.detail-state h2 {
  color: var(--color-text);
  font-size: 24px;
}

.detail-state--error {
  border-bottom: 1px solid var(--color-border);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  clip-path: inset(50%);
}

@media (max-width: 799px) {
  .detail-header {
    top: var(--mobile-safe-top);
  }
}

@media (max-width: 420px) {
  .detail-header {
    padding-inline: var(--space-4);
  }

  .post-detail,
  .post-conversation {
    padding-inline: var(--space-4);
  }
}

@media (prefers-reduced-motion: reduce) {
  .detail-header {
    backdrop-filter: none;
  }

  .detail-loading__spinner {
    animation-duration: 1.4s;
  }
}

</style>
