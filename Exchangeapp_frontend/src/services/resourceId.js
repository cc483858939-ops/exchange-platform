export function normalizeResourceID(value, label = 'resource') {
    const raw = typeof value === 'number' ? String(value) : value.trim();
    const parsed = Number(raw);
    if (!raw || !Number.isSafeInteger(parsed) || parsed <= 0) {
        throw new Error(`Invalid ${label} id`);
    }
    return String(parsed);
}
