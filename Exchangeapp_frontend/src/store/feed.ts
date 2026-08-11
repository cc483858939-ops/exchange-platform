import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { useAuthStore } from './auth';
import type { Article } from '../types/Article';
import type { FeedPost } from '../types/Feed';
import { articleToFeedPost } from '../utils/feedPost';

const maxRecentlyPublishedPosts = 5;

const normalizeViewerID = (value: unknown): number | null => {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return null;
  }
  return value;
};

export const useFeedStore = defineStore('feed', () => {
  const authStore = useAuthStore();
  const viewerID = ref<number | null>(null);
  const recentlyPublishedPosts = ref<FeedPost[]>([]);

  const clearRecentlyPublishedPosts = () => {
    recentlyPublishedPosts.value = [];
  };

  const setViewer = (nextViewerID: number | null) => {
    const normalizedViewerID = normalizeViewerID(nextViewerID);
    if (normalizedViewerID === viewerID.value) {
      return;
    }

    viewerID.value = normalizedViewerID;
    clearRecentlyPublishedPosts();
  };

  const registerPublishedArticle = (
    article: Article,
    publisherUserID: number,
  ): boolean => {
    const normalizedPublisherID = normalizeViewerID(publisherUserID);
    if (
      normalizedPublisherID === null
      || article?.ID <= 0
      || !article?.author
      || article.author.id !== normalizedPublisherID
      || viewerID.value !== normalizedPublisherID
    ) {
      return false;
    }

    const post = articleToFeedPost(article);
    recentlyPublishedPosts.value = [
      post,
      ...recentlyPublishedPosts.value.filter((item) => item.id !== post.id),
    ].slice(0, maxRecentlyPublishedPosts);
    return true;
  };

  watch(
    () => authStore.currentIdentity?.id,
    (nextViewerID) => {
      setViewer(nextViewerID ?? null);
    },
    { immediate: true },
  );

  return {
    viewerID,
    recentlyPublishedPosts,
    maxRecentlyPublishedPosts,
    setViewer,
    registerPublishedArticle,
    clearRecentlyPublishedPosts,
  };
});
