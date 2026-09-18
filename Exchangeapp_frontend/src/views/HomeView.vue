<template>
  <main class="home-view">
    <header class="home-feed-header">
      <MobileHomeHeader />
      <div class="home-feed-header__content">
        <h1>Home</h1>
        <FeedTabs
          :active-tab="activeTab"
          @select="selectTab"
          @reselect="reselectTab"
        />
      </div>
    </header>

    <div
      :id="'feed-panel-' + activeTab"
      class="home-feed-panel"
      ref="feedPanelRef"
      @scroll.passive="handleFeedScroll"
      @wheel.passive="handleFeedUserScrollIntent"
      @touchmove.passive="handleFeedUserScrollIntent"
      @keydown="handleFeedKeydown"
      role="tabpanel"
      tabindex="0"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
    <section
      v-if="!authStore.isAuthenticated"
      class="home-state home-state--auth"
      aria-labelledby="home-auth-title"
    >
      <h2 id="home-auth-title">Sign in to view your financial feed</h2>
      <div class="home-state__actions">
        <RouterLink class="home-state__primary" :to="{ name: 'Login' }">Log in</RouterLink>
        <RouterLink class="home-state__secondary" :to="{ name: 'Register' }">Sign up</RouterLink>
      </div>
    </section>

    <section
      v-else-if="activeFeedStatus.loading && !hasRecentlyPublishedPosts"
      class="feed-list feed-list--loading"
      :aria-labelledby="'feed-tab-' + activeTab"
    >
      <article v-for="skeleton in skeletonPosts" :key="skeleton" class="feed-skeleton" aria-hidden="true">
        <span class="feed-skeleton__author"></span>
        <span class="feed-skeleton__title"></span>
        <span class="feed-skeleton__line"></span>
        <span class="feed-skeleton__line feed-skeleton__line--short"></span>
      </article>
    </section>

    <section
      v-else-if="activeFeedStatus.error && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>Feed unavailable</h2>
      <button class="home-state__primary" type="button" @click="retryActiveFeed">Retry</button>
    </section>

    <section
      v-else-if="activeFeedStatus.empty && !hasRecentlyPublishedPosts"
      class="home-state"
      aria-live="polite"
    >
      <h2>{{ activeTab === 'for-you' ? 'No recommendations yet' : 'No posts from people you follow yet' }}</h2>
    </section>

    <section
      v-else
      class="feed-list"
    >
      <template v-if="activeTab === 'for-you'">
        <div
          class="home-virtual-list"
          data-virtual-feed="for-you"
          :style="{ height: `${forYouVirtualizerTotalSize}px` }"
        >
          <div
            v-for="virtualItem in forYouVirtualItems"
            :key="String(virtualItem.key)"
            class="home-virtual-row"
            :data-index="virtualItem.index"
            :style="virtualRowStyle(virtualItem)"
            :ref="measureForYouRow"
          >
            <PostCard
              v-if="forYouRowKind(virtualItem.index) === 'recent'"
              :post="forYouPostForRow(virtualItem.index)"
              :view-session-key="homeViewSessionKey('for-you')"
              :like-pending="likePendingPostIds.has(forYouPostForRow(virtualItem.index).id)"
              :repost-pending="repostPendingPostIds.has(forYouPostForRow(virtualItem.index).id)"
              :bookmark-pending="bookmarkPendingPostIds.has(forYouPostForRow(virtualItem.index).id)"
              :show-delete="canDeletePost(forYouPostForRow(virtualItem.index))"
              :delete-pending="pendingDeletePostIds.has(forYouPostForRow(virtualItem.index).id)"
              :delete-error="deleteErrors.get(forYouPostForRow(virtualItem.index).id) || ''"
              @toggle-like="handleLikeToggle"
              @toggle-repost="handleRepostToggle"
              @toggle-bookmark="handleBookmarkToggle"
              @delete-post="handleDeletePost"
            />

            <div
              v-else-if="forYouRowKind(virtualItem.index) === 'inline-state'"
              class="home-feed-inline-state"
              aria-live="polite"
            >
              <template v-if="forYouInlineStateForRow(virtualItem.index) === 'loading'">
                Loading recommendations...
              </template>
              <template v-else>
                <span>Could not load recommendations.</span>
                <button class="home-state__primary" type="button" @click="loadForYou(true)">
                  Retry
                </button>
              </template>
            </div>

            <div
              v-else-if="forYouRowKind(virtualItem.index) === 'recommendation'"
              class="recommendation-card-wrapper"
              :ref="recommendationCardRefForRow(virtualItem.index)"
            >
              <PostCard
                :post="forYouRecommendationForRow(virtualItem.index).post"
                :view-session-key="homeViewSessionKey('for-you')"
                :like-pending="likePendingPostIds.has(forYouRecommendationForRow(virtualItem.index).post.id)"
                :repost-pending="repostPendingPostIds.has(forYouRecommendationForRow(virtualItem.index).post.id)"
                :bookmark-pending="bookmarkPendingPostIds.has(forYouRecommendationForRow(virtualItem.index).post.id)"
                :show-not-interested="true"
                :show-delete="canDeletePost(forYouRecommendationForRow(virtualItem.index).post)"
                :delete-pending="pendingDeletePostIds.has(forYouRecommendationForRow(virtualItem.index).post.id)"
                :delete-error="deleteErrors.get(forYouRecommendationForRow(virtualItem.index).post.id) || ''"
                @post-click="handleRecommendationClick(forYouRecommendationForRow(virtualItem.index).recommendation)"
                @toggle-like="handleLikeToggle"
                @toggle-repost="handleRepostToggle"
                @toggle-bookmark="handleBookmarkToggle"
                @not-interested="handleNotInterested"
                @delete-post="handleDeletePost"
              />
            </div>
          </div>
        </div>

        <div
          v-if="!forYouFeed.depleted || forYouFeed.loadingMore || forYouFeed.loadMoreError"
          ref="forYouSentinelRef"
          class="home-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="forYouFeed.loadingMore">Loading more recommendations...</span>
          <template v-else-if="forYouFeed.loadMoreError">
            <span>Could not load more recommendations.</span>
            <button class="home-state__primary" type="button" @click="retryForYouLoadMore">
              Retry
            </button>
          </template>
          <button
            v-else-if="!forYouIntersectionObserverAvailable && !forYouFeed.depleted"
            class="home-state__primary"
            type="button"
            @click="loadMoreForYou"
          >
            Load more recommendations
          </button>
        </div>
      </template>

      <template v-else>
        <div
          class="home-virtual-list"
          data-virtual-feed="following"
          :style="{ height: `${followingVirtualizerTotalSize}px` }"
        >
          <div
            v-for="virtualItem in followingVirtualItems"
            :key="String(virtualItem.key)"
            class="home-virtual-row"
            :data-index="virtualItem.index"
            :style="virtualRowStyle(virtualItem)"
            :ref="measureFollowingRow"
          >
            <PostCard
              v-if="followingRowKind(virtualItem.index) === 'following'"
              :post="followingPostForRow(virtualItem.index)"
              :view-session-key="homeViewSessionKey('following')"
              :like-pending="likePendingPostIds.has(followingPostForRow(virtualItem.index).id)"
              :repost-pending="repostPendingPostIds.has(followingPostForRow(virtualItem.index).id)"
              :bookmark-pending="bookmarkPendingPostIds.has(followingPostForRow(virtualItem.index).id)"
              :show-delete="canDeletePost(followingPostForRow(virtualItem.index))"
              :delete-pending="pendingDeletePostIds.has(followingPostForRow(virtualItem.index).id)"
              :delete-error="deleteErrors.get(followingPostForRow(virtualItem.index).id) || ''"
              @toggle-like="handleLikeToggle"
              @toggle-repost="handleRepostToggle"
              @toggle-bookmark="handleBookmarkToggle"
              @delete-post="handleDeletePost"
            />
          </div>
        </div>

        <div
          v-if="followingFeed.nextCursor || followingFeed.loadingMore || followingFeed.loadMoreError"
          ref="followingSentinelRef"
          class="home-feed-sentinel"
          aria-live="polite"
        >
          <span v-if="followingFeed.loadingMore">Loading more posts...</span>
          <template v-else-if="followingFeed.loadMoreError">
            <span>Could not load more posts.</span>
            <button class="home-state__primary" type="button" @click="retryFollowingLoadMore">
              Retry
            </button>
          </template>
          <button
            v-else-if="!followingIntersectionObserverAvailable && followingFeed.nextCursor"
            class="home-state__primary"
            type="button"
            @click="loadMoreFollowing"
          >
            Load more posts
          </button>
        </div>
      </template>
    </section>

    </div>

    <RouterLink
      v-if="authStore.isAuthenticated"
      class="home-compose-fab"
      :to="{ name: 'PostCreate' }"
      aria-label="Post"
      title="Post"
    >
      <AppIcon name="compose" :size="27" />
    </RouterLink>
  </main>
