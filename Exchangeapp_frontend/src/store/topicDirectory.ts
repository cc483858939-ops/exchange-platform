import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import { getTopics, type TopicSummary } from '../services/topicService';

export type TopicDirectoryStatus = 'idle' | 'loading' | 'ready' | 'error';

export const useTopicDirectoryStore = defineStore('topicDirectory', () => {
  const items = ref<TopicSummary[]>([]);
  const status = ref<TopicDirectoryStatus>('idle');
  const error = ref('');
  const loading = computed(() => status.value === 'loading');
  const loaded = computed(() => status.value === 'ready');
  const unavailable = computed(() => status.value === 'error');
  let inFlight: Promise<boolean> | null = null;

  const performLoad = async (): Promise<boolean> => {
    status.value = 'loading';
    error.value = '';

    try {
      const response = await getTopics();
      items.value = response.items ?? [];
      status.value = 'ready';
      return true;
    } catch {
      items.value = [];
      status.value = 'error';
      error.value = 'Topics unavailable.';
      return false;
    }
  };

  const load = (force = false): Promise<boolean> => {
    if (inFlight) return inFlight;
    if (!force && status.value === 'ready') return Promise.resolve(true);
    if (!force && status.value === 'error') return Promise.resolve(false);

    const request = performLoad();
    inFlight = request;
    const clearInFlight = () => {
      if (inFlight === request) inFlight = null;
    };
    void request.then(clearInFlight, clearInFlight);
    return request;
  };

  const ensureLoaded = () => load();
  const retry = () => load(true);

  return { items, status, error, loading, loaded, unavailable, ensureLoaded, retry };
});
