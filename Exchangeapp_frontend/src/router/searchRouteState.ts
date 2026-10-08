import { readonly, shallowRef } from 'vue';

export type SearchRouteSnapshot = Readonly<{
  tab: 'posts' | 'people';
  q?: string;
  author?: string;
  time?: 'any' | '24h' | '7d' | '30d' | 'custom';
  from?: string;
  to?: string;
}>;

type SearchRouteLike = Readonly<{
  name?: unknown;
  fullPath?: string;
  query?: Readonly<Record<string, unknown>>;
}>;

const lastSearchRouteSnapshot = shallowRef<SearchRouteSnapshot | null>(null);
export const rememberedSearchRouteSnapshot = readonly(lastSearchRouteSnapshot);

const scalarQueryValue = (value: unknown): string | null => {
  if (typeof value === 'string') return value;
  if (!Array.isArray(value)) return null;
  return value.find((item): item is string => typeof item === 'string') ?? null;
};

const absoluteISOString = (value: unknown): string | undefined => {
  if (typeof value !== 'string' || !value.trim()) return undefined;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : undefined;
};

export const normalizeSearchRouteSnapshot = (
  query: Readonly<Record<string, unknown>> | null | undefined,
): SearchRouteSnapshot | null => {
  if (!query) return null;
  const keys = ['tab', 'q', 'author', 'time', 'from', 'to'] as const;
  if (!keys.some(key => scalarQueryValue(query[key]) !== null)) return null;

  const tabValue = scalarQueryValue(query.tab);
  const tab: SearchRouteSnapshot['tab'] = tabValue === 'people' ? 'people' : 'posts';
  const snapshot: {
    tab: 'posts' | 'people';
    q?: string;
    author?: string;
    time?: 'any' | '24h' | '7d' | '30d' | 'custom';
    from?: string;
    to?: string;
  } = { tab };
  const q = scalarQueryValue(query.q)?.trim();
  if (q) snapshot.q = q;

  if (tab === 'posts') {
    const author = scalarQueryValue(query.author);
    if (author && /^\d+$/.test(author)) {
      const authorID = Number(author);
      if (Number.isSafeInteger(authorID) && authorID > 0) snapshot.author = String(authorID);
    }
    const time = scalarQueryValue(query.time);
    if (time === 'any' || time === '24h' || time === '7d' || time === '30d' || time === 'custom') {
      snapshot.time = time;
      if (time === 'custom') {
        snapshot.from = absoluteISOString(query.from);
        snapshot.to = absoluteISOString(query.to);
      }
    }
  }

  return snapshot;
};

export const rememberSearchRoute = (
  query: Readonly<Record<string, unknown>> | null,
): SearchRouteSnapshot | null => {
  lastSearchRouteSnapshot.value = normalizeSearchRouteSnapshot(query);
  return lastSearchRouteSnapshot.value;
};

const routeToPath = (snapshot: SearchRouteSnapshot | null): string => {
  if (!snapshot) return '/search';
  const params = new URLSearchParams();
  params.set('tab', snapshot.tab);
  if (snapshot.q) params.set('q', snapshot.q);
  if (snapshot.tab === 'posts') {
    if (snapshot.author) params.set('author', snapshot.author);
    if (snapshot.time) params.set('time', snapshot.time);
    if (snapshot.time === 'custom') {
      if (snapshot.from) params.set('from', snapshot.from);
      if (snapshot.to) params.set('to', snapshot.to);
    }
  }
  return `/search?${params.toString()}`;
};

export const searchNavigationDestination = (
  isAuthenticated: boolean,
  route: SearchRouteLike,
) => {
  const snapshot = route.name === 'UserSearch'
    ? normalizeSearchRouteSnapshot(route.query)
    : lastSearchRouteSnapshot.value;

  if (isAuthenticated) {
    return snapshot
      ? { name: 'UserSearch', query: snapshot }
      : { name: 'UserSearch' };
  }

  const returnTo = route.name === 'UserSearch' && route.fullPath
    ? route.fullPath
    : routeToPath(snapshot);
  return { name: 'Login', query: { returnTo } };
};
