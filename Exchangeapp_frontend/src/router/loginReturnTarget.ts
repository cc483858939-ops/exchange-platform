import type { Router } from 'vue-router';

export const resolveSafeLoginReturnTarget = (
  router: Router,
  rawReturnTo: unknown,
): string | null => {
  if (
    typeof rawReturnTo !== 'string'
    || rawReturnTo.length === 0
    || !rawReturnTo.startsWith('/')
    || rawReturnTo.startsWith('//')
    || rawReturnTo.includes('\\')
  ) {
    return null;
  }

  try {
    const resolved = router.resolve(rawReturnTo);
    if (resolved.matched.length === 0 || resolved.meta.layout === 'auth') {
      return null;
    }

    return resolved.fullPath;
  } catch {
    return null;
  }
};
