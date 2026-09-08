import type { RouterScrollBehavior } from 'vue-router';

export const routeScrollBehavior: RouterScrollBehavior = (to, from, savedPosition) => {
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
