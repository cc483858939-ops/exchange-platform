import { useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import { useAuthStore } from '../store/auth';
import { AuthRequestError } from '../utils/authError';

export function useLogout() {
  const router = useRouter();
  const authStore = useAuthStore();

  const handleLogout = () => {
    void authStore.logout().catch((error: unknown) => {
      ElMessage.warning(error instanceof AuthRequestError ? error.message
        : 'Signed out on this browser, but server sign-out could not be confirmed. The session may still be active.');
    });
    void router.push({ name: 'Home' });
  };

  return { authStore, handleLogout };
}
