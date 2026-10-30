import { json } from "../router";
import type { RateLimiter } from "../ratelimit";

const MAX_BODY = 4096;
const badRequest = () => json(400, { reason: "bad_request" });

/** Rate limit by client IP; returns a 429 response when over the limit. Runs before any KV or Patreon call. */
export async function limited(req: Request, limiter: RateLimiter): Promise<Response | null> {
  const ip = req.headers.get("cf-connecting-ip") ?? "unknown";
  return (await limiter.limit(ip)).success ? null : json(429, { reason: "rate_limited" });
}

/** A JSON object body of at most 4 KiB, or a 400 response. */
export async function readJson(req: Request): Promise<Record<string, unknown> | Response> {
  if (!(req.headers.get("content-type") ?? "").includes("application/json")) return badRequest();
  const text = await req.text();
  if (new TextEncoder().encode(text).length > MAX_BODY) return badRequest();
  try {
    const o: unknown = JSON.parse(text);
    return typeof o === "object" && o !== null && !Array.isArray(o) ? (o as Record<string, unknown>) : badRequest();
  } catch {
    return badRequest();
  }
}
