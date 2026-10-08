// Coalesce enqueue writes until the end of this turn. Read the live queue when
// saving; never retain a snapshot that could resurrect cleared/acknowledged data.
// Sudden process termination before this microtask can lose this turn's events.
export const createTelemetryPersistence = (save: () => void) => {
  let pending = false;
  const flush = () => {
    if (!pending) return;
    pending = false;
    save();
  };
  return {
    schedule: () => {
      if (pending) return;
      pending = true;
      void Promise.resolve().then(flush);
    },
    flush,
  };
};
