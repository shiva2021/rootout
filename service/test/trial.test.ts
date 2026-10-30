import { beforeEach, describe, expect, it } from "vitest";
import golden from "./fixtures/golden.json";
import { memoryLimiter } from "../src/ratelimit";
import { trialHandler } from "../src/routes/trial";
import { importPublicKey, verify } from "../src/sign";
import { env } from "./helpers";

const mid = "0123456789abcdef0123456789abcdef";
const NOW = 1_800_000_000;
const ctx = { waitUntil: () => {}, passThroughOnException: () => {} } as unknown as ExecutionContext;

async function call(body: unknown, limiter = memoryLimiter(5, 60), type = "application/json") {
  const req = new Request("https://keys.test/trial", {
    method: "POST",
    headers: { "content-type": type, "cf-connecting-ip": "1.2.3.4" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
  const res = await trialHandler({ now: () => NOW, limiter })(req, env, ctx);
  return { res, body: (await res.json()) as Record<string, string> };
}

beforeEach(async () => {
  for (const { name } of (await env.KEYS.list()).keys) await env.KEYS.delete(name);
});

describe("trial", () => {
  it("first call issues 7-day key and marks KV", async () => {
    const { res, body } = await call({ mid });
    expect(res.status).toBe(200);
    const c = await verify(body.key, await importPublicKey(golden.pub_hex));
    expect(c).toEqual({ v: 1, kind: "trial", sub: "trial", mid, iat: NOW, exp: NOW + 7 * 86400 });
    expect(JSON.parse((await env.KEYS.get(`trial:${mid}`))!)).toEqual({ iat: NOW });
  });

  it("second call → 409", async () => {
    await call({ mid });
    const { res, body } = await call({ mid });
    expect([res.status, body.reason]).toEqual([409, "trial_used"]);
  });

  it("bad mid → 400", async () => {
    for (const b of [{ mid: "short" }, { mid: 5 }, {}, "nope"]) {
      const { res, body } = await call(b);
      expect([res.status, body.reason]).toEqual([400, "bad_request"]);
    }
    expect(await env.KEYS.get(`trial:${mid}`)).toBeNull();
  });

  it("non-JSON content type → 400", async () => {
    expect((await call({ mid }, undefined, "text/plain")).res.status).toBe(400);
  });

  it("sixth call in a minute → 429 without touching KV", async () => {
    const limiter = memoryLimiter(5, 60, () => NOW);
    for (let i = 0; i < 5; i++) {
      expect((await call({ mid: i.toString(16).repeat(32) }, limiter)).res.status).toBe(200);
    }
    const { res, body } = await call({ mid }, limiter);
    expect([res.status, body.reason]).toEqual([429, "rate_limited"]);
    expect(await env.KEYS.get(`trial:${mid}`)).toBeNull();
  });
});