</template>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  reactive,
  ref,
  watch,
} from 'vue';
import { useVirtualizer } from '@tanstack/vue-virtual';
import type { VirtualItem, Virtualizer } from '@tanstack/vue-virtual';
import { ElMessage } from 'element-plus';
import 'element-plus/es/components/message/style/css';
import type { ComponentPublicInstance } from 'vue';
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router';
import { isInitialDocumentReloadForRoute } from '../router/documentNavigation';
import FeedTabs from '../components/feed/FeedTabs.vue';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileHomeHeader from '../components/layout/MobileHomeHeader.vue';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { getPostViewTelemetry } from '../services/postViewTelemetry';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import { useHomeTimelineStore } from '../store/homeTimeline';
import type { HomeRecommendationItem } from '../store/homeTimeline';
import type { RecommendedPost } from '../types/Recommendation';
import type { FeedPost, FeedTab } from '../types/Feed';

defineOptions({ name: 'HomeView' });

const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const homeTimeline = useHomeTimelineStore();
const initialHomeDocumentReload = isInitialDocumentReloadForRoute(route.fullPath);
const initialHomeDocumentReloadTab: FeedTab = route.query.tab === 'following'
  ? 'following'
  : 'for-you';
if (initialHomeDocumentReload) {
  homeTimeline.setScrollTop(initialHomeDocumentReloadTab, 0);
}
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);
const getHomePostViewTelemetry = () => getPostViewTelemetry();

const skeletonPosts = [0, 1, 2];
const recommendationCardElements = new Map<number, HTMLElement>();
const feedPanelRef = ref<HTMLElement | null>(null);
const forYouSentinelRef = ref<HTMLElement | null>(null);
const forYouIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let forYouObserver: IntersectionObserver | null = null;
const followingSentinelRef = ref<HTMLElement | null>(null);
const followingIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let followingObserver: IntersectionObserver | null = null;
let feedPanelResizeObserver: ResizeObserver | null = null;
let lastMeasuredFeedPanelWidth: number | null = null;

const forYouFeed = homeTimeline.forYou;
const followingFeed = homeTimeline.following;
const likePendingPostIds = homeTimeline.likePendingPostIds;
const repostPendingPostIds = homeTimeline.repostPendingPostIds;
const bookmarkPendingPostIds = homeTimeline.bookmarkPendingPostIds;
const pendingDeletePostIds = homeTimeline.pendingDeletePostIds;
const deleteErrors = homeTimeline.deleteErrors;
const homeViewActive = ref(true);
const HOME_RESELECT_TOP_THRESHOLD_PX = 8;
const HOME_USER_SCROLL_INTENT_WINDOW_MS = 500;
const HOME_SCROLL_INTENT_KEYS = new Set([
  'ArrowDown',
  'ArrowUp',
  'PageDown',
  'PageUp',
  'Home',
  'End',
  ' ',
  'Spacebar',
]);
const HOME_POST_ESTIMATE_PX = 360;
const HOME_INLINE_STATE_ESTIMATE_PX = 72;
const HOME_VIRTUAL_OVERSCAN = 8;
let resumeOnActivation = false;
let lastUserScrollIntentAt = Number.NEGATIVE_INFINITY;
let correctingPinnedScroll = false;

