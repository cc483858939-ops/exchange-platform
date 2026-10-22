// @vitest-environment jsdom

import { createPinia } from 'pinia';
import { createApp, nextTick } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('./components/layout/AppShell.vue', () => ({
  default: { name: 'AppShell', template: '<div data-app-shell><slot /></div>' },
}));
vi.mock('./components/composer/PostPublishStatus.vue', () => ({
  default: { name: 'PostPublishStatus', template: '<div data-post-publish-status />' },
}));
vi.mock('./components/onboarding/OnboardingDialog.vue', () => ({
  default: { name: 'OnboardingDialog', template: '<div data-onboarding-dialog />' },
}));
vi.mock('./services/postViewTelemetry', () => ({
  initializePostViewTelemetry: () => null,
}));
import App from './App.vue';

const createTestRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'Home', component: { template: '<main data-public-home>Home</main>' }, meta: { layout: 'app' } },
    { path: '/login', name: 'Login', component: { template: '<main data-login-page>Log in</main>' }, meta: { layout: 'auth' } },
    { path: '/topics', name: 'TopicDirectory', component: { template: '<main data-topics-page>Topics</main>' }, meta: { layout: 'app' } },
  ],
});

const originalLocalStorageDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage');

afterEach(() => {
  if (originalLocalStorageDescriptor) {
    Object.defineProperty(window, 'localStorage', originalLocalStorageDescriptor);
  }
  vi.restoreAllMocks();
});

describe('App with unavailable authentication storage', () => {
  it('renders Home, Login, and a public Topics route as a guest', async () => {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get: () => { throw new DOMException('Blocked', 'SecurityError'); },
    });
    vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    const container = document.createElement('div');
    document.body.append(container);
    const app = createApp(App);
    const router = createTestRouter();

    try {
      await router.push('/');
      app.use(createPinia());
      app.use(router);
      app.mount(container);
      await router.isReady();
      await flushPromises();
      await nextTick();

      expect(container.querySelector('[data-public-home]')).not.toBeNull();

      await router.push('/login');
      await flushPromises();
      await nextTick();
      expect(container.querySelector('[data-login-page]')).not.toBeNull();

      await router.push('/topics');
      await flushPromises();
      await nextTick();
      expect(container.querySelector('[data-topics-page]')).not.toBeNull();
    } finally {
      app.unmount();
      container.remove();
      if (originalLocalStorageDescriptor) {
        Object.defineProperty(window, 'localStorage', originalLocalStorageDescriptor);
      }
      if (router.currentRoute.value.fullPath !== '/') await router.push('/');
    }
  });
});
