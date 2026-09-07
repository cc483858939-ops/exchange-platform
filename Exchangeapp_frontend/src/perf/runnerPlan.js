export const PERF_COUNTS = [20, 50, 100, 200, 300];
export const PERF_APPEND_BASES = [20, 100, 200, 280];
export const PERF_VIEWPORTS = [
    { viewport: 'desktop', width: 1440, height: 900 },
    { viewport: 'mobile', width: 390, height: 844 },
];
export const PERF_MODES = [
    { trackView: true, label: 'tracked' },
    { trackView: false, label: 'untracked' },
];
export const PERF_RECORDED_RUNS = 3;
export const PERF_EXECUTIONS_PER_SCENARIO = PERF_RECORDED_RUNS + 1;
export const PERF_MAX_RECORDED_RETRIES = 2;
const isAppendBase = (count) => (PERF_APPEND_BASES.includes(count));
export function createPerfScenarioPlans(fixture = 'mixed') {
    const plans = [];
    for (const viewport of PERF_VIEWPORTS) {
        for (const mode of PERF_MODES) {
            for (const count of PERF_COUNTS) {
                plans.push({
                    viewport: viewport.viewport,
                    width: viewport.width,
                    height: viewport.height,
                    count,
                    trackView: mode.trackView,
                    fixture,
                    runType: 'matrix',
                    appendFrom: isAppendBase(count) ? count : undefined,
                });
            }
            plans.push({
                viewport: viewport.viewport,
                width: viewport.width,
                height: viewport.height,
                count: 280,
                trackView: mode.trackView,
                fixture,
                runType: 'append',
                appendFrom: 280,
            });
        }
    }
    return plans;
}
export const scenarioPlanLabel = (plan) => (`${plan.viewport} · ${plan.count} · ${plan.trackView ? 'tracked' : 'untracked'} · ${plan.runType}`);
