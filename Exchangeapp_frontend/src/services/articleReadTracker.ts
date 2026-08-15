export type ArticleReadGeometry = {
  articleTopDoc: number;
  articleHeight: number;
  initialViewportBottomDoc: number;
};

export type ArticleReadCurrentGeometry = {
  articleTopDoc: number;
  articleHeight: number;
  currentViewportBottomDoc: number;
};

export type ArticleReadSnapshot = {
  foregroundTimeMS: number;
  scrollProgressPercent: number;
  finished: boolean;
};

export type ArticleReadEndPayload = {
  foreground_time_ms: number;
  scroll_progress_percent: number;
  exit_type: string;
};

export type ArticleReadClock = () => number;

const clamp = (value: number, minimum: number, maximum: number) =>
  Math.min(maximum, Math.max(minimum, value));

const defaultClock: ArticleReadClock = () =>
  typeof performance !== 'undefined' ? performance.now() : Date.now();

export const createArticleReadGeometry = (
  rect: Pick<DOMRect, 'top' | 'height'>,
  scrollY: number,
  viewportHeight: number,
): ArticleReadGeometry => {
  const articleHeight = Math.max(rect.height, 1);
  const articleTopDoc = scrollY + rect.top;
  return {
    articleTopDoc,
    articleHeight,
    initialViewportBottomDoc: scrollY + viewportHeight,
  };
};

export class ArticleReadTracker {
  private geometry: ArticleReadCurrentGeometry | null = null;
  private anchorProgress = 0;
  private anchorReadHeadPx = 0;
  private anchorRemainingPx = 0;
  private maxProgress = 0;
  private foregroundTimeMS = 0;
  private foregroundStartedAt: number | null = null;
  private finished = false;

  constructor(private readonly clock: ArticleReadClock = defaultClock) {}

  start(geometry: ArticleReadGeometry, visible = true) {
    this.geometry = this.normalizeGeometry({
      ...geometry,
      currentViewportBottomDoc: geometry.initialViewportBottomDoc,
    });
    this.anchorProgress = 0;
    this.anchorReadHeadPx = clamp(
      geometry.initialViewportBottomDoc - this.geometry.articleTopDoc,
      0,
      this.geometry.articleHeight,
    );
    this.anchorRemainingPx = Math.max(
      0,
      this.geometry.articleHeight - this.anchorReadHeadPx,
    );
    this.maxProgress = 0;
    this.foregroundTimeMS = 0;
    this.foregroundStartedAt = null;
    this.finished = false;
    if (visible) {
      this.resume();
    }
  }

  updateGeometry(geometry: ArticleReadCurrentGeometry) {
    if (this.finished) {
      return;
    }

    this.anchorProgress = this.maxProgress;
    this.geometry = this.normalizeGeometry(geometry);
    this.anchorReadHeadPx = clamp(
      geometry.currentViewportBottomDoc - this.geometry.articleTopDoc,
      0,
      this.geometry.articleHeight,
    );
    this.anchorRemainingPx = Math.max(
      0,
      this.geometry.articleHeight - this.anchorReadHeadPx,
    );
  }

  recordScroll(currentViewportBottomDoc: number) {
    if (this.finished || !this.geometry) {
      return;
    }
    const currentReadHeadPx = clamp(
      currentViewportBottomDoc - this.geometry.articleTopDoc,
      0,
      this.geometry.articleHeight,
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

  snapshot(at = this.clock()): ArticleReadSnapshot {
    return {
      foregroundTimeMS: this.currentForegroundTimeMS(at),
      scrollProgressPercent: this.maxProgress,
      finished: this.finished,
    };
  }

  finish(exitType: string, at = this.clock()): ArticleReadEndPayload | null {
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
    articleTopDoc: number;
    articleHeight: number;
    currentViewportBottomDoc?: number;
  }): ArticleReadCurrentGeometry {
    const currentViewportBottomDoc = geometry.currentViewportBottomDoc;
    return {
      articleTopDoc: Number.isFinite(geometry.articleTopDoc) ? geometry.articleTopDoc : 0,
      articleHeight: Math.max(
        Number.isFinite(geometry.articleHeight) ? geometry.articleHeight : 1,
        1,
      ),
      currentViewportBottomDoc: typeof currentViewportBottomDoc === 'number'
        && Number.isFinite(currentViewportBottomDoc)
        ? currentViewportBottomDoc
        : 0,
    };
  }
}