import type { LocationQuery, RouteLocationRaw, Router } from 'vue-router';
import { resolveSafeLoginReturnTarget } from './loginReturnTarget';

export const resolveAuthSuccessDestination = (
  router: Router,
  query: LocationQuery,
  currentIdentity: { id?: unknown } | null | undefined,
): RouteLocationRaw | string => {
  const returnTarget = resolveSafeLoginReturnTarget(router, query.returnTo);
  if (returnTarget) {
    return returnTarget;
  }

  if (query.intent === 'profile') {
    const id = currentIdentity?.id;
    if (typeof id === 'number' && Number.isSafeInteger(id) && id > 0) {
      return { name: 'UserProfile', params: { id: String(id) } };
    }
  }

  return { name: 'Home' };
};
