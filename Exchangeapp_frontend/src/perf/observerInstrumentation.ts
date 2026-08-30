import type { PerfObserverMetrics } from './types';

type InstrumentedWindow = Window & {
  IntersectionObserver: typeof IntersectionObserver;
};

export interface IntersectionObserverInstrumentation {
  snapshot(): PerfObserverMetrics;
  restore(): void;
}

export function installIntersectionObserverInstrumentation(
  targetWindow: Window = window,
): IntersectionObserverInstrumentation {
  const target = targetWindow as InstrumentedWindow;
  const NativeObserver = target.IntersectionObserver;
  const activeTargets = new Map<Element, number>();
  const state = {
    supported: typeof NativeObserver === 'function',
    instancesCreated: 0,
    observeCalls: 0,
    unobserveCalls: 0,
    peakTargets: 0,
  };

  const addTarget = (element: Element) => {
    const nextCount = (activeTargets.get(element) ?? 0) + 1;
    activeTargets.set(element, nextCount);
    state.peakTargets = Math.max(state.peakTargets, activeTargets.size);
  };

  const removeTarget = (element: Element) => {
    const previousCount = activeTargets.get(element);
    if (previousCount === undefined) {
      return;
    }
    if (previousCount <= 1) {
      activeTargets.delete(element);
    } else {
      activeTargets.set(element, previousCount - 1);
    }
  };

  if (state.supported) {
    class DelegatingIntersectionObserver {
      private readonly nativeObserver: IntersectionObserver;
      private readonly targets = new Set<Element>();

      constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
        state.instancesCreated += 1;
        this.nativeObserver = new NativeObserver(callback, options);
      }

      get root(): Element | Document | null {
        return this.nativeObserver.root;
      }

      get rootMargin(): string {
        return this.nativeObserver.rootMargin;
      }

      get thresholds(): ReadonlyArray<number> {
        return this.nativeObserver.thresholds;
      }

      observe(element: Element): void {
        state.observeCalls += 1;
        this.targets.add(element);
        addTarget(element);
        this.nativeObserver.observe(element);
      }

      unobserve(element: Element): void {
        state.unobserveCalls += 1;
        if (this.targets.delete(element)) {
          removeTarget(element);
        }
        this.nativeObserver.unobserve(element);
      }

      disconnect(): void {
        for (const element of this.targets) {
          removeTarget(element);
        }
        this.targets.clear();
        this.nativeObserver.disconnect();
      }

      takeRecords(): IntersectionObserverEntry[] {
        return this.nativeObserver.takeRecords();
      }
    }

    target.IntersectionObserver = DelegatingIntersectionObserver as unknown as typeof IntersectionObserver;
  }

  let restored = false;
  return {
    snapshot: () => ({
      supported: state.supported,
      instancesCreated: state.instancesCreated,
      observeCalls: state.observeCalls,
      unobserveCalls: state.unobserveCalls,
      targetsBeforeCleanup: activeTargets.size,
      currentTargets: activeTargets.size,
      peakTargets: state.peakTargets,
    }),
    restore: () => {
      if (restored) {
        return;
      }
      restored = true;
      if (state.supported) {
        target.IntersectionObserver = NativeObserver;
      }
      activeTargets.clear();
    },
  };
}
