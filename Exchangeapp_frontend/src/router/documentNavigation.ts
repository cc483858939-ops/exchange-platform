export type InitialDocumentNavigation = {
  type: PerformanceNavigationTiming['type'] | 'unknown';
  url: string;
};

const readNavigationType = (): InitialDocumentNavigation['type'] => {
  if (
    typeof performance === 'undefined'
    || typeof performance.getEntriesByType !== 'function'
  ) {
    return 'unknown';
  }

  const entry = performance
    .getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;

  return entry?.type ?? 'unknown';
};

const readDocumentURL = () => {
  if (typeof window === 'undefined') {
    return '';
  }

  return `${window.location.pathname}${window.location.search}${window.location.hash}`;
};

const normalizeDocumentURL = (url: string) => {
  if (!url) {
    return '';
  }

  try {
    const parsed = new URL(url, 'http://document.local');
    return `${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return url;
  }
};

export const initialDocumentNavigation: InitialDocumentNavigation = {
  type: readNavigationType(),
  url: readDocumentURL(),
};

export const isInitialDocumentReloadForRoute = (
  routeFullPath: string,
  navigation: InitialDocumentNavigation = initialDocumentNavigation,
) => (
  navigation.type === 'reload'
  && normalizeDocumentURL(navigation.url) === normalizeDocumentURL(routeFullPath)
);
