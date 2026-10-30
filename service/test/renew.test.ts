import { beforeEach, describe, expect, it, vi } from "vitest";
import golden from "./fixtures/golden.json";
import { renewHandler } from "../src/routes/renew";
import { memoryLimiter } from "../src/ratelimit";
import { type Claims, importPrivateKey, importPublicKey, sign, verify } from "../src/sign";
import { env, jsonResponse, stubFetch } from "./helpers";

const mid = "0123456789abcdef0123456789abcdef";
const NOW = 1_800_000_000;
const ctx = { waitUntil: () => {}, passThroughOnException: () => {} } as unknown as ExecutionContext;
const PKCS8_PREFIX = "302e020100300506032b657004220420";

const seedKey = (seedHex: string) =>
  importPrivateKey(btoa(String.fromCharCode(...Uint8Array.from((PKCS8_PREFIX + seedHex).match(/../g)!, (h) => parseInt(h, 16)))));
const ourKey = () => seedKey(golden.seed_hex);
const foreignKey = () => seedKey("11".repeat(32));

const patron: Claims = { v: 1, kind: "patron", sub: "u1", mid, iat: NOW - 86400, exp: NOW + 34 * 86400 };
const member = (status: string | null, cents = 500) =>
  jsonResponse(200, {
    data: {
      id: "m1", type: "member",
      attributes: { patron_status: status, currently_entitled_amount_cents: cents, is_gifted: false },
      relationships: { user: { data: { id: "u1" } } },
    },
  });

async function call(body: unknown, fetchFn: typeof fetch, opts: { ctx?: ExecutionContext; limiter?: ReturnType<typeof memoryLimiter>; ip?: string; raw?: string; type?: string } = {}) {
  const req = new Request("https://keys.test/renew", {
    method: "POST",
    headers: { "content-type": opts.type ?? "application/json", "cf-connecting-ip": opts.ip ?? "1.2.3.4" },
    body: opts.raw ?? JSON.stringify(body),
  });
  const res = await renewHandler({ fetchFn, now: () => NOW, limiter: opts.limiter ?? memoryLimiter(5, 60) })(req, env, opts.ctx ?? ctx);
  return { res, body: (await res.json()) as Record<string, string> };
}

beforeEach(async () => {
  for (const { name } of (await env.KEYS.list()).keys) await env.KEYS.delete(name);
  await env.KEYS.put("creator", JSON.stringify({ access: "ca", refresh: "cr", updated: 1 }));
  await env.KEYS.put("devices:u1", JSON.stringify([{ mid, iat: NOW - 86400 }]));
  await env.KEYS.put("member:u1", "m1");
});

