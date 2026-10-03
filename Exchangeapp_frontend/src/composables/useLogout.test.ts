// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

const mocks = vi.hoisted(() => ({ logout: vi.fn(), push: vi.fn(), warning: vi.fn() }));
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }));
vi.mock('../store/auth', () => ({ useAuthStore: () => ({ logout: mocks.logout }) }));
vi.mock('element-plus', () => ({ ElMessage: { warning: mocks.warning } }));
import { useLogout } from './useLogout';
import { AuthRequestError } from '../utils/authError';

beforeEach(() => {
  vi.resetAllMocks();
  mocks.logout.mockResolvedValue(undefined);
  mocks.push.mockResolvedValue(undefined);
});

describe('logout feedback', () => {
  it('reports pending local cleanup separately from server revocation', async () => {
    const message = 'Signed out locally, but saved credentials could not be cleared. Please retry sign-out.';
    mocks.logout.mockRejectedValue(new AuthRequestError(message, 'AUTH_LOGOUT_CLEANUP_PENDING'));
    useLogout().handleLogout();
    await flushPromises();
    expect(mocks.warning).toHaveBeenCalledWith(message);
  });
  it('returns home immediately while server logout is pending', async () => {
    let resolve!: () => void;
    mocks.logout.mockReturnValue(new Promise<void>(done => { resolve = done; }));
    useLogout().handleLogout();
    expect(mocks.push).toHaveBeenCalledWith({ name: 'Home' });
    expect(mocks.logout).toHaveBeenCalledTimes(1);
    resolve();
    await flushPromises();
    expect(mocks.warning).not.toHaveBeenCalled();
  });

  it('shows unconfirmed server logout without an unhandled rejection', async () => {
    mocks.logout.mockRejectedValue(new Error('unconfirmed'));
    useLogout().handleLogout();
    await flushPromises();
    expect(mocks.warning).toHaveBeenCalledWith(
      'Signed out on this browser, but server sign-out could not be confirmed. The session may still be active.',
    );
    expect(mocks.push).toHaveBeenCalledTimes(1);
  });
});