const currentViewerID = () => {
  const id = authStore.currentIdentity?.id;
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
};

const homeViewSessionVersion = reactive<Record<FeedTab, number>>({
  'for-you': 0,
  following: 0,
});

const makeHomeViewSessionKey = (viewerID: number | null, tab: FeedTab, version: number) => (
  `home:${viewerID ?? 'anonymous'}:${tab}:${version}`
);

const homeViewSessionKeys = computed<Record<FeedTab, string>>(() => {
  const viewerID = currentViewerID();
  return {
    'for-you': makeHomeViewSessionKey(viewerID, 'for-you', homeViewSessionVersion['for-you']),
    following: makeHomeViewSessionKey(viewerID, 'following', homeViewSessionVersion.following),
  };
});

const homeViewSessionKey = (tab: FeedTab) => homeViewSessionKeys.value[tab];

const releaseHomeViewSession = (tab: FeedTab, viewerID = currentViewerID()) => {
  getHomePostViewTelemetry().releaseFeedViewSession(
    makeHomeViewSessionKey(viewerID, tab, homeViewSessionVersion[tab]),
  );
};

const rotateHomeViewSession = (tab: FeedTab) => {
  releaseHomeViewSession(tab);
  homeViewSessionVersion[tab] += 1;
};

const releaseHomeViewSessionsForViewer = (viewerID: number | null) => {
  (['for-you', 'following'] as FeedTab[]).forEach((tab) => {
    releaseHomeViewSession(tab, viewerID);
  });
};

let lastHomeViewerID = currentViewerID();
let lastHomeAuthenticated = authStore.isAuthenticated;

const activeTab = computed<FeedTab>(() => homeTimeline.activeTab);
const feedTopPinned = reactive<Record<FeedTab, boolean>>({
  'for-you': false,
  following: false,
});
const activeFeedStatus = computed(() => {
  const state = activeTab.value === 'for-you' ? forYouFeed : followingFeed;
  return {
    loading: state.loading || (!state.loaded && !state.error),
    error: state.error,
    empty: state.loaded && !state.loading && !state.error && state.items.length === 0,
  };
});

const hasRecentlyPublishedPosts = computed(
  () => activeTab.value === 'for-you' && feedStore.recentlyPublishedPosts.length > 0,
);
const recentlyPublishedIDs = computed(
  () => new Set(feedStore.recentlyPublishedPosts.map((post) => post.id)),
);
const visibleForYouItems = computed(() => forYouFeed.items.filter((item) =>
  !recentlyPublishedIDs.value.has(item.post.id)
  && !feedStore.isPostDeleted(item.post.id)
));

type ForYouVirtualRow =
  | {
      kind: 'recent';
      key: string;
      post: FeedPost;
    }
  | {
      kind: 'inline-state';
      key: string;
      state: 'loading' | 'error';
    }
  | {
      kind: 'recommendation';
      key: string;
      item: HomeRecommendationItem;
    };

type FollowingVirtualRow = {
  kind: 'following';
  key: string;
  post: FeedPost;
};

const forYouVirtualRows = computed<ForYouVirtualRow[]>(() => {
  const rows: ForYouVirtualRow[] = feedStore.recentlyPublishedPosts.map((post) => ({
    kind: 'recent',
    key: `recent:${post.id}`,
    post,
  }));

  if (forYouFeed.loading && hasRecentlyPublishedPosts.value) {
    rows.push({
      kind: 'inline-state',
      key: 'for-you:inline-loading',
      state: 'loading',
    });
  } else if (forYouFeed.error && hasRecentlyPublishedPosts.value) {
    rows.push({
      kind: 'inline-state',
      key: 'for-you:inline-error',
      state: 'error',
    });
  }

  visibleForYouItems.value.forEach((item) => {
    rows.push({
      kind: 'recommendation',
      key: `recommendation:${item.post.id}`,
      item,
    });
  });

  return rows;
});

const followingVirtualRows = computed<FollowingVirtualRow[]>(() => (
  followingFeed.items.map((post) => ({
    kind: 'following',
    key: `following:${post.id}`,
    post,
  }))
));

const isVirtualFeedRendered = computed(() => (
  authStore.isAuthenticated
  && !(activeFeedStatus.value.loading && !hasRecentlyPublishedPosts.value)
  && !(activeFeedStatus.value.error && !hasRecentlyPublishedPosts.value)
  && !(activeFeedStatus.value.empty && !hasRecentlyPublishedPosts.value)
));

type HomeVirtualizer = Virtualizer<HTMLElement, HTMLElement>;

const forYouVirtualizerOptions = computed(() => ({
  count: forYouVirtualRows.value.length,
  getScrollElement: () => (
    homeViewActive.value
    && authStore.isAuthenticated
    && activeTab.value === 'for-you'
    && isVirtualFeedRendered.value
      ? feedPanelRef.value
      : null
  ),
  estimateSize: (index: number) => (
    forYouVirtualRows.value[index]?.kind === 'inline-state'
      ? HOME_INLINE_STATE_ESTIMATE_PX
      : HOME_POST_ESTIMATE_PX
  ),
  getItemKey: (index: number) => forYouVirtualRows.value[index]?.key ?? index,
  enabled: true,
  useCachedMeasurements: !homeViewActive.value
    || !authStore.isAuthenticated
    || activeTab.value !== 'for-you'
    || !isVirtualFeedRendered.value,
  overscan: HOME_VIRTUAL_OVERSCAN,
}));

