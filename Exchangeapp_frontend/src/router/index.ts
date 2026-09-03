import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';

const HomeView = () => import('../views/HomeView.vue');
const LiveExchangeView = () => import('../views/LiveExchangeView.vue');
const PostDetailView = () => import('../views/PostDetailView.vue');
const PostCreateView = () => import('../views/PostCreateView.vue');
const UserProfileView = () => import('../views/UserProfileView.vue');
const UserConnectionsView = () => import('../views/UserConnectionsView.vue');
const UserSearchView = () => import('../views/UserSearchView.vue');
const HistoryView = () => import('../views/HistoryView.vue');
const NotificationsView = () => import('../views/NotificationsView.vue');
const Login = () => import('../components/Login.vue');
const Register = () => import('../components/Register.vue');

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'Home', component: HomeView, meta: { layout: 'app' } },
  { path: '/exchange', name: 'CurrencyExchange', component: LiveExchangeView, meta: { layout: 'app' } },

  { path: '/posts/new', name: 'PostCreate', component: PostCreateView, meta: { layout: 'app' } },
  { path: '/posts/:id', name: 'PostDetail', component: PostDetailView, meta: { layout: 'app' } },

  { path: '/users/:id', name: 'UserProfile', component: UserProfileView, meta: { layout: 'app' } },
  { path: '/users/:id/following', name: 'UserFollowing', component: UserConnectionsView, meta: { layout: 'app' } },
  { path: '/users/:id/followers', name: 'UserFollowers', component: UserConnectionsView, meta: { layout: 'app' } },
  { path: '/search', name: 'UserSearch', component: UserSearchView, meta: { layout: 'app' } },
  { path: '/history', name: 'History', component: HistoryView, meta: { layout: 'app' } },
  { path: '/notifications', name: 'Notifications', component: NotificationsView, meta: { layout: 'app' } },
  { path: '/login', name: 'Login', component: Login, meta: { layout: 'auth' } },
  { path: '/register', name: 'Register', component: Register, meta: { layout: 'auth' } },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
