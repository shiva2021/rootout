export interface RateLimiter {
  limit(key: string): Promise<{ success: boolean }>;
}

/** Wraps Cloudflare's Workers rate-limit binding (`env.RATE`). */
export function bindingLimiter(binding: { limit(opts: { key: string }): Promise<{ success: boolean }> }): RateLimiter {
  return { limit: (key) => binding.limit({ key }) };
}

/** Per-isolate sliding window, used when no binding is configured (tests, local dev). */
export function memoryLimiter(limit: number, periodSeconds: number, now: () => number = () => Date.now() / 1000): RateLimiter {
  const hits = new Map<string, number[]>();
  return {
    async limit(key) {
      const t = now();
      const recent = (hits.get(key) ?? []).filter((x) => x > t - periodSeconds);
      if (recent.length >= limit) {
        hits.set(key, recent);
        return { success: false };
      }
      recent.push(t);
      hits.set(key, recent);
      return { success: true };
    },
  };
}
