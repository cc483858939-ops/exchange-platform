import { useRouter } from 'vue-router';
import { useAuthStore } from '../store/auth';

export function useLogout() {
  const router = useRouter();
  const authStore = useAuthStore();

  const handleLogout = () => {
    authStore.logout();
    void router.push({ name: 'Home' });
  };

  return { authStore, handleLogout };
}