const BRAND_TITLE = 'Exchange';
const TITLE_SEPARATOR = ' — ';

export const formatPageTitle = (pageTitle?: string | null) => {
  const normalized = pageTitle?.trim();

  return normalized
    ? `${normalized}${TITLE_SEPARATOR}${BRAND_TITLE}`
    : BRAND_TITLE;
};

export const setPageTitle = (pageTitle?: string | null) => {
  if (typeof document === 'undefined') {
    return;
  }

  document.title = formatPageTitle(pageTitle);
};
