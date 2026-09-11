import type { RouteLocationNormalizedLoaded } from 'vue-router';

export const getViewerCacheNamespace = (viewerID: number | null): string => (
  viewerID === null ? 'anonymous' : `viewer:${viewerID}`
);

const normalizePositiveRouteID = (value: unknown): number | null => {
  const raw = Array.isArray(value) ? value[0] : value;
  const numeric = Number(raw);

  return Number.isSafeInteger(numeric) && numeric > 0 ? numeric : null;
};

export const getRootSurfaceCacheKey = (
  route: RouteLocationNormalizedLoaded,
  viewerID: number | null,
): string | null => {
  switch (route.name) {
    case 'Home':
      return 'root:home';
    case 'UserSearch':
      return 'root:search';
    case 'CurrencyExchange':
      return 'root:exchange';
    case 'Notifications':
      return 'root:notifications';
    case 'UserProfile': {
      const profileID = normalizePositiveRouteID(route.params.id);
      return viewerID !== null && profileID === viewerID
        ? `root:profile:${viewerID}`
        : null;
    }
    default:
      return null;
  }
};

export const getExternalProfileCacheKey = (
  route: RouteLocationNormalizedLoaded,
  viewerID: number | null,
): string | null => {
  if (route.name !== 'UserProfile') {
    return null;
  }

  const profileID = normalizePositiveRouteID(route.params.id);
  if (profileID === null || (viewerID !== null && profileID === viewerID)) {
    return null;
  }

  return `external-profile:${profileID}`;
};

export const shouldPreserveExternalProfileCache = (
  route: RouteLocationNormalizedLoaded,
): boolean => (
  route.name === 'UserProfile'
  || route.name === 'PostDetail'
  || route.name === 'UserFollowing'
  || route.name === 'UserFollowers'
);

export const isViewOwnedScrollRoute = (
  route: RouteLocationNormalizedLoaded,
): boolean => (
  route.name === 'Home'
  || route.name === 'UserSearch'
  || route.name === 'CurrencyExchange'
  || route.name === 'Notifications'
  || route.name === 'UserProfile'
);
