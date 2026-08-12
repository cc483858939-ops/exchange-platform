import { createRouter, createWebHistory, RouteRecordRaw } from 'vue-router';
import HomeView from '../views/HomeView.vue';
import LiveExchangeView from '../views/LiveExchangeView.vue';

import NewsDetailView from '../views/NewsDetailView.vue';
import ArticleCreateView from '../views/ArticleCreateView.vue';

import UserProfileView from '../views/UserProfileView.vue';
import UserConnectionsView from '../views/UserConnectionsView.vue';
import Login from '../components/Login.vue';
import Register from '../components/Register.vue';

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'Home', component: HomeView, meta: { layout: 'app' } },
  { path: '/exchange', name: 'CurrencyExchange', component: LiveExchangeView, meta: { layout: 'app' } },

  { path: '/news/new', name: 'ArticleCreate', component: ArticleCreateView, meta: { layout: 'app' } },
  { path: '/news/:id', name: 'NewsDetail', component: NewsDetailView, meta: { layout: 'app' } },

  { path: '/users/:id', name: 'UserProfile', component: UserProfileView, meta: { layout: 'app' } },
  { path: '/users/:id/following', name: 'UserFollowing', component: UserConnectionsView, meta: { layout: 'app' } },
  { path: '/users/:id/followers', name: 'UserFollowers', component: UserConnectionsView, meta: { layout: 'app' } },
  { path: '/login', name: 'Login', component: Login, meta: { layout: 'auth' } },
  { path: '/register', name: 'Register', component: Register, meta: { layout: 'auth' } },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
