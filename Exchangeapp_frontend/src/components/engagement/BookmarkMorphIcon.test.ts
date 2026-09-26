// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import BookmarkMorphIcon from './BookmarkMorphIcon.vue';

type TimelineOptions = {
  onComplete?: () => void;
};

type MockTimeline = {
  options: TimelineOptions;
  set: ReturnType<typeof vi.fn>;
  to: ReturnType<typeof vi.fn>;
  kill: ReturnType<typeof vi.fn>;
};

const gsapMocks = vi.hoisted(() => {
  type HoistedTimeline = {
    options: { onComplete?: () => void };
    set: ReturnType<typeof vi.fn>;
    to: ReturnType<typeof vi.fn>;
    kill: ReturnType<typeof vi.fn>;
  };

  const timelines: HoistedTimeline[] = [];
  const applySet = (targets: unknown, vars: Record<string, unknown>) => {
    const targetList = Array.isArray(targets) ? targets : [targets];

    targetList.forEach(target => {
      if (!target || typeof target !== 'object' || !('style' in target)) {
        return;
      }

      const element = target as SVGElement;
      if ('opacity' in vars) {
        element.style.opacity = String(vars.opacity);
      }
      if (vars.clearProps === 'transform') {
        element.style.removeProperty('transform');
      }
    });
  };
  const set = vi.fn(applySet);
  const timeline = vi.fn((options: { onComplete?: () => void } = {}) => {
    const instance = {} as HoistedTimeline;
    instance.options = options;
    instance.set = vi.fn((targets: unknown, vars: Record<string, unknown>) => {
      applySet(targets, vars);
      return instance;
    });
    instance.to = vi.fn(() => instance);
    instance.kill = vi.fn();
    timelines.push(instance);
    return instance;
  });

  return {
    timelines,
    set,
    timeline,
    registerPlugin: vi.fn(),
  };
});

vi.mock('gsap', () => ({
  gsap: {
    set: gsapMocks.set,
    timeline: gsapMocks.timeline,
    registerPlugin: gsapMocks.registerPlugin,
  },
}));

vi.mock('gsap/MorphSVGPlugin', () => ({
  MorphSVGPlugin: { name: 'MorphSVGPlugin' },
}));

const setReducedMotion = (matches: boolean) => {
  vi.stubGlobal('matchMedia', vi.fn((media: string) => ({
    matches,
    media,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  } satisfies Partial<MediaQueryList>)));
};

const mountMorphIcon = (props: Partial<{
  size: number;
  bookmarked: boolean;
  motion: 'idle' | 'bookmarking' | 'unbookmarking';
}> = {}) => mount(BookmarkMorphIcon, {
  props: {
    size: 18,
    bookmarked: false,
    motion: 'idle',
    ...props,
  },
});

const getPaths = (wrapper: ReturnType<typeof mountMorphIcon>) => ({
  svg: wrapper.get('svg'),
  outline: wrapper.get('.bookmark-morph-icon__outline').element as SVGPathElement,
  filled: wrapper.get('.bookmark-morph-icon__filled').element as SVGPathElement,
  ribbon: wrapper.get('.bookmark-morph-icon__ribbon').element as SVGPathElement,
});

const getMorphTweens = (timeline: MockTimeline) => timeline.to.mock.calls
  .map(([, vars]) => vars as Record<string, unknown>)
  .filter(vars => typeof vars.morphSVG === 'string');

const getTimelineEnd = (timeline: MockTimeline) => Math.max(...timeline.to.mock.calls.map(([, vars, position]) => (
  Number(position ?? 0) + Number((vars as Record<string, unknown>).duration ?? 0)
)));

