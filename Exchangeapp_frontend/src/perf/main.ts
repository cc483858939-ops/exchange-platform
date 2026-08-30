import { createApp, defineComponent } from 'vue';
import { createPinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import '../styles/tokens.css';
import '../styles/base.css';
import LongListPerfRunner from './LongListPerfRunner.vue';
import LongListPerfScenario from './LongListPerfScenario.vue';
import { parsePerfScenarioConfig } from './scenarioConfig';

const PerfPlaceholder = defineComponent({
  name: 'PerfPlaceholder',
  setup: () => () => null,
});

const perfRouter = createRouter({
  history: createMemoryHistory(),
  routes: [
    { name: 'NewsDetail', path: '/articles/:id', component: PerfPlaceholder },
    { name: 'UserProfile', path: '/users/:id', component: PerfPlaceholder },
  ],
});

const params = new URLSearchParams(window.location.search);
const isScenario = params.get('scenario') === '1';
const app = isScenario
  ? createApp(LongListPerfScenario, { config: parsePerfScenarioConfig(window.location.search) })
  : createApp(LongListPerfRunner);

app.use(createPinia());
app.use(perfRouter);

void perfRouter.push({ name: 'NewsDetail', params: { id: '1' } }).then(async () => {
  await perfRouter.isReady();
  app.mount('#app');
});
