import type { RouterScrollBehavior } from 'vue-router';
import { isViewOwnedScrollRoute } from './surfaceCachePolicy';

export const routeScrollBehavior: RouterScrollBehavior = (to, from, savedPosition) => {
  if (to.name === 'Home') {
    return {
      left: 0,
      top: 0,
    };
  }

  if (isViewOwnedScrollRoute(to)) {
    return false;
  }

  if (savedPosition) {
    return savedPosition;
  }

  const enteringPostCreate =
    to.name === 'PostCreate'
    && from.name !== 'PostCreate';

  const enteringDifferentPostDetail =
    to.name === 'PostDetail'
    && (
      from.name !== 'PostDetail'
      || String(to.params.id ?? '') !== String(from.params.id ?? '')
    );

  if (enteringPostCreate || enteringDifferentPostDetail) {
    return { top: 0 };
  }

  return false;
};
