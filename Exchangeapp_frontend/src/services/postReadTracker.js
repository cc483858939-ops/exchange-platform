const clamp = (value, minimum, maximum) => Math.min(maximum, Math.max(minimum, value));
const defaultClock = () => typeof performance !== 'undefined' ? performance.now() : Date.now();
export const createPostReadGeometry = (rect, scrollY, viewportHeight) => {
    const postHeight = Math.max(rect.height, 1);
    const postTopDoc = scrollY + rect.top;
    return {
        postTopDoc,
        postHeight,
        initialViewportBottomDoc: scrollY + viewportHeight,
    };
};
export class PostReadTracker {
    clock;
    geometry = null;
    anchorProgress = 0;
    anchorReadHeadPx = 0;
    anchorRemainingPx = 0;
    maxProgress = 0;
    foregroundTimeMS = 0;
    foregroundStartedAt = null;
    finished = false;
    constructor(clock = defaultClock) {
        this.clock = clock;
    }
    start(geometry, visible = true) {
        this.geometry = this.normalizeGeometry({
            ...geometry,
            currentViewportBottomDoc: geometry.initialViewportBottomDoc,
        });
        this.anchorProgress = 0;
        this.anchorReadHeadPx = clamp(geometry.initialViewportBottomDoc - this.geometry.postTopDoc, 0, this.geometry.postHeight);
        this.anchorRemainingPx = Math.max(0, this.geometry.postHeight - this.anchorReadHeadPx);
        this.maxProgress = 0;
        this.foregroundTimeMS = 0;
        this.foregroundStartedAt = null;
        this.finished = false;
        if (visible) {
            this.resume();
        }
    }
    updateGeometry(geometry) {
        if (this.finished) {
            return;
        }
        this.anchorProgress = this.maxProgress;
        this.geometry = this.normalizeGeometry(geometry);
        this.anchorReadHeadPx = clamp(geometry.currentViewportBottomDoc - this.geometry.postTopDoc, 0, this.geometry.postHeight);
        this.anchorRemainingPx = Math.max(0, this.geometry.postHeight - this.anchorReadHeadPx);
    }
    recordScroll(currentViewportBottomDoc) {
        if (this.finished || !this.geometry) {
            return;
        }
        const currentReadHeadPx = clamp(currentViewportBottomDoc - this.geometry.postTopDoc, 0, this.geometry.postHeight);
        const advancedPx = Math.max(0, currentReadHeadPx - this.anchorReadHeadPx);
        const segmentProgress = this.anchorRemainingPx <= 0
            ? 0
            : (advancedPx / this.anchorRemainingPx) * (100 - this.anchorProgress);
        const candidateProgress = clamp(this.anchorProgress + segmentProgress, this.anchorProgress, 100);
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
    snapshot(at = this.clock()) {
        return {
            foregroundTimeMS: this.currentForegroundTimeMS(at),
            scrollProgressPercent: this.maxProgress,
            finished: this.finished,
        };
    }
    finish(exitType, at = this.clock()) {
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
    currentForegroundTimeMS(at) {
        if (this.foregroundStartedAt === null) {
            return Math.max(0, this.foregroundTimeMS);
        }
        return Math.max(0, this.foregroundTimeMS + at - this.foregroundStartedAt);
    }
    normalizeGeometry(geometry) {
        const currentViewportBottomDoc = geometry.currentViewportBottomDoc;
        return {
            postTopDoc: Number.isFinite(geometry.postTopDoc) ? geometry.postTopDoc : 0,
            postHeight: Math.max(Number.isFinite(geometry.postHeight) ? geometry.postHeight : 1, 1),
            currentViewportBottomDoc: typeof currentViewportBottomDoc === 'number'
                && Number.isFinite(currentViewportBottomDoc)
                ? currentViewportBottomDoc
                : 0,
        };
    }
}