describe("renew", () => {
  it("active member gets a fresh key with same sub and mid and performs no KV write", async () => {
    const put = vi.spyOn(env.KEYS, "put");
    const del = vi.spyOn(env.KEYS, "delete");
    const { fetchFn, calls } = stubFetch(member("active_patron"));
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, fetchFn);
    expect(res.status).toBe(200);
    const c = await verify(body.key, await importPublicKey(golden.pub_hex));
    expect(c).toMatchObject({ kind: "patron", sub: "u1", mid, iat: NOW, exp: NOW + 35 * 86400 });
    expect(calls[0].url).toContain("/members/m1");
    expect(put).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
    put.mockRestore();
    del.mockRestore();
    expect(JSON.parse((await env.KEYS.get("devices:u1"))!)).toEqual([{ mid, iat: NOW - 86400 }]);
  });

  it("sixth call in a minute from one IP → 429", async () => {
    const limiter = memoryLimiter(5, 60, () => NOW);
    const key = await sign(patron, await ourKey());
    for (let i = 0; i < 5; i++) {
      const { res } = await call({ key }, stubFetch(member("active_patron")).fetchFn, { limiter });
      expect(res.status).toBe(200);
    }
    const { fetchFn, calls } = stubFetch();
    const { res, body } = await call({ key }, fetchFn, { limiter });
    expect(res.status).toBe(429);
    expect(body.reason).toBe("rate_limited");
    expect(calls).toHaveLength(0);
    expect((await call({ key }, stubFetch(member("active_patron")).fetchFn, { limiter, ip: "9.9.9.9" })).res.status).toBe(200);
  });

  it("trial key → 403 not_renewable", async () => {
    const k = await sign({ ...patron, kind: "trial", sub: "trial" }, await ourKey());
    const { res, body } = await call({ key: k }, stubFetch().fetchFn);
    expect([res.status, body.reason]).toEqual([403, "not_renewable"]);
  });

  it("admin key → 403", async () => {
    const k = await sign({ v: 1, kind: "admin", sub: "admin", mid, iat: NOW }, await ourKey());
    const { res, body } = await call({ key: k }, stubFetch().fetchFn);
    expect([res.status, body.reason]).toEqual([403, "not_renewable"]);
  });

  it("foreign signature → 400", async () => {
    const { res, body } = await call({ key: await sign(patron, await foreignKey()) }, stubFetch().fetchFn);
    expect([res.status, body.reason]).toEqual([400, "bad_key"]);
  });

  it("evicted device → device_replaced", async () => {
    await env.KEYS.put("devices:u1", JSON.stringify([{ mid: "b".repeat(32), iat: 1 }]));
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch().fetchFn);
    expect([res.status, body.reason]).toEqual([403, "device_replaced"]);
  });

  it("unknown member → 403", async () => {
    await env.KEYS.delete("member:u1");
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch().fetchFn);
    expect([res.status, body.reason]).toEqual([403, "unknown_member"]);
  });

  it("declined → 403 declined_patron", async () => {
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(member("declined_patron")).fetchFn);
    expect([res.status, body.reason]).toEqual([403, "declined_patron"]);
  });

  it("null patron_status → 403 inactive", async () => {
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(member(null, 0)).fetchFn);
    expect([res.status, body.reason]).toEqual([403, "inactive"]);
  });

  it("active_patron with zero cents and not gifted → 403 free_member", async () => {
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(member("active_patron", 0)).fetchFn);
    expect([res.status, body.reason]).toEqual([403, "free_member"]);
  });

  it("member record for a different user → 403 unknown_member", async () => {
    await env.KEYS.put("member:u1", "m2");
    const other = jsonResponse(200, {
      data: {
        id: "m2", type: "member",
        attributes: { patron_status: "active_patron", currently_entitled_amount_cents: 500, is_gifted: false },
        relationships: { user: { data: { id: "someone-else" } } },
      },
    });
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(other).fetchFn);
    expect([res.status, body.reason]).toEqual([403, "unknown_member"]);
  });

  it("creator 401 → 503 creator_token", async () => {
    const waitUntil = vi.fn();
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(new Response("", { status: 401 })).fetchFn, { ctx: { waitUntil } as unknown as ExecutionContext });
    expect([res.status, body.reason]).toEqual([503, "creator_token"]);
    expect(waitUntil).toHaveBeenCalledTimes(1);
  });

  it("missing creator token → 503 creator_token", async () => {
    await env.KEYS.delete("creator");
    const waitUntil = vi.fn();
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch().fetchFn, { ctx: { waitUntil } as unknown as ExecutionContext });
    expect([res.status, body.reason]).toEqual([503, "creator_token"]);
    expect(waitUntil).toHaveBeenCalledTimes(1);
  });

  it("Patreon outage → 503 patreon_unavailable", async () => {
    const { res, body } = await call({ key: await sign(patron, await ourKey()) }, stubFetch(new Response("", { status: 500 })).fetchFn);
    expect([res.status, body.reason]).toEqual([503, "patreon_unavailable"]);
  });

  it("bad bodies → 400 bad_request", async () => {
    const f = stubFetch().fetchFn;
    expect((await call(null, f, { type: "text/plain", raw: "{}" })).res.status).toBe(400);
    expect((await call(null, f, { raw: "not json" })).res.status).toBe(400);
    expect((await call({ key: 5 }, f)).res.status).toBe(400);
    expect((await call({ key: "x".repeat(5000) }, f)).body.reason).toBe("bad_request");
  });

  it("client abort mid-body → 400", async () => {
    const stream = new ReadableStream<Uint8Array>({ pull() { throw new Error("client aborted"); } });
    const req = new Request("https://keys.test/renew", {
      method: "POST", headers: { "content-type": "application/json" }, body: stream, duplex: "half",
    } as RequestInit);
    const res = await renewHandler({ now: () => NOW, limiter: memoryLimiter(5, 60) })(req, env, ctx);
    expect(res.status).toBe(400);
  });

  it("oversized streamed body without Content-Length → 400 without reading it all", async () => {
    let pulls = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        pulls++;
        if (pulls > 20) throw new Error("pulled far past the cap");
        c.enqueue(new Uint8Array(1024).fill(97));
      },
    });
    const req = new Request("https://keys.test/renew", {
      method: "POST", headers: { "content-type": "application/json" }, body: stream, duplex: "half",
    } as RequestInit);
    const res = await renewHandler({ now: () => NOW, limiter: memoryLimiter(5, 60) })(req, env, ctx);
    expect(res.status).toBe(400);
    expect(pulls).toBeLessThan(20);
  });

  it("declared Content-Length over the cap → 400", async () => {
    const req = new Request("https://keys.test/renew", {
      method: "POST", headers: { "content-type": "application/json", "content-length": "99999" }, body: "{}",
    });
    const res = await renewHandler({ now: () => NOW, limiter: memoryLimiter(5, 60) })(req, env, ctx);
    expect(res.status).toBe(400);
  });
});
