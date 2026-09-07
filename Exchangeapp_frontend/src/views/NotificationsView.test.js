// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import NotificationsView from './NotificationsView.vue';
import { useNotificationStore } from '../store/notification';
const mocks = vi.hoisted(() => ({
    authStore: null,
    router: { push: vi.fn() },
    getNotifications: vi.fn(),
    getUnreadNotificationCount: vi.fn(),
    markNotificationRead: vi.fn(),
    markAllNotificationsRead: vi.fn(),
}));
vi.mock('../store/auth', () => ({
    useAuthStore: () => mocks.authStore,
}));
vi.mock('../services/notificationService', () => ({
    getNotifications: mocks.getNotifications,
    getUnreadNotificationCount: mocks.getUnreadNotificationCount,
    markNotificationRead: mocks.markNotificationRead,
    markAllNotificationsRead: mocks.markAllNotificationsRead,
}));
vi.mock('vue-router', () => ({
    useRouter: () => mocks.router,
}));
const notification = (id, read = false, avatarURL = '') => ({
    id,
    type: 'post_liked',
    actor: { id: 9, username: 'alice', display_name: 'Alice', avatar_url: avatarURL },
    post_id: 42,
    conversation_id: 42,
    activity_at: '2026-08-22T12:00:00.000Z',
    read,
});
const setAuth = (id) => {
    mocks.authStore = reactive({
        isAuthenticated: id !== null,
        currentIdentity: id === null ? null : { id, username: `viewer-${id}` },
    });
};
const setNotificationViewer = (id) => {
    useNotificationStore().setViewer(id);
};
const mountView = () => mount(NotificationsView, {
    global: {
        stubs: {
            RouterLink: { template: '<a class="router-link-stub"><slot /></a>' },
        },
    },
});
const deferred = () => {
    let resolve;
    let reject;
    const promise = new Promise((resolvePromise, rejectPromise) => {
        resolve = resolvePromise;
        reject = rejectPromise;
    });
    return { promise, resolve, reject };
};
describe('NotificationsView', () => {
    beforeEach(() => {
        setActivePinia(createPinia());
        vi.clearAllMocks();
        vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
        setAuth(null);
        setNotificationViewer(null);
        mocks.getNotifications.mockResolvedValue({ items: [], next_cursor: null });
        mocks.getUnreadNotificationCount.mockResolvedValue(0);
        mocks.markNotificationRead.mockResolvedValue(undefined);
        mocks.markAllNotificationsRead.mockResolvedValue(undefined);
    });
    afterEach(() => {
        vi.restoreAllMocks();
    });
    it('does not request notifications while unauthenticated', async () => {
        const wrapper = mountView();
        await flushPromises();
        expect(wrapper.text()).toContain('Log in to view your notifications.');
        expect(mocks.getNotifications).not.toHaveBeenCalled();
        wrapper.unmount();
    });
    it('loads a page and deduplicates IDs across cursor pages', async () => {
        setAuth(7);
        setNotificationViewer(7);
        mocks.getNotifications
            .mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-1' })
            .mockResolvedValueOnce({ items: [notification(1), notification(2, true)], next_cursor: null });
        const wrapper = mountView();
        await flushPromises();
        expect(mocks.getNotifications).toHaveBeenCalledWith({ limit: 20 });
        expect(wrapper.findAll('.notification-card')).toHaveLength(1);
        await wrapper.find('.notifications-page__load-more').trigger('click');
        await flushPromises();
        expect(mocks.getNotifications).toHaveBeenLastCalledWith({ limit: 20, cursor: 'cursor-1' });
        expect(wrapper.findAll('.notification-card')).toHaveLength(2);
        wrapper.unmount();
    });
    it('navigates without waiting for mark-read and rolls back a failed optimistic read', async () => {
        setAuth(7);
        setNotificationViewer(7);
        mocks.getNotifications.mockResolvedValue({ items: [notification(1)], next_cursor: null });
        const pending = deferred();
        mocks.markNotificationRead.mockReturnValueOnce(pending.promise);
        const store = useNotificationStore();
        store.setUnreadCount(1);
        const wrapper = mountView();
        await flushPromises();
        await wrapper.find('.notification-card__open').trigger('click');
        expect(mocks.router.push).toHaveBeenCalledWith({ name: 'PostDetail', params: { id: '42' } });
        expect(store.pendingReadIDs.has(1)).toBe(true);
        expect(wrapper.find('.notification-card--unread').exists()).toBe(false);
        pending.reject(new Error('read failed'));
        await flushPromises();
        expect(wrapper.find('.notification-card--unread').exists()).toBe(true);
        expect(mocks.getUnreadNotificationCount).toHaveBeenCalled();
        wrapper.unmount();
    });
    it('renders notification actors through the shared avatar component', async () => {
        setAuth(7);
        setNotificationViewer(7);
        mocks.getNotifications.mockResolvedValue({
            items: [notification(1, false, '/actor.webp')],
            next_cursor: null,
        });
        const wrapper = mountView();
        await flushPromises();
        expect(wrapper.get('.notification-card__avatar .user-avatar__image').attributes('src'))
            .toBe('/actor.webp');
        expect(wrapper.get('.notification-card__avatar').attributes('aria-hidden')).toBe('true');
        wrapper.unmount();
    });
    it('keeps cached notifications while revalidating and blocks cursor loading', async () => {
        setAuth(7);
        setNotificationViewer(7);
        mocks.getNotifications.mockResolvedValueOnce({ items: [notification(1)], next_cursor: 'cursor-old' });
        const store = useNotificationStore();
        const wrapper = mountView();
        await flushPromises();
        mocks.getUnreadNotificationCount.mockResolvedValueOnce(2);
        await store.refreshUnreadCount();
        expect(store.listStale).toBe(true);
        expect(wrapper.findAll('.notification-card')).toHaveLength(1);
        const revalidation = deferred();
        mocks.getNotifications.mockReturnValueOnce(revalidation.promise);
        const request = store.revalidateNotifications();
        expect(store.revalidating).toBe(true);
        expect(store.loading).toBe(false);
        await store.loadMore();
        expect(mocks.getNotifications).toHaveBeenCalledTimes(2);
        revalidation.resolve({ items: [notification(2)], next_cursor: 'cursor-new' });
        await request;
        expect(store.listStale).toBe(false);
        expect(store.items.map(item => item.id)).toEqual([2, 1]);
        wrapper.unmount();
    });
});
