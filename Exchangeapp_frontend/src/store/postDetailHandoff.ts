import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { FeedPost } from '../types/Feed';
import { useAuthStore } from './auth';

const maxHandoffAgeMs = 30_000;

export type PostDetailHandoff = {
  post: FeedPost;
  viewerID: number | null;
  capturedAt: number;
};

const cloneReference = (reference: FeedPost['quotePost']): FeedPost['quotePost'] => {
  if (!reference) {
    return reference;
  }
  if (reference.deleted) {
    return { ...reference };
  }
  return {
    ...reference,
    author: { ...reference.author },
    media: reference.media.map(item => ({ ...item })),
  };
};

const clonePost = (post: FeedPost): FeedPost => ({
  ...post,
  author: { ...post.author },
  media: post.media.map(item => ({ ...item })),
  quotePost: cloneReference(post.quotePost),
  replyToPost: cloneReference(post.replyToPost),
  repostContext: post.repostContext
    ? { actor: { ...post.repostContext.actor } }
    : undefined,
});

export const usePostDetailHandoffStore = defineStore('postDetailHandoff', () => {
  const authStore = useAuthStore();
  const pending = ref<PostDetailHandoff | null>(null);

  const remember = (post: FeedPost) => {
    pending.value = {
      post: clonePost(post),
      viewerID: authStore.currentIdentity?.id ?? null,
      capturedAt: Date.now(),
    };
  };

  const consume = (postID: number) => {
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
      || handoff.post.id !== postID
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

