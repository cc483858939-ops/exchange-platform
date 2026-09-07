import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { setPageTitle } from '../utils/pageTitle';

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
const NotFoundView = () => import('../views/NotFoundView.vue');

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'Home', component: HomeView, meta: { layout: 'app', title: 'Home' } },
  {
    path: '/exchange',
    name: 'CurrencyExchange',
    component: LiveExchangeView,
    meta: { layout: 'app', title: 'Currency Exchange' },
  },

  { path: '/posts/new', name: 'PostCreate', component: PostCreateView, meta: { layout: 'app', title: 'Post' } },
  { path: '/posts/:id', name: 'PostDetail', component: PostDetailView, meta: { layout: 'app', title: 'Post' } },

  { path: '/users/:id', name: 'UserProfile', component: UserProfileView, meta: { layout: 'app', title: 'Profile' } },
  {
    path: '/users/:id/following',
    name: 'UserFollowing',
    component: UserConnectionsView,
    meta: { layout: 'app', title: 'Following' },
  },
  {
    path: '/users/:id/followers',
    name: 'UserFollowers',
    component: UserConnectionsView,
    meta: { layout: 'app', title: 'Followers' },
  },
  { path: '/search', name: 'UserSearch', component: UserSearchView, meta: { layout: 'app', title: 'Search' } },
  { path: '/history', name: 'History', component: HistoryView, meta: { layout: 'app', title: 'History' } },
  {
    path: '/notifications',
    name: 'Notifications',
    component: NotificationsView,
    meta: { layout: 'app', title: 'Notifications' },
  },
  { path: '/login', name: 'Login', component: Login, meta: { layout: 'auth', title: 'Log in' } },
  { path: '/register', name: 'Register', component: Register, meta: { layout: 'auth', title: 'Sign up' } },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: NotFoundView,
    meta: { layout: 'app', title: 'Page not found' },
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

router.beforeEach((to, from) => {
  if (
    to.name !== 'Login'
    || to.query.returnTo !== undefined
    || from.matched.length === 0
    || from.meta.layout === 'auth'
  ) {
    return true;
  }

  return {
    name: 'Login',
    query: {
      ...to.query,
      returnTo: from.fullPath,
    },
    hash: to.hash,
  };
});

router.afterEach((to) => {
  const title = typeof to.meta.title === 'string' ? to.meta.title : undefined;
  setPageTitle(title);
});

export default router;
