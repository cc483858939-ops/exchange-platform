export function installIntersectionObserverInstrumentation(targetWindow = window) {
    const target = targetWindow;
    const NativeObserver = target.IntersectionObserver;
    const activeTargets = new Map();
    const state = {
        supported: typeof NativeObserver === 'function',
        instancesCreated: 0,
        observeCalls: 0,
        unobserveCalls: 0,
        peakTargets: 0,
    };
    const addTarget = (element) => {
        const nextCount = (activeTargets.get(element) ?? 0) + 1;
        activeTargets.set(element, nextCount);
        state.peakTargets = Math.max(state.peakTargets, activeTargets.size);
    };
    const removeTarget = (element) => {
        const previousCount = activeTargets.get(element);
        if (previousCount === undefined) {
            return;
        }
        if (previousCount <= 1) {
            activeTargets.delete(element);
        }
        else {
            activeTargets.set(element, previousCount - 1);
        }
    };
    if (state.supported) {
        class DelegatingIntersectionObserver {
            nativeObserver;
            targets = new Set();
            constructor(callback, options) {
                state.instancesCreated += 1;
                this.nativeObserver = new NativeObserver(callback, options);
            }
            get root() {
                return this.nativeObserver.root;
            }
            get rootMargin() {
                return this.nativeObserver.rootMargin;
            }
            get thresholds() {
                return this.nativeObserver.thresholds;
            }
            observe(element) {
                state.observeCalls += 1;
                this.targets.add(element);
                addTarget(element);
                this.nativeObserver.observe(element);
            }
            unobserve(element) {
                state.unobserveCalls += 1;
                if (this.targets.delete(element)) {
                    removeTarget(element);
                }
                this.nativeObserver.unobserve(element);
            }
            disconnect() {
                for (const element of this.targets) {
                    removeTarget(element);
                }
                this.targets.clear();
                this.nativeObserver.disconnect();
            }
            takeRecords() {
                return this.nativeObserver.takeRecords();
            }
        }
        target.IntersectionObserver = DelegatingIntersectionObserver;
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