const followingVirtualizerOptions = computed(() => ({
  count: followingVirtualRows.value.length,
  getScrollElement: () => (
    homeViewActive.value
    && authStore.isAuthenticated
    && activeTab.value === 'following'
    && isVirtualFeedRendered.value
      ? feedPanelRef.value
      : null
  ),
  estimateSize: () => HOME_POST_ESTIMATE_PX,
  getItemKey: (index: number) => followingVirtualRows.value[index]?.key ?? index,
  enabled: true,
  useCachedMeasurements: !homeViewActive.value
    || !authStore.isAuthenticated
    || activeTab.value !== 'following'
    || !isVirtualFeedRendered.value,
  overscan: HOME_VIRTUAL_OVERSCAN,
}));

const forYouVirtualizer = useVirtualizer<HTMLElement, HTMLElement>(forYouVirtualizerOptions);
const followingVirtualizer = useVirtualizer<HTMLElement, HTMLElement>(followingVirtualizerOptions);

const virtualizerForTab = (tab: FeedTab): HomeVirtualizer => (
  tab === 'for-you'
    ? forYouVirtualizer.value
    : followingVirtualizer.value
);

const feedClockNow = () => (
  typeof performance !== 'undefined' && typeof performance.now === 'function'
    ? performance.now()
    : Date.now()
);

const clearFeedUserScrollIntent = () => {
  lastUserScrollIntentAt = Number.NEGATIVE_INFINITY;
};

const markFeedUserScrollIntent = () => {
  lastUserScrollIntentAt = feedClockNow();
};

const hasRecentFeedUserScrollIntent = () => (
  feedClockNow() - lastUserScrollIntentAt < HOME_USER_SCROLL_INTENT_WINDOW_MS
);

const setFeedTopPinned = (tab: FeedTab, pinned: boolean) => {
  if (pinned) {
    clearFeedUserScrollIntent();
  }
  if (feedTopPinned[tab] === pinned) {
    return;
  }

  feedTopPinned[tab] = pinned;
  virtualizerForTab(tab).shouldAdjustScrollPositionOnItemSizeChange = pinned
    ? () => false
    : undefined;
};

const virtualItemsWithInitialFallback = (
  virtualItems: VirtualItem[],
  rowCount: number,
  firstRowKey: string | undefined,
  estimateSize: number,
) => {
  if (virtualItems.length > 0 || rowCount === 0 || firstRowKey === undefined) {
    return virtualItems;
  }
  return [{
    key: firstRowKey,
    index: 0,
    start: 0,
    end: estimateSize,
    size: estimateSize,
    lane: 0,
  }];
};

const forYouVirtualItems = computed(() => virtualItemsWithInitialFallback(
  forYouVirtualizer.value.getVirtualItems(),
  forYouVirtualRows.value.length,
  forYouVirtualRows.value[0]?.key,
  HOME_POST_ESTIMATE_PX,
));
const followingVirtualItems = computed(() => virtualItemsWithInitialFallback(
  followingVirtualizer.value.getVirtualItems(),
  followingVirtualRows.value.length,
  followingVirtualRows.value[0]?.key,
  HOME_POST_ESTIMATE_PX,
));
const forYouVirtualizerTotalSize = computed(() => forYouVirtualizer.value.getTotalSize());
const followingVirtualizerTotalSize = computed(() => followingVirtualizer.value.getTotalSize());

const virtualRowStyle = (virtualItem: VirtualItem) => ({
  transform: `translateY(${virtualItem.start}px)`,
});

const forYouRowKind = (index: number) => forYouVirtualRows.value[index]?.kind;

const forYouPostForRow = (index: number): FeedPost => {
  const row = forYouVirtualRows.value[index];
  if (row?.kind === 'recent') {
    return row.post;
  }
  if (row?.kind === 'recommendation') {
    return row.item.post;
  }
  throw new Error(`Unexpected For You row at index ${index}`);
};

const forYouRecommendationForRow = (index: number): HomeRecommendationItem => {
  const row = forYouVirtualRows.value[index];
  if (row?.kind === 'recommendation') {
    return row.item;
  }
  throw new Error(`Unexpected recommendation row at index ${index}`);
};

const forYouInlineStateForRow = (index: number): 'loading' | 'error' => {
  const row = forYouVirtualRows.value[index];
  if (row?.kind === 'inline-state') {
    return row.state;
  }
  throw new Error(`Unexpected inline state row at index ${index}`);
};

const followingPostForRow = (index: number): FeedPost => {
  const row = followingVirtualRows.value[index];
  if (row) {
    return row.post;
  }
  throw new Error(`Unexpected Following row at index ${index}`);
};

const followingRowKind = (index: number) => followingVirtualRows.value[index]?.kind;

const measureVirtualRow = (element: Element | ComponentPublicInstance | null, virtualizer: HomeVirtualizer) => {
  if (element === null) {
    virtualizer.measureElement(null);
    return;
  }
  if (element instanceof HTMLElement) {
    virtualizer.measureElement(element);
  }
};

const measureForYouRow = (element: Element | ComponentPublicInstance | null) => {
  measureVirtualRow(element, forYouVirtualizer.value);
};

const measureFollowingRow = (element: Element | ComponentPublicInstance | null) => {
  measureVirtualRow(element, followingVirtualizer.value);
};

const canDeletePost = (post: FeedPost) =>
  authStore.isAuthenticated
  && currentViewerID() !== null
  && post.author.id === currentViewerID();

const readResizeEntryWidth = (entry: ResizeObserverEntry) => {
  const borderBoxSize = Array.isArray(entry.borderBoxSize)
    ? entry.borderBoxSize[0]
    : entry.borderBoxSize;
  const width = borderBoxSize?.inlineSize
    ?? entry.contentRect?.width
    ?? feedPanelRef.value?.clientWidth
    ?? 0;
  return Math.round(width);
};

