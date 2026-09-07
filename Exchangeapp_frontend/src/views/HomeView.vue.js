/* __placeholder__ */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import FeedTabs from '../components/feed/FeedTabs.vue';
import PostCard from '../components/feed/PostCard.vue';
import AppIcon from '../components/icons/AppIcon.vue';
import MobileHomeHeader from '../components/layout/MobileHomeHeader.vue';
import { savePendingRecommendationAttribution } from '../services/recommendationAttribution';
import { getRecommendationTelemetry } from '../services/recommendationTelemetry';
import { useAuthStore } from '../store/auth';
import { useFeedStore } from '../store/feed';
import { useHomeTimelineStore } from '../store/homeTimeline';
const { defineProps, defineSlots, defineEmits, defineExpose, defineModel, defineOptions, withDefaults, } = await import('vue');
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();
const feedStore = useFeedStore();
const homeTimeline = useHomeTimelineStore();
const recommendationTelemetry = getRecommendationTelemetry(() => authStore.token);
const skeletonPosts = [0, 1, 2];
const recommendationCardElements = new Map();
const forYouSentinelRef = ref(null);
const forYouIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let forYouObserver = null;
const followingSentinelRef = ref(null);
const followingIntersectionObserverAvailable = typeof IntersectionObserver !== 'undefined';
let followingObserver = null;
const forYouFeed = homeTimeline.forYou;
const followingFeed = homeTimeline.following;
const likePendingPostIds = homeTimeline.likePendingPostIds;
const repostPendingPostIds = homeTimeline.repostPendingPostIds;
const pendingDeletePostIds = homeTimeline.pendingDeletePostIds;
const deleteErrors = homeTimeline.deleteErrors;
const activeTab = computed(() => homeTimeline.activeTab);
const activeFeedStatus = computed(() => {
    const state = activeTab.value === 'for-you' ? forYouFeed : followingFeed;
    return {
        loading: state.loading || (!state.loaded && !state.error),
        error: state.error,
        empty: state.loaded && !state.loading && !state.error && state.items.length === 0,
    };
});
const hasRecentlyPublishedPosts = computed(() => activeTab.value === 'for-you' && feedStore.recentlyPublishedPosts.length > 0);
const recentlyPublishedIDs = computed(() => new Set(feedStore.recentlyPublishedPosts.map((post) => post.id)));
const visibleForYouItems = computed(() => forYouFeed.items.filter((item) => !recentlyPublishedIDs.value.has(item.post.id)
    && !feedStore.isPostDeleted(item.post.id)));
const currentViewerID = () => {
    const id = authStore.currentIdentity?.id;
    return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 ? id : null;
};
const canDeletePost = (post) => authStore.isAuthenticated
    && currentViewerID() !== null
    && post.author.id === currentViewerID();
