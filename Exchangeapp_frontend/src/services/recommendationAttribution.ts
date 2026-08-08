import type { RecommendationTracking } from '../types/Recommendation';

const storageKey = 'recommendation_pending_attribution_v1';
const maxAgeMs = 24 * 60 * 60 * 1000;

type StoredAttribution = { tracking: RecommendationTracking; saved_at: number };
type AttributionMap = Record<string, StoredAttribution>;

const load = (): AttributionMap => {
  try {
    const parsed = JSON.parse(sessionStorage.getItem(storageKey) ?? '{}') as AttributionMap;
    const now = Date.now();
    Object.entries(parsed).forEach(([id, value]) => {
      if (!value?.tracking?.token || now - value.saved_at > maxAgeMs) delete parsed[id];
    });
    return parsed;
  } catch { return {}; }
};

const save = (items: AttributionMap) => {
  try { sessionStorage.setItem(storageKey, JSON.stringify(items)); } catch { /* unavailable storage */ }
};

export const savePendingRecommendationAttribution = (articleID: number, tracking?: RecommendationTracking) => {
  if (!tracking?.token) return;
  const items = load();
  items[String(articleID)] = { tracking, saved_at: Date.now() };
  save(items);
};

export const consumePendingRecommendationAttribution = (articleID: number): RecommendationTracking | null => {
  const items = load();
  const item = items[String(articleID)];
  delete items[String(articleID)];
  save(items);
  if (!item || Date.parse(item.tracking.expires_at) <= Date.now()) return null;
  return item.tracking;
};