const handleFeedPanelResize = (entries: ResizeObserverEntry[]) => {
  const width = entries[0] ? readResizeEntryWidth(entries[0]) : 0;
  if (width <= 0) {
    return;
  }
  if (lastMeasuredFeedPanelWidth === null) {
    lastMeasuredFeedPanelWidth = width;
    return;
  }
  if (lastMeasuredFeedPanelWidth === width) {
    return;
  }

  lastMeasuredFeedPanelWidth = width;
  forYouVirtualizer.value.measure();
  followingVirtualizer.value.measure();
};

const disconnectFeedPanelResizeObserver = () => {
  feedPanelResizeObserver?.disconnect();
  feedPanelResizeObserver = null;
};

const observeFeedPanelResize = () => {
  disconnectFeedPanelResizeObserver();
  if (typeof ResizeObserver === 'undefined' || !feedPanelRef.value) {
    return;
  }

  const initialWidth = Math.round(feedPanelRef.value.clientWidth);
  if (initialWidth > 0 && lastMeasuredFeedPanelWidth === null) {
    lastMeasuredFeedPanelWidth = initialWidth;
  }

  feedPanelResizeObserver = new ResizeObserver(handleFeedPanelResize);
  feedPanelResizeObserver.observe(feedPanelRef.value);
};

const saveCurrentScroll = (tab: FeedTab) => {
  const panel = feedPanelRef.value;
  if (!panel) {
    return;
  }

  homeTimeline.setScrollTop(tab, panel.scrollTop);
};

const restoreScroll = async (tab: FeedTab) => {
  await nextTick();

  if (!homeViewActive.value || activeTab.value !== tab) {
    return;
  }

  const panel = feedPanelRef.value;
  if (!panel) {
    return;
  }

  const virtualizer = virtualizerForTab(tab);
  const target = homeTimeline.scrollTop[tab];
  virtualizer.scrollToOffset(target, { align: 'start', behavior: 'auto' });
  if (Math.abs(panel.scrollTop - target) > 1) {
    panel.scrollTop = target;
  }
};

const enforcePinnedFeedTop = (tab: FeedTab) => {
  const panel = feedPanelRef.value;
  if (
    !panel
    || !homeViewActive.value
    || activeTab.value !== tab
    || !feedTopPinned[tab]
  ) {
    return;
  }

  if (panel.scrollTop === 0) {
    homeTimeline.setScrollTop(tab, 0);
    return;
  }

  correctingPinnedScroll = true;
  try {
    const virtualizer = virtualizerForTab(tab);
    virtualizer.scrollToOffset(0, { align: 'start', behavior: 'auto' });
    panel.scrollTop = 0;
    homeTimeline.setScrollTop(tab, 0);
  } finally {
    correctingPinnedScroll = false;
  }
};

const handleFeedUserScrollIntent = () => {
  markFeedUserScrollIntent();
};

const handleFeedKeydown = (event: KeyboardEvent) => {
  if (HOME_SCROLL_INTENT_KEYS.has(event.key)) {
    markFeedUserScrollIntent();
  }
};

const handleFeedScroll = () => {
  if (!homeViewActive.value) {
    return;
  }

  recommendationTelemetry.notifyViewportChange();

  if (correctingPinnedScroll) {
    return;
  }

  const panel = feedPanelRef.value;
  const tab = activeTab.value;
  if (!panel || !feedTopPinned[tab]) {
    return;
  }

  if (panel.scrollTop <= HOME_RESELECT_TOP_THRESHOLD_PX) {
    return;
  }

  if (hasRecentFeedUserScrollIntent()) {
    setFeedTopPinned(tab, false);
    return;
  }

  enforcePinnedFeedTop(tab);
};

onBeforeRouteLeave(() => {
  saveCurrentScroll(activeTab.value);
});

const selectTab = (tab: FeedTab) => {
  if (activeTab.value === tab) {
    return;
  }
  saveCurrentScroll(activeTab.value);
  homeTimeline.setActiveTab(tab);
  void router.push({
    name: 'Home',
    query: tab === 'following' ? { tab } : {},
  });
};

const reselectTab = (tab: FeedTab) => {
  if (tab !== activeTab.value) {
    return;
  }

  homeTimeline.requestHomeReselect();
};

const normalizeRouteTab = (value: unknown): FeedTab => {
  const tab: FeedTab = value === 'following' ? 'following' : 'for-you';
  homeTimeline.setActiveTab(tab);
  if (value === undefined || value === 'for-you' || value === 'following') {
    return tab;
  }
  void router.replace({
    name: 'Home',
    query: { tab: 'for-you' },
  });
  return tab;
};

const disconnectFollowingObserver = () => {
  followingObserver?.disconnect();
  followingObserver = null;
};

const disconnectForYouObserver = () => {
  forYouObserver?.disconnect();
  forYouObserver = null;
};

const updateForYouObserver = () => {
  if (!homeViewActive.value) {
    return;
  }
  disconnectForYouObserver();
  const root = feedPanelRef.value;
  if (!root) {
    return;
  }
  if (
    !forYouIntersectionObserverAvailable
    || activeTab.value !== 'for-you'
    || !forYouSentinelRef.value
    || !authStore.isAuthenticated
    || !forYouFeed.loaded
    || forYouFeed.loading
    || forYouFeed.loadingMore
    || forYouFeed.loadMoreError
    || forYouFeed.depleted
  ) {
    return;
  }

  forYouObserver = new IntersectionObserver((entries) => {
    if (homeViewActive.value && entries.some((entry) => entry.isIntersecting)) {
      void homeTimeline.loadMoreForYou();
    }
  }, { root, rootMargin: '800px 0px' });
  forYouObserver.observe(forYouSentinelRef.value);
};

