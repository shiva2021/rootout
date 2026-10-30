import { env as workerEnv } from "cloudflare:workers";
import type { Env } from "../src/env";

// The Miniflare bindings from vitest.config.ts plus the KEYS namespace from wrangler.toml.
export const env = workerEnv as unknown as Env;

export interface Call { url: string; init: RequestInit }

/** A fetch stub that replays canned responses in order and records every call. */
export function stubFetch(...responses: Response[]): { fetchFn: typeof fetch; calls: Call[] } {
  const calls: Call[] = [];
  const queue = [...responses];
  const fetchFn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} });
    const r = queue.shift();
    if (!r) throw new Error("unexpected fetch: " + String(input));
    return r;
  }) as typeof fetch;
  return { fetchFn, calls };
}

export const jsonResponse = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
