// @vitest-environment jsdom

import { describe, expect, it } from 'vitest';
import router from './index';

describe('History route', () => {
  it('registers the private history surface in the app layout', () => {
    const route = router.getRoutes().find(item => item.name === 'History');
    expect(route?.path).toBe('/history');
    expect(route?.meta.layout).toBe('app');
  });
});
