import { describe, expect, it } from 'vitest';
import { parsePerfScenarioConfig } from './scenarioConfig';

describe('performance scenario config', () => {
  it('parses orchestration identity and every scenario field', () => {
    expect(parsePerfScenarioConfig(
      '?scenario=1&suiteId=suite-1&runId=run-2&viewport=mobile&width=392&height=840'
      + '&count=100&trackView=false&fixture=text-only&runType=append&appendFrom=80',
    )).toEqual({
      suiteId: 'suite-1',
      runId: 'run-2',
      viewport: 'mobile',
      width: 392,
      height: 840,
      count: 100,
      trackView: false,
      fixture: 'text-only',
      runType: 'append',
      appendFrom: 80,
    });
  });

  it('leaves missing orchestration IDs empty instead of accepting a standalone run', () => {
    const config = parsePerfScenarioConfig('?scenario=1');
    expect(config.suiteId).toBe('');
    expect(config.runId).toBe('');
  });
});
