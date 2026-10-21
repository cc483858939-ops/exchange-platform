export type TelemetryRetrySnapshot = {
  retryNotBeforeEpochMs: number;
  retryAttempt: number;
};

export type TelemetrySnapshot<TEvent> = {
  events: TEvent[];
  retry: TelemetryRetrySnapshot;
  ownerUserId?: number | null;
};

export const initialTelemetryRetrySnapshot = (): TelemetryRetrySnapshot => ({
  retryNotBeforeEpochMs: 0,
  retryAttempt: 0,
});

export const parseRetryAfterMS = (
  retryAfter: string | null | undefined,
  serverDate: string | null | undefined,
  clientNow = Date.now(),
): number | null => {
  const value = retryAfter?.trim();
  if (!value) return null;

  if (/^\d+$/.test(value)) {
    const seconds = Number(value);
    if (!Number.isSafeInteger(seconds) || seconds > Number.MAX_SAFE_INTEGER / 1000) {
      return null;
    }
    return seconds * 1000;
  }

  const target = Date.parse(value);
  if (!Number.isFinite(target)) return null;
  const parsedServerDate = serverDate ? Date.parse(serverDate) : Number.NaN;
  const reference = Number.isFinite(parsedServerDate) ? parsedServerDate : clientNow;
  const remaining = target - reference;
  return Number.isFinite(remaining) ? Math.max(0, remaining) : null;
};

export const readTelemetryHeader = (headers: unknown, name: string): string | null => {
  if (!headers || typeof headers !== 'object') return null;
  const candidate = headers as {
    get?: (headerName: string) => unknown;
    [key: string]: unknown;
  };
  if (typeof candidate.get === 'function') {
    const value = candidate.get(name);
    if (typeof value === 'string') return value;
  }
  const lowerName = name.toLowerCase();
  for (const [key, value] of Object.entries(candidate)) {
    if (key.toLowerCase() === lowerName && typeof value === 'string') return value;
  }
  return null;
};

export type TelemetryHTTPFailure = {
  status: number | null;
  data: unknown;
  retryAfter: string | null;
  date: string | null;
};

export const getTelemetryHTTPFailure = (error: unknown): TelemetryHTTPFailure => {
  const response = (error as {
    response?: {
      status?: unknown;
      data?: unknown;
      headers?: unknown;
    };
  } | null)?.response;
  const status = typeof response?.status === 'number' && Number.isFinite(response.status)
    ? response.status
    : null;
  return {
    status,
    data: response?.data,
    retryAfter: readTelemetryHeader(response?.headers, 'Retry-After'),
    date: readTelemetryHeader(response?.headers, 'Date'),
  };
};

const maxTimerDelayMS = 2_147_000_000;
const initialRetryWindowMS = 2_000;
const maxRetryWindowMS = 300_000;
const maxRetryExponent = 32;

export const calculateTelemetryRetryDelayMS = (
  attempt: number,
  random: () => number = Math.random,
): number => {
  const safeAttempt = Number.isFinite(attempt)
    ? Math.max(0, Math.min(Math.floor(attempt), maxRetryExponent))
    : 0;
  const windowMS = Math.min(initialRetryWindowMS * (2 ** safeAttempt), maxRetryWindowMS);
  const randomValue = random();
  const unit = Number.isFinite(randomValue) ? Math.min(1, Math.max(0, randomValue)) : 0.5;
  return Math.floor(windowMS / 2 + unit * windowMS / 2);
};

export class TelemetryRetryGate {
  private retryNotBeforeEpochMs = 0;
  private retryAttempt = 0;
  private retryEpoch = 0;
  private timer: number | null = null;
  private timerDueAt: number | null = null;
  private timerVersion = 0;

  constructor(
    private readonly onReady: () => void,
    private readonly onChange: () => void,
    private readonly now: () => number = Date.now,
    private readonly random: () => number = Math.random,
  ) {}

  snapshot(): TelemetryRetrySnapshot {
    return {
      retryNotBeforeEpochMs: this.retryNotBeforeEpochMs,
      retryAttempt: this.retryAttempt,
    };
  }

  restore(snapshot: unknown): void {
    const value = snapshot as Partial<TelemetryRetrySnapshot> | null;
    const deadline = value?.retryNotBeforeEpochMs;
    const attempt = value?.retryAttempt;
    this.retryNotBeforeEpochMs = typeof deadline === 'number'
      && Number.isSafeInteger(deadline) && deadline > 0
      ? deadline
      : 0;
    this.retryAttempt = typeof attempt === 'number' && Number.isSafeInteger(attempt) && attempt >= 0
      ? Math.min(attempt, maxRetryExponent + 1)
      : 0;
    this.retryEpoch += 1;
  }

  captureEpoch(): number {
    return this.retryEpoch;
  }

  canSend(now = this.now()): boolean {
    return now >= this.retryNotBeforeEpochMs;
  }

  request(delayMS = 0): void {
    const delay = Number.isFinite(delayMS) ? Math.max(0, delayMS) : 0;
    this.scheduleAt(Math.max(this.now() + delay, this.retryNotBeforeEpochMs));
  }

  recordFailure(retryAfter?: string | null, serverDate?: string | null): number {
    const now = this.now();
    const localDelay = calculateTelemetryRetryDelayMS(this.retryAttempt, this.random);
    const retryAfterMS = parseRetryAfterMS(retryAfter, serverDate, now) ?? 0;
    this.retryAttempt = Math.min(this.retryAttempt + 1, maxRetryExponent + 1);
    this.retryNotBeforeEpochMs = Math.max(
      this.retryNotBeforeEpochMs,
      now + localDelay,
      now + retryAfterMS,
    );
    this.retryEpoch += 1;
    this.scheduleAt(this.retryNotBeforeEpochMs);
    this.onChange();
    return this.retryNotBeforeEpochMs;
  }

  clearAfterSuccess(capturedEpoch: number): boolean {
    if (capturedEpoch !== this.retryEpoch) return false;
    this.cancelScheduled();
    this.retryNotBeforeEpochMs = 0;
    this.retryAttempt = 0;
    this.retryEpoch += 1;
    this.onChange();
    return true;
  }

  reset(): void {
    this.cancelScheduled();
    this.retryNotBeforeEpochMs = 0;
    this.retryAttempt = 0;
    this.retryEpoch += 1;
    this.onChange();
  }

  cancelScheduled(): void {
    this.timerVersion += 1;
    if (this.timer !== null) {
      window.clearTimeout(this.timer);
      this.timer = null;
    }
    this.timerDueAt = null;
  }

  private scheduleAt(dueAt: number): void {
    const normalized = Number.isFinite(dueAt) ? Math.max(this.now(), dueAt) : Number.MAX_SAFE_INTEGER;
    if (this.timer !== null && this.timerDueAt === normalized) return;
    this.cancelScheduled();
    this.timerDueAt = normalized;
    this.scheduleTimerSegment();
  }

  private scheduleTimerSegment(): void {
    if (this.timerDueAt === null) return;
    const version = ++this.timerVersion;
    const remaining = Math.max(0, this.timerDueAt - this.now());
    const delay = Math.min(remaining, maxTimerDelayMS);
    this.timer = window.setTimeout(() => {
      if (version !== this.timerVersion) return;
      this.timer = null;
      if (this.timerDueAt !== null && this.now() < this.timerDueAt) {
        this.scheduleTimerSegment();
        return;
      }
      this.timerDueAt = null;
      this.onReady();
    }, delay);
  }
}
