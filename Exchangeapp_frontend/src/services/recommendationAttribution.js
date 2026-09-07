const storageKey = 'recommendation_pending_attribution_v1';
const maxAgeMs = 24 * 60 * 60 * 1000;
const load = () => {
    try {
        const parsed = JSON.parse(sessionStorage.getItem(storageKey) ?? '{}');
        const now = Date.now();
        Object.entries(parsed).forEach(([id, value]) => {
            if (!value?.tracking?.token || now - value.saved_at > maxAgeMs)
                delete parsed[id];
        });
        return parsed;
    }
    catch {
        return {};
    }
};
const save = (items) => {
    try {
        sessionStorage.setItem(storageKey, JSON.stringify(items));
    }
    catch { /* unavailable storage */ }
};
export const savePendingRecommendationAttribution = (postID, tracking) => {
    if (!tracking?.token)
        return;
    const items = load();
    items[String(postID)] = { tracking, saved_at: Date.now() };
    save(items);
};
export const consumePendingRecommendationAttribution = (postID) => {
    const items = load();
    const item = items[String(postID)];
    delete items[String(postID)];
    save(items);
    if (!item || Date.parse(item.tracking.expires_at) <= Date.now())
        return null;
    return item.tracking;
};
