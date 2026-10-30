import { beforeEach, describe, expect, it } from "vitest";
import golden from "./fixtures/golden.json";
import { callbackHandler } from "../src/routes/callback";
import { importPublicKey, verify } from "../src/sign";
import { signState } from "../src/state";
import { env, jsonResponse, stubFetch } from "./helpers";

const mid = "0123456789abcdef0123456789abcdef";
const NOW = 1_800_000_000;
const ctx = { waitUntil: () => {}, passThroughOnException: () => {} } as unknown as ExecutionContext;

const tokens = () => jsonResponse(200, { access_token: "ua", refresh_token: "ur" });
const identity = (status: string | null, cents = 500, gifted = false) =>
  jsonResponse(200, {
    data: { id: "u1", type: "user" },
    included: [{
      id: "m1", type: "member",
      attributes: { patron_status: status, currently_entitled_amount_cents: cents, is_gifted: gifted },
      relationships: { campaign: { data: { id: env.PATREON_CAMPAIGN_ID } } },
    }],
  });
const member = (status: string | null, cents = 500, gifted = false) =>
  jsonResponse(200, {
    data: {
      id: "m1", type: "member",
      attributes: { patron_status: status, currently_entitled_amount_cents: cents, is_gifted: gifted },
      relationships: { user: { data: { id: "u1" } } },
    },
  });
const noMember = () => jsonResponse(200, { data: { id: "u1", type: "user" }, included: [] });

async function call(fetchFn: typeof fetch, opts: { state?: string; code?: string | null } = {}) {
  const state = opts.state ?? (await signState(env, { mid, nonce: "n", exp: NOW + 600 }));
  const q = new URLSearchParams();
  if (opts.code !== null) q.set("code", opts.code ?? "abc");
  q.set("state", state);
  const res = await callbackHandler({ fetchFn, now: () => NOW })(new Request(`https://keys.test/callback?${q}`), env, ctx);
  return { res, html: await res.text() };
}

async function clearKv() {
  for (const { name } of (await env.KEYS.list()).keys) await env.KEYS.delete(name);
}
const seedCreator = () => env.KEYS.put("creator", JSON.stringify({ access: "ca", refresh: "cr", updated: 1 }));

beforeEach(async () => {
  await clearKv();
  await seedCreator();
});

describe("callback", () => {
  it("active patron gets a patron key bound to the state mid", async () => {
    const { fetchFn, calls } = stubFetch(tokens(), identity("active_patron"), member("active_patron"));
    const { res, html } = await call(fetchFn);
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(calls[2].url).toContain("/members/m1");
    expect((calls[2].init.headers as Record<string, string>).authorization).toBe("Bearer ca");
    const key = /<code[^>]*>(ro1\.[^<]+)<\/code>/.exec(html)![1];
    const c = await verify(key, await importPublicKey(golden.pub_hex));
    expect(c).toMatchObject({ v: 1, kind: "patron", sub: "u1", mid, iat: NOW });
    expect(c!.exp! - c!.iat).toBe(35 * 86400);
    expect(html).toContain(`rootout license ${key}`);
    expect(html).toContain("This Mac");
    expect(JSON.parse((await env.KEYS.get("devices:u1"))!)).toEqual([{ mid, iat: NOW }]);
    expect(await env.KEYS.get("member:u1")).toBe("m1");
  });

  it("creator member record is authoritative over the identity data", async () => {
    const { fetchFn } = stubFetch(tokens(), identity("active_patron"), member("former_patron", 0));
    const { html } = await call(fetchFn);
    expect(html).not.toContain("ro1.");
    expect(html).toContain("ended");
  });

  it("third device evicts the oldest", async () => {
    await env.KEYS.put("devices:u1", JSON.stringify([{ mid: "a".repeat(32), iat: 100 }, { mid: "b".repeat(32), iat: 200 }]));
    const { fetchFn } = stubFetch(tokens(), identity("active_patron"), member("active_patron"));
    const { html } = await call(fetchFn);
    expect(JSON.parse((await env.KEYS.get("devices:u1"))!)).toEqual([{ mid: "b".repeat(32), iat: 200 }, { mid, iat: NOW }]);
    expect(html).toContain("1 other");
  });

  it("null patron_status is not active", async () => {
    const { fetchFn } = stubFetch(tokens(), identity(null, 0), member(null, 0));
    const { res, html } = await call(fetchFn);
    expect(res.status).toBe(200);
    expect(html).toContain("free");
    expect(html).not.toContain("ro1.");
    expect(await env.KEYS.get("devices:u1")).toBeNull();
  });

  it("declined patron denied", async () => {
    const { fetchFn } = stubFetch(tokens(), identity("declined_patron"), member("declined_patron"));
    const { html } = await call(fetchFn);
    expect(html).toContain("declined");
    expect(html).toContain(env.PATREON_URL);
    expect(html).not.toContain("ro1.");
  });

  it("not a member of the campaign is denied without a member lookup", async () => {
    const { fetchFn, calls } = stubFetch(tokens(), noMember());
    const { html } = await call(fetchFn);
    expect(calls).toHaveLength(2);
    expect(html).not.toContain("ro1.");
    expect(html).toContain(env.PATREON_URL);
  });

  it("tampered state is rejected", async () => {
    const good = await signState(env, { mid, nonce: "n", exp: NOW + 600 });
    const { fetchFn, calls } = stubFetch();
    const { res } = await call(fetchFn, { state: good.slice(0, -2) + "xx" });
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  it("missing code → 400 with no Patreon call", async () => {
    const { fetchFn, calls } = stubFetch();
    const { res } = await call(fetchFn, { code: null });
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  it("creator token 401 falls back to identity data and still issues when active", async () => {
    const { fetchFn } = stubFetch(tokens(), identity("active_patron"), new Response("", { status: 401 }));
    const { res, html } = await call(fetchFn);
    expect(res.status).toBe(200);
    expect(html).toContain("ro1.");
  });

  it("missing creator pair falls back to identity data", async () => {
    await clearKv();
    const { fetchFn, calls } = stubFetch(tokens(), identity("active_patron"));
    const { html } = await call(fetchFn);
    expect(calls).toHaveLength(2);
    expect(html).toContain("ro1.");
  });

  it("Patreon outage → 502", async () => {
    const { fetchFn } = stubFetch(tokens(), new Response("", { status: 500 }));
    const { res } = await call(fetchFn);
    expect(res.status).toBe(502);
  });
});
