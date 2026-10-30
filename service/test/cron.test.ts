import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { refreshCreator } from "../src/cron";
import { getCreator } from "../src/kv";
import { env, jsonResponse, stubFetch } from "./helpers";

const NOW = 1_800_000_000;
const DAY = 86400;
const kv = env.KEYS;
const old = { access: "oldA", refresh: "oldR", updated: NOW - 21 * DAY };
const tokenOk = () => jsonResponse(200, { access_token: "newA", refresh_token: "newR", expires_in: 2678400 });
const issuesFor = (title: string) => [jsonResponse(200, []), jsonResponse(201, { title })];

beforeEach(async () => {
  await kv.delete("creator");
  await kv.delete("creator_prev");
});
afterEach(() => vi.restoreAllMocks());

// refreshCreator takes one fetch for Patreon and alert uses the global fetch; route both through a single stub.
function withGlobalFetch(...responses: Response[]) {
  const s = stubFetch(...responses);
  vi.stubGlobal("fetch", s.fetchFn);
  return s;
}

describe("cron", () => {
  it("refreshes after 20 days and stores the new pair with prev kept", async () => {
    await kv.put("creator", JSON.stringify(old));
    const { fetchFn, calls } = stubFetch(tokenOk());
    expect(await refreshCreator(env, NOW, fetchFn)).toBe("refreshed");
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("https://www.patreon.com/api/oauth2/token");
    expect(calls[0].init.method).toBe("POST");
    expect(String(calls[0].init.body)).toContain("refresh_token=oldR");
    expect(await getCreator(kv)).toEqual({ access: "newA", refresh: "newR", updated: NOW });
    expect(JSON.parse((await kv.get("creator_prev"))!)).toEqual(old);
  });

  it("skips a fresh pair", async () => {
    const fresh = { ...old, updated: NOW - 19 * DAY };
    await kv.put("creator", JSON.stringify(fresh));
    const { fetchFn, calls } = stubFetch();
    expect(await refreshCreator(env, NOW, fetchFn)).toBe("skipped");
    expect(calls).toHaveLength(0);
    expect(await getCreator(kv)).toEqual(fresh);
  });

  it("Patreon error alerts and keeps the pair", async () => {
    await kv.put("creator", JSON.stringify(old));
    const g = withGlobalFetch(...issuesFor("x"));
    const { fetchFn, calls } = stubFetch(new Response("{}", { status: 400 }));
    expect(await refreshCreator(env, NOW, fetchFn)).toBe("failed");
    expect(calls).toHaveLength(1);
    expect(g.calls).toHaveLength(2);
    expect(g.calls[1].url).toBe("https://api.github.com/repos/shiva2021/rootout/issues");
    const posted = JSON.parse(g.calls[1].init.body as string);
    expect(posted.title).toBe("creator token refresh failed");
    expect(posted.body).toContain(new Date(NOW * 1000).toISOString());
    expect(posted.body).toContain("keys.test");
    expect(await getCreator(kv)).toEqual(old);
  });

  it("KV put failure keeps the previous pair", async () => {
    await kv.put("creator", JSON.stringify(old));
    const g = withGlobalFetch(...issuesFor("x"));
    const realPut = kv.put.bind(kv);
    vi.spyOn(kv, "put").mockImplementation(((key: string, value: string) => {
      if (key === "creator") throw new Error("kv down");
      return realPut(key, value);
    }) as typeof kv.put);
    const { fetchFn } = stubFetch(tokenOk());
    expect(await refreshCreator(env, NOW, fetchFn)).toBe("failed");
    expect(JSON.parse(g.calls[1].init.body as string).body).toContain("kv down");
    vi.restoreAllMocks();
    expect(await getCreator(kv)).toEqual(old);
    expect(JSON.parse((await kv.get("creator_prev"))!)).toEqual(old);
  });

  it("missing pair alerts and returns failed", async () => {
    const g = withGlobalFetch(...issuesFor("x"));
    const { fetchFn, calls } = stubFetch();
    expect(await refreshCreator(env, NOW, fetchFn)).toBe("failed");
    expect(calls).toHaveLength(0);
    expect(JSON.parse(g.calls[1].init.body as string).title).toBe("creator token missing");
  });
});
