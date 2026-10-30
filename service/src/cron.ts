import { alert } from "./alert";
import type { Env } from "./env";
import { getCreator, putCreator } from "./kv";
import { refreshToken } from "./patreon";

const REFRESH_AFTER = 20 * 86400;

const detail = (env: Env, now: number, extra: string) =>
  `${extra}\n\nTime: ${new Date(now * 1000).toISOString()}\nWorker: ${env.PUBLIC_ORIGIN}`;

/**
 * Daily creator-token refresh. `now` is in seconds. The old pair survives any failure: putCreator keeps it as
 * creator_prev before writing creator, so one key is always usable.
 */
export async function refreshCreator(
  env: Env, now: number, fetchFn: typeof fetch = fetch,
): Promise<"refreshed" | "skipped" | "failed"> {
  try {
    const pair = await getCreator(env.KEYS);
    if (!pair) {
      await alert(env, "creator token missing", detail(env, now, "No creator token pair is stored. Re-authorise the creator account."));
      return "failed";
    }
    if (now - pair.updated < REFRESH_AFTER) return "skipped";
    const next = await refreshToken(env, pair.refresh, fetchFn);
    await putCreator(env.KEYS, { access: next.access, refresh: next.refresh, updated: now });
    return "refreshed";
  } catch (e) {
    await alert(env, "creator token refresh failed", detail(env, now, e instanceof Error ? e.message : String(e)));
    return "failed";
  }
}
