export type PostReadGeometry = {
  postTopDoc: number;
  postHeight: number;
  initialViewportBottomDoc: number;
};

export type PostReadCurrentGeometry = {
  postTopDoc: number;
  postHeight: number;
  currentViewportBottomDoc: number;
};

export type PostReadSnapshot = {
  foregroundTimeMS: number;
  scrollProgressPercent: number;
  finished: boolean;
};

export type PostReadEndPayload = {
  foreground_time_ms: number;
  scroll_progress_percent: number;
  exit_type: string;
};

export type PostReadClock = () => number;

const clamp = (value: number, minimum: number, maximum: number) =>
  Math.min(maximum, Math.max(minimum, value));

const defaultClock: PostReadClock = () =>
  typeof performance !== 'undefined' ? performance.now() : Date.now();

export const createPostReadGeometry = (
  rect: Pick<DOMRect, 'top' | 'height'>,
  scrollY: number,
  viewportHeight: number,
): PostReadGeometry => {
  const postHeight = Math.max(rect.height, 1);
  const postTopDoc = scrollY + rect.top;
  return {
    postTopDoc,
    postHeight,
    initialViewportBottomDoc: scrollY + viewportHeight,
  };
};

export class PostReadTracker {
  private geometry: PostReadCurrentGeometry | null = null;
  private anchorProgress = 0;
  private anchorReadHeadPx = 0;
  private anchorRemainingPx = 0;
  private maxProgress = 0;
  private foregroundTimeMS = 0;
  private foregroundStartedAt: number | null = null;
  private finished = false;

  constructor(private readonly clock: PostReadClock = defaultClock) {}

  start(geometry: PostReadGeometry, visible = true) {
    this.geometry = this.normalizeGeometry({
      ...geometry,
      currentViewportBottomDoc: geometry.initialViewportBottomDoc,
    });
    this.anchorProgress = 0;
    this.anchorReadHeadPx = clamp(
      geometry.initialViewportBottomDoc - this.geometry.postTopDoc,
      0,
      this.geometry.postHeight,
    );
    this.anchorRemainingPx = Math.max(
      0,
      this.geometry.postHeight - this.anchorReadHeadPx,
    );
    this.maxProgress = 0;
    this.foregroundTimeMS = 0;
    this.foregroundStartedAt = null;
    this.finished = false;
    if (visible) {
      this.resume();
    }
  }

  updateGeometry(geometry: PostReadCurrentGeometry) {
    if (this.finished) {
      return;
    }

    this.anchorProgress = this.maxProgress;
    this.geometry = this.normalizeGeometry(geometry);
    this.anchorReadHeadPx = clamp(
      geometry.currentViewportBottomDoc - this.geometry.postTopDoc,
      0,
      this.geometry.postHeight,
    );
    this.anchorRemainingPx = Math.max(
      0,
      this.geometry.postHeight - this.anchorReadHeadPx,
    );
  }

  recordScroll(currentViewportBottomDoc: number) {
    if (this.finished || !this.geometry) {
      return;
    }
    const currentReadHeadPx = clamp(
      currentViewportBottomDoc - this.geometry.postTopDoc,
      0,
      this.geometry.postHeight,
    );
    const advancedPx = Math.max(0, currentReadHeadPx - this.anchorReadHeadPx);
    const segmentProgress = this.anchorRemainingPx <= 0
      ? 0
      : (advancedPx / this.anchorRemainingPx) * (100 - this.anchorProgress);
    const candidateProgress = clamp(
      this.anchorProgress + segmentProgress,
      this.anchorProgress,
      100,
    );
    this.maxProgress = Math.max(this.maxProgress, Math.round(candidateProgress));
  }

  pause(at = this.clock()) {
    if (this.foregroundStartedAt === null || this.finished) {
      return;
    }
    this.foregroundTimeMS += Math.max(0, at - this.foregroundStartedAt);
    this.foregroundStartedAt = null;
  }

  resume(at = this.clock()) {
    if (this.finished || this.foregroundStartedAt !== null) {
      return;
    }
    this.foregroundStartedAt = at;
  }

  snapshot(at = this.clock()): PostReadSnapshot {
    return {
      foregroundTimeMS: this.currentForegroundTimeMS(at),
      scrollProgressPercent: this.maxProgress,
      finished: this.finished,
    };
  }

  finish(exitType: string, at = this.clock()): PostReadEndPayload | null {
    if (this.finished) {
      return null;
    }
    this.pause(at);
    this.finished = true;
    return {
      foreground_time_ms: Math.max(0, Math.round(this.foregroundTimeMS)),
      scroll_progress_percent: this.maxProgress,
      exit_type: exitType,
    };
  }

  isFinished() {
    return this.finished;
  }

  private currentForegroundTimeMS(at: number) {
    if (this.foregroundStartedAt === null) {
      return Math.max(0, this.foregroundTimeMS);
    }
    return Math.max(0, this.foregroundTimeMS + at - this.foregroundStartedAt);
  }

  private normalizeGeometry(geometry: {
    postTopDoc: number;
    postHeight: number;
    currentViewportBottomDoc?: number;
  }): PostReadCurrentGeometry {
    const currentViewportBottomDoc = geometry.currentViewportBottomDoc;
    return {
      postTopDoc: Number.isFinite(geometry.postTopDoc) ? geometry.postTopDoc : 0,
      postHeight: Math.max(
        Number.isFinite(geometry.postHeight) ? geometry.postHeight : 1,
        1,
      ),
      currentViewportBottomDoc: typeof currentViewportBottomDoc === 'number'
        && Number.isFinite(currentViewportBottomDoc)
        ? currentViewportBottomDoc
        : 0,
    };
  }
}
