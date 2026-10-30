export interface Env {
  KEYS: KVNamespace;
  /** Workers rate-limit binding; absent in tests and local dev, where an in-memory limiter is used. */
  RATE?: { limit(opts: { key: string }): Promise<{ success: boolean }> };
  PATREON_CLIENT_ID: string;
  PATREON_CLIENT_SECRET: string;
  PATREON_CAMPAIGN_ID: string;
  SIGNING_KEY: string;
  STATE_SECRET: string;
  ALERT_GITHUB_TOKEN: string;
  PATREON_URL: string;
  ALERT_REPO: string;
  PUBLIC_ORIGIN: string;
}
