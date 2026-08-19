import {
  onBeforeUnmount,
  onMounted,
  readonly,
  ref,
} from 'vue';

const TICK_INTERVAL_MS = 30_000;
const now = ref(new Date());
const readonlyNow = readonly(now);

let subscriberCount = 0;
let timerID: number | null = null;
let visibilityListenerAttached = false;

const updateNow = () => {
  now.value = new Date();
};

const handleVisibilityChange = () => {
  if (document.visibilityState === 'visible') {
    updateNow();
  }
};

const startClock = () => {
  updateNow();

  if (timerID === null) {
    timerID = window.setInterval(updateNow, TICK_INTERVAL_MS);
  }

  if (!visibilityListenerAttached) {
    document.addEventListener('visibilitychange', handleVisibilityChange);
    visibilityListenerAttached = true;
  }
};

const stopClock = () => {
  if (timerID !== null) {
    window.clearInterval(timerID);
    timerID = null;
  }

  if (visibilityListenerAttached) {
    document.removeEventListener('visibilitychange', handleVisibilityChange);
    visibilityListenerAttached = false;
  }
};

export function useNow() {
  onMounted(() => {
    subscriberCount += 1;

    if (subscriberCount === 1) {
      startClock();
    }
  });

  onBeforeUnmount(() => {
    subscriberCount = Math.max(0, subscriberCount - 1);

    if (subscriberCount === 0) {
      stopClock();
    }
  });

  return readonlyNow;
}