const updateFollowingObserver = () => {
  if (!homeViewActive.value) {
    return;
  }
  disconnectFollowingObserver();
  const root = feedPanelRef.value;
  if (!root) {
    return;
  }
  if (
    !followingIntersectionObserverAvailable
    || activeTab.value !== 'following'
    || !followingSentinelRef.value
    || !followingFeed.nextCursor
    || followingFeed.loadingMore
    || followingFeed.stale
    || followingFeed.revalidating
    || followingFeed.loadMoreError
    || !authStore.isAuthenticated
  ) {
    return;
  }

  followingObserver = new IntersectionObserver((entries) => {
    if (homeViewActive.value && entries.some((entry) => entry.isIntersecting)) {
      void homeTimeline.loadMoreFollowing();
    }
  }, { root, rootMargin: '240px 0px' });
  followingObserver.observe(followingSentinelRef.value);
};

const bindCurrentRecommendationCards = async () => {
  await nextTick();
  if (!homeViewActive.value || !authStore.isAuthenticated || activeTab.value !== 'for-you') {
    return;
  }
  visibleForYouItems.value.forEach((item) => {
    const element = recommendationCardElements.get(item.recommendation.post.id);
    if (element) {
      recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
    }
  });
};

const pauseRecommendationObservation = () => {
  recommendationTelemetry.resetObservedCards();
  void recommendationTelemetry.flush(false);
};

const resetRecommendationObservation = () => {
  pauseRecommendationObservation();
  recommendationCardElements.clear();
};

const loadForYou = async (force = false) => {
  if (force) {
    rotateHomeViewSession('for-you');
  }
  await homeTimeline.loadForYou(force);
  await bindCurrentRecommendationCards();
};

const loadFollowing = async (force = false) => {
  if (force) {
    rotateHomeViewSession('following');
  }
  if (!force && followingFeed.loaded && followingFeed.stale) {
    await homeTimeline.revalidateFollowing();
  } else {
    await homeTimeline.loadFollowing(force);
  }
  await nextTick(updateFollowingObserver);
};

const loadMoreFollowing = () => {
  void homeTimeline.loadMoreFollowing();
};

const loadMoreForYou = () => {
  void homeTimeline.loadMoreForYou();
};

const retryForYouLoadMore = () => {
  homeTimeline.retryForYouLoadMore();
};

const retryFollowingLoadMore = () => {
  homeTimeline.retryFollowingLoadMore();
};

const loadActiveFeed = (tab: FeedTab = activeTab.value) => {
  if (tab === 'for-you') {
    void loadForYou();
  } else {
    void loadFollowing();
  }
};

const retryActiveFeed = () => {
  if (activeTab.value === 'for-you') {
    void loadForYou(true);
  } else {
    void loadFollowing(true);
  }
};

const initializeColdHomeReloadAtTop = () => {
  if (
    !initialHomeDocumentReload
    || !homeViewActive.value
    || activeTab.value !== initialHomeDocumentReloadTab
  ) {
    return;
  }

  setFeedTopPinned(initialHomeDocumentReloadTab, true);
  homeTimeline.setScrollTop(initialHomeDocumentReloadTab, 0);
  enforcePinnedFeedTop(initialHomeDocumentReloadTab);
};

const prefersReducedMotion = () => typeof window !== 'undefined'
  && typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const nextAnimationFrame = () => new Promise<void>((resolve) => {
  if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
    queueMicrotask(resolve);
    return;
  }
  window.requestAnimationFrame(() => resolve());
});

const prepareActiveFeedRefreshAtTop = (tab: FeedTab) => {
  setFeedTopPinned(tab, true);
  homeTimeline.setScrollTop(tab, 0);

  if (!homeViewActive.value || activeTab.value !== tab) {
    return;
  }

  const panel = feedPanelRef.value;
  if (!panel) {
    return;
  }

  const virtualizer = virtualizerForTab(tab);
  virtualizer.scrollToOffset(0, { align: 'start', behavior: 'auto' });
  if (panel.scrollTop !== 0) {
    panel.scrollTop = 0;
  }
};

const finishActiveFeedRefreshAtTop = async (tab: FeedTab) => {
  await nextTick();

  if (!homeViewActive.value || activeTab.value !== tab) {
    return;
  }

  const panel = feedPanelRef.value;
  if (!panel) {
    return;
  }

  const virtualizer = virtualizerForTab(tab);
  virtualizer.measure();

  homeTimeline.setScrollTop(tab, 0);
  virtualizer.scrollToOffset(0, { align: 'start', behavior: 'auto' });
  panel.scrollTop = 0;

  await nextAnimationFrame();

  if (
    !homeViewActive.value
    || activeTab.value !== tab
    || !feedTopPinned[tab]
  ) {
    return;
  }

  virtualizer.scrollToOffset(0, { align: 'start', behavior: 'auto' });
  panel.scrollTop = 0;
  homeTimeline.setScrollTop(tab, 0);
};

const refreshActiveFeed = async () => {
  if (!authStore.isAuthenticated) {
    return;
  }

  const tab = activeTab.value;

  if (tab === 'for-you') {
    if (forYouFeed.loading || forYouFeed.loadingMore) {
      return;
    }

    prepareActiveFeedRefreshAtTop(tab);
    try {
      await loadForYou(true);
    } finally {
      await finishActiveFeedRefreshAtTop(tab);
    }
    return;
  }

  if (
    followingFeed.loading
    || followingFeed.loadingMore
    || followingFeed.revalidating
  ) {
    return;
  }

  prepareActiveFeedRefreshAtTop(tab);
  try {
    await loadFollowing(true);
  } finally {
    await finishActiveFeedRefreshAtTop(tab);
  }
};

