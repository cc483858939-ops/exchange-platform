export type InitialDocumentNavigation = {
  type: PerformanceNavigationTiming['type'] | 'unknown';
  url: string;
};

const readInitialDocumentNavigation = (): InitialDocumentNavigation => {
  if (
    typeof performance === 'undefined'
    || typeof performance.getEntriesByType !== 'function'
  ) {
    return { type: 'unknown', url: '' };
  }

  const entry = performance
    .getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;

  if (!entry) {
    return { type: 'unknown', url: '' };
  }

  return {
    type: entry.type ?? 'unknown',
    url: entry.name ?? '',
  };
};

const normalizeDocumentURL = (url: string) => {
  if (!url) {
    return '';
  }

  try {
    const parsed = new URL(url, 'http://document.local');
    // A URL fragment does not create a new document navigation, and some
    // browsers omit it from PerformanceNavigationTiming.name. Compare the
    // document identity by path and query so an initial hash cannot become a
    // false negative when the timing entry does not preserve the fragment.
    return `${parsed.pathname}${parsed.search}`;
  } catch {
    return url;
  }
};

export const initialDocumentNavigation = readInitialDocumentNavigation();

export const isInitialDocumentEntryForRoute = (
  routeFullPath: string,
  navigation: InitialDocumentNavigation = initialDocumentNavigation,
) => (
  (navigation.type === 'navigate' || navigation.type === 'reload')
  && normalizeDocumentURL(navigation.url) === normalizeDocumentURL(routeFullPath)
);
