<template>
  <main class="app-shell">
    <header class="topbar">
      <router-link class="brand" :to="{ name: 'Home' }">
        <span class="brand-mark">GX</span>
        <span class="brand-copy">
          <strong>Go Exchange</strong>
          <small>market notes</small>
        </span>
      </router-link>

      <nav class="nav-links" aria-label="Main navigation">
        <router-link :to="{ name: 'Home' }">首页</router-link>
        <router-link :to="{ name: 'CurrencyExchange' }">汇率</router-link>
        <router-link :to="{ name: 'News' }">新闻</router-link>
        <router-link :to="{ name: 'Recommendations' }">推荐</router-link>
        <router-link v-if="authStore.isAuthenticated" :to="{ name: 'ArticleCreate' }">发布</router-link>
      </nav>

      <div class="account-actions">
        <template v-if="authStore.isAuthenticated">
          <button class="text-button" type="button" @click="handleLogout">退出</button>
        </template>
        <template v-else>
          <router-link class="text-button" :to="{ name: 'Login' }">登录</router-link>
          <router-link class="solid-button" :to="{ name: 'Register' }">注册</router-link>
        </template>
      </div>
    </header>

    <section class="route-surface">
      <router-view />
    </section>
  </main>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router';
import { useAuthStore } from './store/auth';

const router = useRouter();
const authStore = useAuthStore();

const handleLogout = () => {
  authStore.logout();
  router.push({ name: 'Home' });
};
</script>

<style scoped>
.app-shell {
  min-height: 100vh;
  width: 100%;
  max-width: 100%;
  overflow-x: hidden;
  background:
    radial-gradient(circle at 12% 8%, rgba(41, 98, 255, 0.12), transparent 32rem),
    linear-gradient(180deg, #f8fafc 0%, #eef3f8 46%, #f6f7f5 100%);
  color: #111827;
  font-family: "Aptos Display", "Segoe UI Variable", "PingFang SC", sans-serif;
}

.topbar {
  position: sticky;
  top: 0;
  z-index: 30;
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto minmax(180px, 1fr);
  align-items: center;
  gap: 24px;
  min-height: 72px;
  padding: 14px clamp(18px, 4vw, 48px);
  border-bottom: 1px solid rgba(17, 24, 39, 0.08);
  background: rgba(248, 250, 252, 0.82);
  backdrop-filter: blur(22px);
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  width: max-content;
  color: inherit;
  text-decoration: none;
}

.brand-mark {
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border-radius: 14px;
  background: #111827;
  color: #f8fafc;
  font-weight: 800;
  letter-spacing: 0.02em;
  box-shadow: 0 18px 50px rgba(17, 24, 39, 0.18);
}

.brand-copy {
  display: grid;
  gap: 1px;
}

.brand-copy strong {
  font-size: 15px;
  letter-spacing: 0.01em;
}

.brand-copy small {
  color: #64748b;
  font-size: 11px;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}

.nav-links {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px;
  border: 1px solid rgba(17, 24, 39, 0.08);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.72);
  box-shadow: 0 20px 60px rgba(15, 23, 42, 0.08);
}

.nav-links a,
.text-button,
.solid-button {
  min-height: 38px;
  white-space: nowrap;
  border-radius: 999px;
  font-size: 14px;
  font-weight: 700;
  text-decoration: none;
  transition: transform 180ms ease, color 180ms ease, background 180ms ease, border-color 180ms ease;
}

.nav-links a {
  display: inline-flex;
  align-items: center;
  padding: 0 16px;
  color: #475569;
}

.nav-links a.router-link-active {
  background: #111827;
  color: #ffffff;
}

.account-actions {
  display: inline-flex;
  justify-content: flex-end;
  align-items: center;
  gap: 10px;
}

.text-button,
.solid-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  cursor: pointer;
  font-family: inherit;
}

.text-button {
  padding: 0 14px;
  background: transparent;
  color: #334155;
}

.solid-button {
  padding: 0 18px;
  background: #1d4ed8;
  color: #ffffff;
  box-shadow: 0 18px 44px rgba(29, 78, 216, 0.24);
}

.nav-links a:hover,
.text-button:hover,
.solid-button:hover {
  transform: translateY(-1px);
}

.route-surface {
  min-height: calc(100vh - 72px);
}

@media (max-width: 860px) {
  .topbar {
    position: relative;
    grid-template-columns: 1fr;
    gap: 14px;
  }

  .nav-links {
    justify-content: space-between;
    width: 100%;
    overflow-x: auto;
  }

  .account-actions {
    justify-content: flex-start;
  }
}
</style>