const handleHomeReselect = async () => {
  if (!homeViewActive.value || route.name !== 'Home') {
    return;
  }

  const panel = feedPanelRef.value;
  if (!panel) {
    return;
  }

  if (panel.scrollTop > HOME_RESELECT_TOP_THRESHOLD_PX) {
    panel.scrollTo({
      top: 0,
      behavior: prefersReducedMotion() ? 'auto' : 'smooth',
    });
    homeTimeline.setScrollTop(activeTab.value, 0);
    return;
  }

  await refreshActiveFeed();
};

const bindRecommendationCard = (
  element: Element | ComponentPublicInstance | null,
  item: { recommendation: RecommendedPost },
) => {
  if (element instanceof HTMLElement) {
    recommendationCardElements.set(item.recommendation.post.id, element);
    if (homeViewActive.value) {
      recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
    }
    return;
  }

  recommendationCardElements.delete(item.recommendation.post.id);
  if (!homeViewActive.value) {
    return;
  }
  recommendationTelemetry.detachFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  queueMicrotask(() => {
    if (!homeViewActive.value || recommendationCardElements.has(item.recommendation.post.id)) {
      return;
    }
    const stillRendered = visibleForYouItems.value.some(
      visibleItem => visibleItem.recommendation.post.id === item.recommendation.post.id,
    );
    if (!stillRendered) {
      recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
    }
  });
};

type RecommendationCardRef = (
  element: Element | ComponentPublicInstance | null,
) => void;

const recommendationCardRefForRow = (index: number): RecommendationCardRef => {
  const item = forYouRecommendationForRow(index);

  return (element) => {
    bindRecommendationCard(element, item);
  };
};

const handleRecommendationClick = (recommendation: RecommendedPost) => {
  savePendingRecommendationAttribution(recommendation.post.id, recommendation.tracking);
  recommendationTelemetry.recordClick(recommendation.post.id, recommendation.tracking);
};

const handleLikeToggle = async (postId: number) => {
  const result = await homeTimeline.toggleLike(postId);
  if (result === 'failed') {
    ElMessage.error('Couldn’t update your like. Try again.');
  }
};

const handleRepostToggle = async (postId: number) => {
  const result = await homeTimeline.toggleRepost(postId);
  if (result === 'failed') {
    ElMessage.error('Couldn’t update your repost. Try again.');
  }
};

const handleBookmarkToggle = async (postId: number) => {
  const result = await homeTimeline.toggleBookmark(postId);
  if (result === 'failed') {
    ElMessage.error('Couldn’t update your bookmark. Try again.');
  }
};

const handleDeletePost = async (postId: number) => {
  const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
  if (item) {
    recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  }
  await homeTimeline.deletePost(postId);
};

const handleNotInterested = (postId: number) => {
  const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
  if (!item) {
    return;
  }
  recommendationTelemetry.recordNotInterested(item.recommendation.post.id, item.recommendation.tracking);
  recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
  homeTimeline.dismissRecommendation(postId);
  recommendationCardElements.delete(postId);
};

watch(
  () => route.query.tab,
  (tab) => {
    if (!homeViewActive.value || route.name !== 'Home') {
      return;
    }
    normalizeRouteTab(tab);
  },
  { immediate: true },
);

watch(
  activeTab,
  (tab, previousTab) => {
    if (!homeViewActive.value) {
      return;
    }
    if (previousTab && previousTab !== tab) {
      setFeedTopPinned(previousTab, false);
      saveCurrentScroll(previousTab);
      if (previousTab === 'for-you') {
        resetRecommendationObservation();
        disconnectForYouObserver();
      } else {
        disconnectFollowingObserver();
      }
    }
    loadActiveFeed(tab);
    restoreScroll(tab);
  },
  { immediate: true, flush: 'sync' },
);

watch(
  () => homeTimeline.homeReselectVersion,
  () => {
    void handleHomeReselect();
  },
);

watch(
  [
    activeTab,
    () => followingFeed.nextCursor,
    () => followingFeed.loadingMore,
    () => followingFeed.loadMoreError,
    () => followingFeed.loading,
    () => followingFeed.stale,
    () => followingFeed.revalidating,
  ],
  () => {
    if (!homeViewActive.value) {
      return;
    }
    void nextTick(updateFollowingObserver);
  },
  { flush: 'post' },
);

watch(
  [
    activeTab,
    () => forYouFeed.items.length,
    () => forYouFeed.loaded,
    () => forYouFeed.loading,
    () => forYouFeed.loadingMore,
    () => forYouFeed.loadMoreError,
    () => forYouFeed.depleted,
    () => authStore.isAuthenticated,
  ],
  () => {
    if (!homeViewActive.value) {
      return;
    }
    void nextTick(updateForYouObserver);
  },
  { flush: 'post', immediate: true },
);

watch(
  () => forYouFeed.items.map((item) => item.recommendation.post.id).join(','),
  () => {
    void bindCurrentRecommendationCards();
  },
  { flush: 'post' },
);

watch(
  [() => currentViewerID(), () => authStore.isAuthenticated],
  ([viewerID, isAuthenticated]) => {
    if (viewerID === lastHomeViewerID && isAuthenticated === lastHomeAuthenticated) {
      return;
    }
    releaseHomeViewSessionsForViewer(lastHomeViewerID);
    homeViewSessionVersion['for-you'] += 1;
    homeViewSessionVersion.following += 1;
    lastHomeViewerID = viewerID;
    lastHomeAuthenticated = isAuthenticated;
  },
);

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated) => {
    if (isAuthenticated && homeViewActive.value) {
      loadActiveFeed();
    }
  },
  { immediate: true },
);

onMounted(() => {
  initializeColdHomeReloadAtTop();
  observeFeedPanelResize();
});