beforeEach(() => {
  gsapMocks.timelines.splice(0);
  gsapMocks.set.mockClear();
  gsapMocks.timeline.mockClear();
  setReducedMotion(false);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('BookmarkMorphIcon', () => {
  it('renders an 18px canonical unbookmarked SVG without animating on mount', () => {
    const wrapper = mountMorphIcon();
    const { svg, outline, filled, ribbon } = getPaths(wrapper);

    expect(svg.attributes()).toMatchObject({
      width: '18',
      height: '18',
      viewBox: '0 0 36 36',
      'aria-hidden': 'true',
      focusable: 'false',
      'data-motion': 'idle',
    });
    expect(outline.style.opacity).toBe('1');
    expect(filled.style.opacity).toBe('0');
    expect(ribbon.style.opacity).toBe('0');
    expect(ribbon.getAttribute('d')).not.toBe(filled.getAttribute('d'));
    expect(gsapMocks.timeline).not.toHaveBeenCalled();

    wrapper.unmount();
  });

  it('snaps an initially bookmarked detail icon to canonical shape without a timeline', () => {
    const wrapper = mountMorphIcon({ size: 20, bookmarked: true });
    const { svg, outline, filled, ribbon } = getPaths(wrapper);

    expect(svg.attributes()).toMatchObject({ width: '20', height: '20' });
    expect(outline.style.opacity).toBe('0');
    expect(filled.style.opacity).toBe('1');
    expect(ribbon.style.opacity).toBe('0');
    expect(ribbon.getAttribute('d')).toBe(filled.getAttribute('d'));
    expect(gsapMocks.timeline).not.toHaveBeenCalled();

    wrapper.unmount();
  });

  it('snaps prop-only bookmark changes without creating a morph timeline', async () => {
    const wrapper = mountMorphIcon();

    await wrapper.setProps({ bookmarked: true });
    expect(getPaths(wrapper).filled.style.opacity).toBe('1');
    expect(gsapMocks.timeline).not.toHaveBeenCalled();

    await wrapper.setProps({ bookmarked: false });
    expect(getPaths(wrapper).outline.style.opacity).toBe('1');
    expect(getPaths(wrapper).filled.style.opacity).toBe('0');
    expect(gsapMocks.timeline).not.toHaveBeenCalled();

    wrapper.unmount();
  });

  it('plays a multi-stage 540ms bookmark-in geometry morph', async () => {
    const wrapper = mountMorphIcon();
    const initialRibbonPath = getPaths(wrapper).ribbon.getAttribute('d');

    await wrapper.setProps({ bookmarked: true, motion: 'bookmarking' });

    expect(gsapMocks.timeline).toHaveBeenCalledTimes(1);
    const timeline = gsapMocks.timelines[0];
    expect(getMorphTweens(timeline)).toHaveLength(5);
    expect(getTimelineEnd(timeline)).toBeCloseTo(0.54, 4);
    expect(getPaths(wrapper).outline.style.opacity).toBe('1');
    expect(getPaths(wrapper).filled.style.opacity).toBe('0');
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('1');

    timeline.options.onComplete?.();
    expect(getPaths(wrapper).outline.style.opacity).toBe('0');
    expect(getPaths(wrapper).filled.style.opacity).toBe('1');
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('0');
    expect(getPaths(wrapper).ribbon.getAttribute('d')).toBe(getPaths(wrapper).filled.getAttribute('d'));
    expect(initialRibbonPath).not.toBe(getPaths(wrapper).ribbon.getAttribute('d'));

    wrapper.unmount();
  });

  it('plays a distinct 300ms unbookmark geometry morph', async () => {
    const wrapper = mountMorphIcon({ bookmarked: true });

    await wrapper.setProps({ bookmarked: false, motion: 'unbookmarking' });

    expect(gsapMocks.timeline).toHaveBeenCalledTimes(1);
    const timeline = gsapMocks.timelines[0];
    expect(getMorphTweens(timeline)).toHaveLength(3);
    expect(getTimelineEnd(timeline)).toBeCloseTo(0.3, 4);
    expect(getPaths(wrapper).outline.style.opacity).toBe('0');
    expect(getPaths(wrapper).filled.style.opacity).toBe('1');

    timeline.options.onComplete?.();
    expect(getPaths(wrapper).outline.style.opacity).toBe('1');
    expect(getPaths(wrapper).filled.style.opacity).toBe('0');
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('0');

    wrapper.unmount();
  });

  it('kills bookmark-in motion and snaps immediately on rollback', async () => {
    const wrapper = mountMorphIcon();
    await wrapper.setProps({ bookmarked: true, motion: 'bookmarking' });
    const timeline = gsapMocks.timelines[0];

    await wrapper.setProps({ bookmarked: false, motion: 'idle' });

    expect(timeline.kill).toHaveBeenCalledTimes(1);
    expect(getPaths(wrapper).outline.style.opacity).toBe('1');
    expect(getPaths(wrapper).filled.style.opacity).toBe('0');
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('0');

    wrapper.unmount();
  });

  it('kills unbookmark motion and snaps immediately on reverse rollback', async () => {
    const wrapper = mountMorphIcon({ bookmarked: true });
    await wrapper.setProps({ bookmarked: false, motion: 'unbookmarking' });
    const timeline = gsapMocks.timelines[0];

    await wrapper.setProps({ bookmarked: true, motion: 'idle' });

    expect(timeline.kill).toHaveBeenCalledTimes(1);
    expect(getPaths(wrapper).outline.style.opacity).toBe('0');
    expect(getPaths(wrapper).filled.style.opacity).toBe('1');
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('0');

    wrapper.unmount();
  });

  it('kills an active timeline before starting a different motion', async () => {
    const wrapper = mountMorphIcon();
    await wrapper.setProps({ bookmarked: true, motion: 'bookmarking' });
    const firstTimeline = gsapMocks.timelines[0];

    await wrapper.setProps({ bookmarked: false, motion: 'unbookmarking' });

    expect(firstTimeline.kill).toHaveBeenCalledTimes(1);
    expect(gsapMocks.timeline).toHaveBeenCalledTimes(2);
    expect(getMorphTweens(gsapMocks.timelines[1])).toHaveLength(3);

    wrapper.unmount();
  });

  it.each([
    ['bookmarking', true, '0', '1'],
    ['unbookmarking', false, '1', '0'],
  ] as const)('snaps immediately for reduced-motion %s', async (motion, bookmarked, outlineOpacity, filledOpacity) => {
    setReducedMotion(true);
    const wrapper = mountMorphIcon({ bookmarked: !bookmarked });

    await wrapper.setProps({ bookmarked, motion });

    expect(gsapMocks.timeline).not.toHaveBeenCalled();
    expect(getPaths(wrapper).outline.style.opacity).toBe(outlineOpacity);
    expect(getPaths(wrapper).filled.style.opacity).toBe(filledOpacity);
    expect(getPaths(wrapper).ribbon.style.opacity).toBe('0');

    wrapper.unmount();
  });

  it('kills an active timeline when unmounted', async () => {
    const wrapper = mountMorphIcon();
    await wrapper.setProps({ bookmarked: true, motion: 'bookmarking' });
    const timeline = gsapMocks.timelines[0];

    wrapper.unmount();

    expect(timeline.kill).toHaveBeenCalledTimes(1);
  });
});
