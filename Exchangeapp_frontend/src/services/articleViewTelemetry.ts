import apiClient from '../axios';

export type ArticleViewEvent = {
  event_id: string;
  article_id: number;
  occurred_at: string;
};

type ArticleViewBatchResponse = {
  accepted: number;
  rejected: number;
  results: Array<{
    event_id: string;
    status: 'accepted' | 'rejected';
    reason?: string;
  }>;
};

const maxBatchEvents = 50;
const pendingEvents = new Map<string, ArticleViewEvent>();
let flushPromise: Promise<void> | null = null;

export function enqueueArticleView(
  articleID: number,
  eventID: string,
  occurredAt = new Date().toISOString(),
): Promise<void> {
  if (!pendingEvents.has(eventID)) {
    pendingEvents.set(eventID, {
      event_id: eventID,
      article_id: articleID,
      occurred_at: occurredAt,
    });
  }
  return flushArticleViewEvents();
}

export function flushArticleViewEvents(): Promise<void> {
  if (flushPromise) {
    return flushPromise;
  }
  if (pendingEvents.size === 0) {
    return Promise.resolve();
  }

  const batch = Array.from(pendingEvents.values()).slice(0, maxBatchEvents);
  let succeeded = false;
  flushPromise = apiClient
    .post<ArticleViewBatchResponse>('/article-view-events', { events: batch })
    .then(() => {
      succeeded = true;
      for (const event of batch) {
        if (pendingEvents.get(event.event_id) === event) {
          pendingEvents.delete(event.event_id);
        }
      }
    })
    .catch(() => {
      // Keep failed events queued. The detail view retries with the same event ID.
    })
    .finally(() => {
      flushPromise = null;
      if (succeeded && pendingEvents.size > 0) {
        void flushArticleViewEvents();
      }
    });

  return flushPromise;
}
