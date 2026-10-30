import { json } from "../router";
import type { RateLimiter } from "../ratelimit";

const MAX_BODY = 4096;
const badRequest = () => json(400, { reason: "bad_request" });

/** Rate limit by client IP; returns a 429 response when over the limit. Runs before any KV or Patreon call. */
export async function limited(req: Request, limiter: RateLimiter): Promise<Response | null> {
  const ip = req.headers.get("cf-connecting-ip") ?? "unknown";
  return (await limiter.limit(ip)).success ? null : json(429, { reason: "rate_limited" });
}

/** Reads at most MAX_BODY bytes, cancelling the stream as soon as the cap is passed; null when over. */
async function readCapped(req: Request): Promise<Uint8Array | null> {
  const declared = req.headers.get("content-length");
  if (declared !== null && Number(declared) > MAX_BODY) return null;
  if (!req.body) return new Uint8Array();
  const reader = req.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.length;
    if (total > MAX_BODY) {
      await reader.cancel().catch(() => {});
      return null;
    }
    chunks.push(value);
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) { out.set(c, off); off += c.length; }
  return out;
}

/** A JSON object body of at most 4 KiB, or a 400 response. */
export async function readJson(req: Request): Promise<Record<string, unknown> | Response> {
  if (!(req.headers.get("content-type") ?? "").includes("application/json")) return badRequest();
  const bytes = await readCapped(req);
  if (bytes === null) return badRequest();
  try {
    const o: unknown = JSON.parse(new TextDecoder().decode(bytes));
    return typeof o === "object" && o !== null && !Array.isArray(o) ? (o as Record<string, unknown>) : badRequest();
  } catch {
    return badRequest();
  }
}