onDeactivated(() => {
  if (!homeViewActive.value) {
    return;
  }
  setFeedTopPinned('for-you', false);
  setFeedTopPinned('following', false);
  homeViewActive.value = false;
  disconnectFeedPanelResizeObserver();
  resumeOnActivation = true;
  disconnectForYouObserver();
  disconnectFollowingObserver();
  pauseRecommendationObservation();
});

onActivated(() => {
  observeFeedPanelResize();
  if (!resumeOnActivation) {
    return;
  }
  resumeOnActivation = false;
  homeViewActive.value = true;

  const previousTab = activeTab.value;
  const tab = normalizeRouteTab(route.query.tab);
  if (previousTab === tab) {
    loadActiveFeed(tab);
    void restoreScroll(tab);
  }

  void nextTick(() => {
    if (!homeViewActive.value) {
      return;
    }
    updateForYouObserver();
    updateFollowingObserver();
    void bindCurrentRecommendationCards();
  });
});

onBeforeUnmount(() => {
  setFeedTopPinned('for-you', false);
  setFeedTopPinned('following', false);
  disconnectFeedPanelResizeObserver();
  if (homeViewActive.value) {
    const tab = activeTab.value;
    saveCurrentScroll(tab);
    disconnectForYouObserver();
    disconnectFollowingObserver();
    pauseRecommendationObservation();
  }
  homeViewActive.value = false;
  resumeOnActivation = false;
  releaseHomeViewSessionsForViewer(lastHomeViewerID);
  recommendationCardElements.clear();
});
</script>

<style scoped>
.home-view {
  display: flex;
  width: 100%;
  height: 100vh;
  height: 100dvh;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  background: var(--color-surface);
  color: var(--color-text);
}

.home-feed-header {
  position: relative;
  z-index: 20;
  flex: 0 0 auto;
  border-bottom: 1px solid var(--color-border);
  background: color-mix(in srgb, var(--color-surface) 94%, transparent);
  backdrop-filter: blur(10px);
}

.home-feed-header__content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
}

.home-feed-header h1 {
  margin: 0;
  font-size: 22px;
  letter-spacing: -0.02em;
}

.home-compose-fab {
  display: none;
}

.home-feed-body {
  min-width: 0;
}

.home-feed-panel {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  overflow-anchor: none;
  -webkit-overflow-scrolling: touch;
}

.feed-list {
  min-width: 0;
}

.home-virtual-list {
  position: relative;
  width: 100%;
}

.home-virtual-row {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  min-width: 0;
}

.recommendation-card-wrapper {
  min-width: 0;
}

.home-state {
  display: grid;
  justify-items: center;
  gap: var(--space-5);
  max-width: 480px;
  margin: 0 auto;
  padding: 72px var(--space-5);
  text-align: center;
}

.home-state h2 {
  margin: 0;
  color: var(--color-text);
  font-size: 24px;
  line-height: 1.25;
}

.home-state__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--space-3);
}

.home-state__primary,
.home-state__secondary {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-pill);
  padding: 0 var(--space-5);
  font-size: 14px;
  font-weight: 750;
  text-decoration: none;
}

.home-state__primary {
  border: 1px solid var(--color-accent);
  background: var(--color-accent);
  color: #fff;
}

.home-state__secondary {
  border: 1px solid var(--color-border-strong);
  background: var(--color-surface);
  color: var(--color-text);
}

.home-state__primary:focus-visible,
.home-state__secondary:focus-visible {
  border-color: var(--color-accent);
}

@media (hover: hover) and (pointer: fine) {
  .home-state__primary:hover,
  .home-state__secondary:hover {
    border-color: var(--color-accent);
  }
}

.feed-skeleton {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
}

.feed-skeleton span {
  display: block;
  height: 12px;
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
  animation: feed-shimmer 1.2s ease-in-out infinite;
}

.feed-skeleton__author {
  width: 132px;
}

.feed-skeleton__title {
  width: 74%;
  height: 20px !important;
}

.feed-skeleton__line {
  width: 92%;
}

.feed-skeleton__line--short {
  width: 58% !important;
}

@keyframes feed-shimmer {
  0%,
  100% {
    opacity: 0.55;
  }
  50% {
    opacity: 1;
  }
}

.home-feed-inline-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: 56px;
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

.home-feed-sentinel {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  min-height: 64px;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-secondary);
  font-size: 13px;
}

@media (max-width: 620px) {
  .home-state {
    padding-inline: var(--space-4);
  }
}

@media (max-width: 799px) {
  .home-view {
    height: calc(
      100vh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );

    height: calc(
      100dvh
      - var(--mobile-safe-top)
      - var(--mobile-bottom-nav-height)
      - var(--mobile-safe-bottom)
    );
  }

  .home-feed-header {
    padding: 0;
  }

  .home-feed-header__content {
    display: block;
    padding: 0;
  }

  .home-feed-header h1 {
    display: none;
  }

  .home-compose-fab {
    position: fixed;
    right: 16px;
    bottom: calc(
      var(--mobile-bottom-nav-height)
      + var(--mobile-safe-bottom)
      + 16px
    );
    z-index: 30;
    display: grid;
    width: var(--mobile-fab-size);
    height: var(--mobile-fab-size);
    place-items: center;
    border-radius: 50%;
    background: var(--color-accent);
    color: var(--color-surface);
    text-decoration: none;
    box-shadow: 0 6px 18px color-mix(in srgb, var(--color-text) 18%, transparent);
    transition: background-color var(--transition-fast), transform var(--transition-fast);
  }

  .home-compose-fab:focus-visible {
    background: var(--color-accent-hover);
  }

  @media (hover: hover) and (pointer: fine) {
    .home-compose-fab:hover {
      background: var(--color-accent-hover);
    }
  }

  .home-compose-fab:active {
    transform: scale(0.96);
  }
}

@media (prefers-reduced-motion: reduce) {
  .feed-skeleton span {
    animation: none;
  }
}
</style>
