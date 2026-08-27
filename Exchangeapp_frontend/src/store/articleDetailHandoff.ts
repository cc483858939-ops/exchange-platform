import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { FeedPost } from '../types/Feed';
import { useAuthStore } from './auth';

const maxHandoffAgeMs = 30_000;

export type ArticleDetailHandoff = {
  post: FeedPost;
  viewerID: number | null;
  capturedAt: number;
};

const clonePost = (post: FeedPost): FeedPost => ({
  ...post,
  author: { ...post.author },
});

export const useArticleDetailHandoffStore = defineStore('articleDetailHandoff', () => {
  const authStore = useAuthStore();
  const pending = ref<ArticleDetailHandoff | null>(null);

  const remember = (post: FeedPost) => {
    pending.value = {
      post: clonePost(post),
      viewerID: authStore.currentIdentity?.id ?? null,
      capturedAt: Date.now(),
    };
  };

  const consume = (articleID: number) => {
    const handoff = pending.value;
    pending.value = null;
    if (!handoff) {
      return null;
    }

    const viewerID = authStore.currentIdentity?.id ?? null;
    const age = Date.now() - handoff.capturedAt;
    if (
      !authStore.isAuthenticated
      || viewerID === null
      || handoff.viewerID !== viewerID
      || handoff.post.id !== articleID
      || age < 0
      || age > maxHandoffAgeMs
    ) {
      return null;
    }

    return handoff.post;
  };

  const clear = () => {
    pending.value = null;
  };

  return {
    pending,
    remember,
    consume,
    clear,
  };
});