const saveCurrentScroll = (tab) => {
    if (typeof window !== 'undefined') {
        homeTimeline.setScrollY(tab, window.scrollY);
    }
};
const restoreScroll = (tab) => {
    void nextTick(() => {
        const state = tab === 'for-you' ? forYouFeed : followingFeed;
        if (!state.loaded || typeof window === 'undefined') {
            return;
        }
        if (typeof window.scrollTo === 'function'
            && !window.navigator.userAgent.toLowerCase().includes('jsdom')) {
            window.scrollTo({ top: homeTimeline.scrollY[tab], behavior: 'auto' });
        }
    });
};
const selectTab = (tab) => {
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
const normalizeRouteTab = (value) => {
    const tab = value === 'following' ? 'following' : 'for-you';
    homeTimeline.setActiveTab(tab);
    if (value === undefined || value === 'for-you' || value === 'following') {
        return;
    }
    void router.replace({
        name: 'Home',
        query: { tab: 'for-you' },
    });
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
    disconnectForYouObserver();
    if (!forYouIntersectionObserverAvailable
        || activeTab.value !== 'for-you'
        || !forYouSentinelRef.value
        || !authStore.isAuthenticated
        || !forYouFeed.loaded
        || forYouFeed.loading
        || forYouFeed.loadingMore
        || forYouFeed.loadMoreError
        || forYouFeed.depleted) {
        return;
    }
    forYouObserver = new IntersectionObserver((entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
            void homeTimeline.loadMoreForYou();
        }
    }, { rootMargin: '800px 0px' });
    forYouObserver.observe(forYouSentinelRef.value);
};
const updateFollowingObserver = () => {
    disconnectFollowingObserver();
    if (!followingIntersectionObserverAvailable
        || activeTab.value !== 'following'
        || !followingSentinelRef.value
        || !followingFeed.nextCursor
        || followingFeed.loadingMore
        || followingFeed.stale
        || followingFeed.revalidating
        || followingFeed.loadMoreError
        || !authStore.isAuthenticated) {
        return;
    }
    followingObserver = new IntersectionObserver((entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
            void homeTimeline.loadMoreFollowing();
        }
    }, { rootMargin: '240px 0px' });
    followingObserver.observe(followingSentinelRef.value);
};
const bindCurrentRecommendationCards = async () => {
    await nextTick();
    if (!authStore.isAuthenticated || activeTab.value !== 'for-you') {
        return;
    }
    visibleForYouItems.value.forEach((item) => {
        const element = recommendationCardElements.get(item.recommendation.post.id);
        if (element) {
            recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
        }
    });
};
const resetRecommendationObservation = () => {
    recommendationTelemetry.resetObservedCards();
    void recommendationTelemetry.flush(false);
    recommendationCardElements.clear();
};
const loadForYou = async (force = false) => {
    await homeTimeline.loadForYou(force);
    await bindCurrentRecommendationCards();
};
const loadFollowing = async (force = false) => {
    if (!force && followingFeed.loaded && followingFeed.stale) {
        await homeTimeline.revalidateFollowing();
    }
    else {
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
const loadActiveFeed = (tab = activeTab.value) => {
    if (tab === 'for-you') {
        void loadForYou();
    }
    else {
        void loadFollowing();
    }
};
const retryActiveFeed = () => {
    if (activeTab.value === 'for-you') {
        void loadForYou(true);
    }
    else {
        void loadFollowing(true);
    }
};
const bindRecommendationCard = (element, item) => {
    if (element instanceof HTMLElement) {
        recommendationCardElements.set(item.recommendation.post.id, element);
        recommendationTelemetry.observeFeedCard(element, item.recommendation.post.id, item.recommendation.tracking);
        return;
    }
    recommendationCardElements.delete(item.recommendation.post.id);
    recommendationTelemetry.detachFeedCard(item.recommendation.post.id, item.recommendation.tracking);
    queueMicrotask(() => {
        if (recommendationCardElements.has(item.recommendation.post.id)) {
            return;
        }
        const stillRendered = visibleForYouItems.value.some(visibleItem => visibleItem.recommendation.post.id === item.recommendation.post.id);
        if (!stillRendered) {
            recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
        }
    });
};
const handleRecommendationClick = (recommendation) => {
    savePendingRecommendationAttribution(recommendation.post.id, recommendation.tracking);
    recommendationTelemetry.recordClick(recommendation.post.id, recommendation.tracking);
};
const handleLikeToggle = (postId) => {
    void homeTimeline.toggleLike(postId);
};
const handleRepostToggle = (postId) => {
    void homeTimeline.toggleRepost(postId);
};
const handleDeletePost = async (postId) => {
    const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
    if (item) {
        recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
    }
    await homeTimeline.deletePost(postId);
};
const handleNotInterested = (postId) => {
    const item = forYouFeed.items.find((candidate) => candidate.recommendation.post.id === postId);
    if (!item) {
        return;
    }
    recommendationTelemetry.recordNotInterested(item.recommendation.post.id, item.recommendation.tracking);
    recommendationTelemetry.unobserveFeedCard(item.recommendation.post.id, item.recommendation.tracking);
    homeTimeline.dismissRecommendation(postId);
    recommendationCardElements.delete(postId);
};
watch(() => route.query.tab, normalizeRouteTab, { immediate: true });
watch(activeTab, (tab, previousTab) => {
    if (previousTab && previousTab !== tab) {
        saveCurrentScroll(previousTab);
        if (previousTab === 'for-you') {
            resetRecommendationObservation();
            disconnectForYouObserver();
        }
        else {
            disconnectFollowingObserver();
        }
    }
    loadActiveFeed(tab);
    restoreScroll(tab);
}, { immediate: true });
watch([
    activeTab,
    () => followingFeed.nextCursor,
    () => followingFeed.loadingMore,
    () => followingFeed.loadMoreError,
    () => followingFeed.loading,
    () => followingFeed.stale,
    () => followingFeed.revalidating,
], () => {
    void nextTick(updateFollowingObserver);
}, { flush: 'post' });
watch([
    activeTab,
    () => forYouFeed.items.length,
    () => forYouFeed.loaded,
    () => forYouFeed.loading,
    () => forYouFeed.loadingMore,
    () => forYouFeed.loadMoreError,
    () => forYouFeed.depleted,
    () => authStore.isAuthenticated,
], () => {
    void nextTick(updateForYouObserver);
}, { flush: 'post', immediate: true });
watch(() => forYouFeed.items.map((item) => item.recommendation.post.id).join(','), () => {
    void bindCurrentRecommendationCards();
}, { flush: 'post' });
watch(() => authStore.isAuthenticated, (isAuthenticated) => {
    if (isAuthenticated) {
        loadActiveFeed();
    }
}, { immediate: true });
onBeforeUnmount(() => {
    saveCurrentScroll(activeTab.value);
    disconnectForYouObserver();
    disconnectFollowingObserver();
    resetRecommendationObservation();
});
const __VLS_fnComponent = (await import('vue')).defineComponent({});
let __VLS_functionalComponentProps;
let __VLS_modelEmitsType;
function __VLS_template() {
    let __VLS_ctx;
    /* Components */
    let __VLS_otherComponents;
    let __VLS_own;
    let __VLS_localComponents;
    let __VLS_components;
    let __VLS_styleScopedClasses;
    // CSS variable injection 
    // CSS variable injection end 
    let __VLS_resolvedLocalAndGlobalComponents;
    __VLS_elementAsFunction(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({ ...{ class: ("home-view") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({ ...{ class: ("home-feed-header") }, });
    // @ts-ignore
    [MobileHomeHeader,];
    const __VLS_0 = __VLS_asFunctionalComponent(MobileHomeHeader, new MobileHomeHeader({}));
    const __VLS_1 = __VLS_0({}, ...__VLS_functionalComponentArgsRest(__VLS_0));
    ({}({}));
    const __VLS_4 = __VLS_pickFunctionalComponentCtx(MobileHomeHeader, __VLS_1);
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("home-feed-header__content") }, });
    __VLS_elementAsFunction(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
    // @ts-ignore
    [FeedTabs,];
    const __VLS_5 = __VLS_asFunctionalComponent(FeedTabs, new FeedTabs({ ...{ 'onSelect': {} }, activeTab: ((__VLS_ctx.activeTab)), }));
    const __VLS_6 = __VLS_5({ ...{ 'onSelect': {} }, activeTab: ((__VLS_ctx.activeTab)), }, ...__VLS_functionalComponentArgsRest(__VLS_5));
    ({}({ ...{ 'onSelect': {} }, activeTab: ((__VLS_ctx.activeTab)), }));
    let __VLS_10;
    const __VLS_11 = {
        onSelect: (__VLS_ctx.selectTab)
    };
    // @ts-ignore
    [activeTab, selectTab,];
    const __VLS_9 = __VLS_pickFunctionalComponentCtx(FeedTabs, __VLS_6);
    let __VLS_7;
    let __VLS_8;
    __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ id: (('feed-panel-' + __VLS_ctx.activeTab)), ...{ class: ("home-feed-panel") }, role: ("tabpanel"), tabindex: ("0"), "aria-labelledby": (('feed-tab-' + __VLS_ctx.activeTab)), });
    if (!__VLS_ctx.authStore.isAuthenticated) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("home-state home-state--auth") }, "aria-labelledby": ("home-auth-title"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({ id: ("home-auth-title"), });
        // @ts-ignore
        [activeTab, activeTab, authStore,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("home-state__actions") }, });
        const __VLS_12 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({ ...{ class: ("home-state__primary") }, to: (({ name: 'Login' })), }));
        const __VLS_14 = __VLS_13({ ...{ class: ("home-state__primary") }, to: (({ name: 'Login' })), }, ...__VLS_functionalComponentArgsRest(__VLS_13));
        ({}({ ...{ class: ("home-state__primary") }, to: (({ name: 'Login' })), }));
        (__VLS_17.slots).default;
        const __VLS_17 = __VLS_pickFunctionalComponentCtx(__VLS_12, __VLS_14);
        const __VLS_18 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_19 = __VLS_asFunctionalComponent(__VLS_18, new __VLS_18({ ...{ class: ("home-state__secondary") }, to: (({ name: 'Register' })), }));
        const __VLS_20 = __VLS_19({ ...{ class: ("home-state__secondary") }, to: (({ name: 'Register' })), }, ...__VLS_functionalComponentArgsRest(__VLS_19));
        ({}({ ...{ class: ("home-state__secondary") }, to: (({ name: 'Register' })), }));
        (__VLS_23.slots).default;
        const __VLS_23 = __VLS_pickFunctionalComponentCtx(__VLS_18, __VLS_20);
    }
    else if (__VLS_ctx.activeFeedStatus.loading && !__VLS_ctx.hasRecentlyPublishedPosts) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("feed-list feed-list--loading") }, "aria-labelledby": (('feed-tab-' + __VLS_ctx.activeTab)), });
        for (const [skeleton] of __VLS_getVForSourceType((__VLS_ctx.skeletonPosts))) {
            __VLS_elementAsFunction(__VLS_intrinsicElements.article, __VLS_intrinsicElements.article)({ key: ((skeleton)), ...{ class: ("feed-skeleton") }, "aria-hidden": ("true"), });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("feed-skeleton__author") }, });
            // @ts-ignore
            [activeTab, activeFeedStatus, hasRecentlyPublishedPosts, skeletonPosts,];
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("feed-skeleton__title") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("feed-skeleton__line") }, });
            __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({ ...{ class: ("feed-skeleton__line feed-skeleton__line--short") }, });
        }
    }
    else if (__VLS_ctx.activeFeedStatus.error && !__VLS_ctx.hasRecentlyPublishedPosts) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("home-state") }, "aria-live": ("polite"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        // @ts-ignore
        [activeFeedStatus, hasRecentlyPublishedPosts,];
        __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryActiveFeed) }, ...{ class: ("home-state__primary") }, type: ("button"), });
        // @ts-ignore
        [retryActiveFeed,];
    }
    else if (__VLS_ctx.activeFeedStatus.empty && !__VLS_ctx.hasRecentlyPublishedPosts) {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("home-state") }, "aria-live": ("polite"), });
        __VLS_elementAsFunction(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
        (__VLS_ctx.activeTab === 'for-you' ? 'No recommendations yet' : 'No posts from people you follow yet');
        // @ts-ignore
        [activeTab, activeFeedStatus, hasRecentlyPublishedPosts,];
    }
    else {
        __VLS_elementAsFunction(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({ ...{ class: ("feed-list") }, });
        if (__VLS_ctx.activeTab === 'for-you') {
            for (const [post] of __VLS_getVForSourceType((__VLS_ctx.feedStore.recentlyPublishedPosts))) {
                // @ts-ignore
                [PostCard,];
                const __VLS_24 = __VLS_asFunctionalComponent(PostCard, new PostCard({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: (('recent-' + post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                const __VLS_25 = __VLS_24({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: (('recent-' + post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }, ...__VLS_functionalComponentArgsRest(__VLS_24));
                ({}({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: (('recent-' + post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                let __VLS_29;
                const __VLS_30 = {
                    onToggleLike: (__VLS_ctx.handleLikeToggle)
                };
                const __VLS_31 = {
                    onToggleRepost: (__VLS_ctx.handleRepostToggle)
                };
                const __VLS_32 = {
                    onDeletePost: (__VLS_ctx.handleDeletePost)
                };
                // @ts-ignore
                [activeTab, feedStore, likePendingPostIds, repostPendingPostIds, canDeletePost, pendingDeletePostIds, deleteErrors, handleLikeToggle, handleRepostToggle, handleDeletePost,];
                const __VLS_28 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_25);
                let __VLS_26;
                let __VLS_27;
            }
            if (__VLS_ctx.forYouFeed.loading && __VLS_ctx.hasRecentlyPublishedPosts) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("home-feed-inline-state") }, "aria-live": ("polite"), });
                // @ts-ignore
                [hasRecentlyPublishedPosts, forYouFeed,];
            }
            else if (__VLS_ctx.forYouFeed.error && __VLS_ctx.hasRecentlyPublishedPosts) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ...{ class: ("home-feed-inline-state") }, "aria-live": ("polite"), });
                __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                // @ts-ignore
                [hasRecentlyPublishedPosts, forYouFeed,];
                __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (...[$event]) => {
                            if (!(!((!__VLS_ctx.authStore.isAuthenticated))))
                                return;
                            if (!(!((__VLS_ctx.activeFeedStatus.loading && !__VLS_ctx.hasRecentlyPublishedPosts))))
                                return;
                            if (!(!((__VLS_ctx.activeFeedStatus.error && !__VLS_ctx.hasRecentlyPublishedPosts))))
                                return;
                            if (!(!((__VLS_ctx.activeFeedStatus.empty && !__VLS_ctx.hasRecentlyPublishedPosts))))
                                return;
                            if (!((__VLS_ctx.activeTab === 'for-you')))
                                return;
                            if (!(!((__VLS_ctx.forYouFeed.loading && __VLS_ctx.hasRecentlyPublishedPosts))))
                                return;
                            if (!((__VLS_ctx.forYouFeed.error && __VLS_ctx.hasRecentlyPublishedPosts)))
                                return;
                            __VLS_ctx.loadForYou(true);
                            // @ts-ignore
                            [loadForYou,];
                        } }, ...{ class: ("home-state__primary") }, type: ("button"), });
            }
            for (const [item] of __VLS_getVForSourceType((__VLS_ctx.visibleForYouItems))) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ key: ((item.recommendation.post.id)), ...{ class: ("recommendation-card-wrapper") }, ref: ((element => __VLS_ctx.bindRecommendationCard(element, item))), });
                // @ts-ignore
                [PostCard,];
                const __VLS_33 = __VLS_asFunctionalComponent(PostCard, new PostCard({ ...{ 'onPostClick': {} }, ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onNotInterested': {} }, ...{ 'onDeletePost': {} }, post: ((item.post)), likePending: ((__VLS_ctx.likePendingPostIds.has(item.post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(item.post.id))), showNotInterested: ((true)), showDelete: ((__VLS_ctx.canDeletePost(item.post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(item.post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(item.post.id) || '')), }));
                const __VLS_34 = __VLS_33({ ...{ 'onPostClick': {} }, ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onNotInterested': {} }, ...{ 'onDeletePost': {} }, post: ((item.post)), likePending: ((__VLS_ctx.likePendingPostIds.has(item.post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(item.post.id))), showNotInterested: ((true)), showDelete: ((__VLS_ctx.canDeletePost(item.post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(item.post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(item.post.id) || '')), }, ...__VLS_functionalComponentArgsRest(__VLS_33));
                ({}({ ...{ 'onPostClick': {} }, ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onNotInterested': {} }, ...{ 'onDeletePost': {} }, post: ((item.post)), likePending: ((__VLS_ctx.likePendingPostIds.has(item.post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(item.post.id))), showNotInterested: ((true)), showDelete: ((__VLS_ctx.canDeletePost(item.post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(item.post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(item.post.id) || '')), }));
                let __VLS_38;
                const __VLS_39 = {
                    onPostClick: (...[$event]) => {
                        if (!(!((!__VLS_ctx.authStore.isAuthenticated))))
                            return;
                        if (!(!((__VLS_ctx.activeFeedStatus.loading && !__VLS_ctx.hasRecentlyPublishedPosts))))
                            return;
                        if (!(!((__VLS_ctx.activeFeedStatus.error && !__VLS_ctx.hasRecentlyPublishedPosts))))
                            return;
                        if (!(!((__VLS_ctx.activeFeedStatus.empty && !__VLS_ctx.hasRecentlyPublishedPosts))))
                            return;
                        if (!((__VLS_ctx.activeTab === 'for-you')))
                            return;
                        __VLS_ctx.handleRecommendationClick(item.recommendation);
                        // @ts-ignore
                        [likePendingPostIds, repostPendingPostIds, canDeletePost, pendingDeletePostIds, deleteErrors, visibleForYouItems, bindRecommendationCard, handleRecommendationClick,];
                    }
                };
                const __VLS_40 = {
                    onToggleLike: (__VLS_ctx.handleLikeToggle)
                };
                const __VLS_41 = {
                    onToggleRepost: (__VLS_ctx.handleRepostToggle)
                };
                const __VLS_42 = {
                    onNotInterested: (__VLS_ctx.handleNotInterested)
                };
                const __VLS_43 = {
                    onDeletePost: (__VLS_ctx.handleDeletePost)
                };
                // @ts-ignore
                [handleLikeToggle, handleRepostToggle, handleDeletePost, handleNotInterested,];
                const __VLS_37 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_34);
                let __VLS_35;
                let __VLS_36;
            }
            if (!__VLS_ctx.forYouFeed.depleted || __VLS_ctx.forYouFeed.loadingMore || __VLS_ctx.forYouFeed.loadMoreError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("forYouSentinelRef"), ...{ class: ("home-feed-sentinel") }, "aria-live": ("polite"), });
                // @ts-ignore
                (__VLS_ctx.forYouSentinelRef);
                if (__VLS_ctx.forYouFeed.loadingMore) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [forYouFeed, forYouFeed, forYouFeed, forYouFeed, forYouSentinelRef,];
                }
                else if (__VLS_ctx.forYouFeed.loadMoreError) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [forYouFeed,];
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryForYouLoadMore) }, ...{ class: ("home-state__primary") }, type: ("button"), });
                    // @ts-ignore
                    [retryForYouLoadMore,];
                }
                else if (!__VLS_ctx.forYouIntersectionObserverAvailable && !__VLS_ctx.forYouFeed.depleted) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMoreForYou) }, ...{ class: ("home-state__primary") }, type: ("button"), });
                    // @ts-ignore
                    [forYouFeed, forYouIntersectionObserverAvailable, loadMoreForYou,];
                }
            }
        }
        else {
            for (const [post] of __VLS_getVForSourceType((__VLS_ctx.followingFeed.items))) {
                // @ts-ignore
                [PostCard,];
                const __VLS_44 = __VLS_asFunctionalComponent(PostCard, new PostCard({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                const __VLS_45 = __VLS_44({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }, ...__VLS_functionalComponentArgsRest(__VLS_44));
                ({}({ ...{ 'onToggleLike': {} }, ...{ 'onToggleRepost': {} }, ...{ 'onDeletePost': {} }, key: ((post.id)), post: ((post)), likePending: ((__VLS_ctx.likePendingPostIds.has(post.id))), repostPending: ((__VLS_ctx.repostPendingPostIds.has(post.id))), showDelete: ((__VLS_ctx.canDeletePost(post))), deletePending: ((__VLS_ctx.pendingDeletePostIds.has(post.id))), deleteError: ((__VLS_ctx.deleteErrors.get(post.id) || '')), }));
                let __VLS_49;
                const __VLS_50 = {
                    onToggleLike: (__VLS_ctx.handleLikeToggle)
                };
                const __VLS_51 = {
                    onToggleRepost: (__VLS_ctx.handleRepostToggle)
                };
                const __VLS_52 = {
                    onDeletePost: (__VLS_ctx.handleDeletePost)
                };
                // @ts-ignore
                [likePendingPostIds, repostPendingPostIds, canDeletePost, pendingDeletePostIds, deleteErrors, handleLikeToggle, handleRepostToggle, handleDeletePost, followingFeed,];
                const __VLS_48 = __VLS_pickFunctionalComponentCtx(PostCard, __VLS_45);
                let __VLS_46;
                let __VLS_47;
            }
            if (__VLS_ctx.followingFeed.nextCursor || __VLS_ctx.followingFeed.loadingMore || __VLS_ctx.followingFeed.loadMoreError) {
                __VLS_elementAsFunction(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({ ref: ("followingSentinelRef"), ...{ class: ("home-feed-sentinel") }, "aria-live": ("polite"), });
                // @ts-ignore
                (__VLS_ctx.followingSentinelRef);
                if (__VLS_ctx.followingFeed.loadingMore) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [followingFeed, followingFeed, followingFeed, followingFeed, followingSentinelRef,];
                }
                else if (__VLS_ctx.followingFeed.loadMoreError) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
                    // @ts-ignore
                    [followingFeed,];
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.retryFollowingLoadMore) }, ...{ class: ("home-state__primary") }, type: ("button"), });
                    // @ts-ignore
                    [retryFollowingLoadMore,];
                }
                else if (!__VLS_ctx.followingIntersectionObserverAvailable && __VLS_ctx.followingFeed.nextCursor) {
                    __VLS_elementAsFunction(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({ ...{ onClick: (__VLS_ctx.loadMoreFollowing) }, ...{ class: ("home-state__primary") }, type: ("button"), });
                    // @ts-ignore
                    [followingFeed, followingIntersectionObserverAvailable, loadMoreFollowing,];
                }
            }
        }
    }
    if (__VLS_ctx.authStore.isAuthenticated) {
        const __VLS_53 = {}.RouterLink;
        ({}.RouterLink);
        ({}.RouterLink);
        __VLS_components.RouterLink;
        __VLS_components.RouterLink;
        // @ts-ignore
        [RouterLink, RouterLink,];
        const __VLS_54 = __VLS_asFunctionalComponent(__VLS_53, new __VLS_53({ ...{ class: ("home-compose-fab") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }));
        const __VLS_55 = __VLS_54({ ...{ class: ("home-compose-fab") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }, ...__VLS_functionalComponentArgsRest(__VLS_54));
        ({}({ ...{ class: ("home-compose-fab") }, to: (({ name: 'PostCreate' })), "aria-label": ("Post"), title: ("Post"), }));
        // @ts-ignore
        [AppIcon,];
        const __VLS_59 = __VLS_asFunctionalComponent(AppIcon, new AppIcon({ name: ("compose"), size: ((27)), }));
        const __VLS_60 = __VLS_59({ name: ("compose"), size: ((27)), }, ...__VLS_functionalComponentArgsRest(__VLS_59));
        ({}({ name: ("compose"), size: ((27)), }));
        // @ts-ignore
        [authStore,];
        const __VLS_63 = __VLS_pickFunctionalComponentCtx(AppIcon, __VLS_60);
        (__VLS_58.slots).default;
        const __VLS_58 = __VLS_pickFunctionalComponentCtx(__VLS_53, __VLS_55);
    }
    if (typeof __VLS_styleScopedClasses === 'object' && !Array.isArray(__VLS_styleScopedClasses)) {
        __VLS_styleScopedClasses['home-view'];
        __VLS_styleScopedClasses['home-feed-header'];
        __VLS_styleScopedClasses['home-feed-header__content'];
        __VLS_styleScopedClasses['home-feed-panel'];
        __VLS_styleScopedClasses['home-state'];
        __VLS_styleScopedClasses['home-state--auth'];
        __VLS_styleScopedClasses['home-state__actions'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-state__secondary'];
        __VLS_styleScopedClasses['feed-list'];
        __VLS_styleScopedClasses['feed-list--loading'];
        __VLS_styleScopedClasses['feed-skeleton'];
        __VLS_styleScopedClasses['feed-skeleton__author'];
        __VLS_styleScopedClasses['feed-skeleton__title'];
        __VLS_styleScopedClasses['feed-skeleton__line'];
        __VLS_styleScopedClasses['feed-skeleton__line'];
        __VLS_styleScopedClasses['feed-skeleton__line--short'];
        __VLS_styleScopedClasses['home-state'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-state'];
        __VLS_styleScopedClasses['feed-list'];
        __VLS_styleScopedClasses['home-feed-inline-state'];
        __VLS_styleScopedClasses['home-feed-inline-state'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['recommendation-card-wrapper'];
        __VLS_styleScopedClasses['home-feed-sentinel'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-feed-sentinel'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-state__primary'];
        __VLS_styleScopedClasses['home-compose-fab'];
    }
    var __VLS_slots;
    return __VLS_slots;
    const __VLS_componentsOption = {};
    let __VLS_name;
    const __VLS_internalComponent = (await import('vue')).defineComponent({
        setup() {
            return {
                FeedTabs: FeedTabs,
                PostCard: PostCard,
                AppIcon: AppIcon,
                MobileHomeHeader: MobileHomeHeader,
                authStore: authStore,
                feedStore: feedStore,
                skeletonPosts: skeletonPosts,
                forYouSentinelRef: forYouSentinelRef,
                forYouIntersectionObserverAvailable: forYouIntersectionObserverAvailable,
                followingSentinelRef: followingSentinelRef,
                followingIntersectionObserverAvailable: followingIntersectionObserverAvailable,
                forYouFeed: forYouFeed,
                followingFeed: followingFeed,
                likePendingPostIds: likePendingPostIds,
                repostPendingPostIds: repostPendingPostIds,
                pendingDeletePostIds: pendingDeletePostIds,
                deleteErrors: deleteErrors,
                activeTab: activeTab,
                activeFeedStatus: activeFeedStatus,
                hasRecentlyPublishedPosts: hasRecentlyPublishedPosts,
                visibleForYouItems: visibleForYouItems,
                canDeletePost: canDeletePost,
                selectTab: selectTab,
                loadForYou: loadForYou,
                loadMoreFollowing: loadMoreFollowing,
                loadMoreForYou: loadMoreForYou,
                retryForYouLoadMore: retryForYouLoadMore,
                retryFollowingLoadMore: retryFollowingLoadMore,
                retryActiveFeed: retryActiveFeed,
                bindRecommendationCard: bindRecommendationCard,
                handleRecommendationClick: handleRecommendationClick,
                handleLikeToggle: handleLikeToggle,
                handleRepostToggle: handleRepostToggle,
                handleDeletePost: handleDeletePost,
                handleNotInterested: handleNotInterested,
            };
        },
    });
}
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
;
