import { createRouter, createWebHistory, RouteRecordRaw } from 'vue-router';
import HomeView from '../views/HomeView.vue';
import LiveExchangeView from '../views/LiveExchangeView.vue';
import NewsView from '../views/NewsView.vue';
import NewsDetailView from '../views/NewsDetailView.vue';
import ArticleCreateView from '../views/ArticleCreateView.vue';
import RecommendationView from '../views/RecommendationView.vue';
import Login from '../components/Login.vue';
import Register from '../components/Register.vue';

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'Home', component: HomeView },
  { path: '/exchange', name: 'CurrencyExchange', component: LiveExchangeView },
  { path: '/news', name: 'News', component: NewsView },
  { path: '/news/new', name: 'ArticleCreate', component: ArticleCreateView },
  { path: '/news/:id', name: 'NewsDetail', component: NewsDetailView },
  { path: '/recommendations', name: 'Recommendations', component: RecommendationView },
  { path: '/login', name: 'Login', component: Login },
  { path: '/register', name: 'Register', component: Register },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
