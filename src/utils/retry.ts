export interface RetryOptions {
  maxAttempts: number;
  baseDelayMs: number;
  maxDelayMs: number;
  jitterMs: number;
  delayOverride?: (error: unknown, attempt: number) => number;
}

const DEFAULT_RETRY: RetryOptions = {
  maxAttempts: 4,
  baseDelayMs: 300,
  maxDelayMs: 4000,
  jitterMs: 120,
};

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function backoffMs(attempt: number, options: RetryOptions): number {
  const raw = Math.min(options.maxDelayMs, options.baseDelayMs * (2 ** (attempt - 1)));
  const jitter = Math.floor(Math.random() * options.jitterMs);
  return raw + jitter;
}

function readHeaderSeconds(response: Response, headerName: string): number | null {
  const value = response.headers.get(headerName);
  if (!value) {
    return null;
  }
  const parsed = Number(value);
  if (Number.isFinite(parsed) && parsed > 0) {
    return parsed;
  }
  return null;
}

export function retryAfterWaitMs(response: Response, nowMs = Date.now()): number | null {
  const value = response.headers.get('retry-after')?.trim();
  if (!value) {
    return null;
  }

  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    return seconds * 1000;
  }

  const retryAt = Date.parse(value);
  if (!Number.isFinite(retryAt)) {
    return null;
  }

  const waitMs = retryAt - nowMs;
  return waitMs > 0 ? waitMs : null;
}

function rateLimitWaitMs(response: Response): number | null {
  const resetEpoch = readHeaderSeconds(response, 'x-rate-limit-reset');
  if (resetEpoch) {
    const nowSeconds = Math.floor(Date.now() / 1000);
    const delta = resetEpoch - nowSeconds;
    if (delta > 0) {
      return delta * 1000;
    }
  }

  return retryAfterWaitMs(response);
}

export async function runWithRetry<T>(
  action: (attempt: number) => Promise<T>,
  shouldRetry: (error: unknown) => boolean,
  options?: Partial<RetryOptions>,
): Promise<T> {
  const merged = { ...DEFAULT_RETRY, ...(options ?? {}) };
  let lastError: unknown;

  for (let attempt = 1; attempt <= merged.maxAttempts; attempt += 1) {
    try {
      return await action(attempt);
    } catch (error) {
      lastError = error;
      if (attempt >= merged.maxAttempts || !shouldRetry(error)) {
        throw error;
      }
      const delay = merged.delayOverride
        ? merged.delayOverride(error, attempt)
        : backoffMs(attempt, merged);
      await sleep(delay);
    }
  }

  throw lastError;
}

export class HttpError extends Error {
  response: Response;
  body: string;
  rateLimitWaitMs: number | null;

  constructor(message: string, response: Response, body: string, rateLimitWaitMs?: number | null) {
    super(message);
    this.name = 'HttpError';
    this.response = response;
    this.body = body;
    this.rateLimitWaitMs = rateLimitWaitMs ?? null;
  }
}

export async function fetchWithRetry(
  input: string,
  init: RequestInit,
  options?: Partial<RetryOptions>,
): Promise<Response> {
  const merged = { ...DEFAULT_RETRY, ...(options ?? {}) };

  return await runWithRetry(
    async () => {
      const response = await fetch(input, init);
      if (response.status === 429) {
        const wait = rateLimitWaitMs(response);
        const body = await response.text();
        throw new HttpError(`Rate limited: ${response.status}`, response, body, wait);
      }

      if (response.status >= 500) {
        const body = await response.text();
        throw new HttpError(`Server error: ${response.status}`, response, body);
      }

      return response;
    },
    (error) => {
      if (error instanceof HttpError) {
        const status = error.response.status;
        return status === 429 || status >= 500;
      }
      return error instanceof TypeError;
    },
    {
      ...merged,
      delayOverride: (error, attempt) => {
        if (error instanceof HttpError && error.rateLimitWaitMs) {
          return error.rateLimitWaitMs;
        }
        return backoffMs(attempt, merged);
      },
    },
  );
}
