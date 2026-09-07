const normalizedCount = (value) => {
    const count = Number.isFinite(value) ? Math.floor(value) : 0;
    return Math.max(0, count);
};
export function formatCompactEngagementCount(value) {
    return new Intl.NumberFormat(undefined, {
        notation: 'compact',
        maximumFractionDigits: 1,
    }).format(normalizedCount(value));
}
export function formatAccessibleEngagementCount(value, noun) {
    return normalizedCount(value).toLocaleString() + ' ' + noun;
}
