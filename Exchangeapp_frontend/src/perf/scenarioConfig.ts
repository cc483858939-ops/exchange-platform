import type { PerfFixture, PerfRunType, PerfScenarioConfig, PerfViewport } from './types';

const viewportDefaults: Record<PerfViewport, { width: number; height: number }> = {
  desktop: { width: 1440, height: 900 },
  mobile: { width: 390, height: 844 },
};

const positiveInteger = (value: string | null, fallback: number): number => {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;
};

export function parsePerfScenarioConfig(search: string): PerfScenarioConfig {
  const params = new URLSearchParams(search);
  const viewport: PerfViewport = params.get('viewport') === 'mobile' ? 'mobile' : 'desktop';
  const defaults = viewportDefaults[viewport];
  const fixture: PerfFixture = params.get('fixture') === 'text-only' ? 'text-only' : 'mixed';
  const runType: PerfRunType = params.get('runType') === 'append' ? 'append' : 'matrix';

  return {
    runId: params.get('runId')?.trim() || 'standalone-scenario',
    viewport,
    width: positiveInteger(params.get('width'), defaults.width),
    height: positiveInteger(params.get('height'), defaults.height),
    count: positiveInteger(params.get('count'), 20),
    trackView: params.get('trackView') !== 'false',
    fixture,
    runType,
    appendFrom: params.has('appendFrom')
      ? positiveInteger(params.get('appendFrom'), 0) || undefined
      : undefined,
  };
}

export { viewportDefaults };
