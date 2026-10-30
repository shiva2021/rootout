import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { env } from "./helpers";

const ctx = { waitUntil() {}, passThroughOnException() {} } as unknown as ExecutionContext;
const call = (method: string, path: string) => worker.fetch(new Request("https://keys.test" + path, { method }), env, ctx);

describe("router", () => {
  it("unknown path returns 404", async () => {
    expect((await call("GET", "/nope")).status).toBe(404);
    expect((await call("DELETE", "/renew")).status).toBe(404);
    expect((await call("GET", "/renew")).status).toBe(404);
  });

  it("renew and trial are wired and reject a non-JSON body", async () => {
    for (const [m, p] of [["POST", "/renew"], ["POST", "/trial"]]) {
      expect((await call(m, p)).status, `${m} ${p}`).toBe(400);
    }
  });

  it("scheduled hands the refresh to waitUntil", async () => {
    const pending: Promise<unknown>[] = [];
    const c = { waitUntil: (p: Promise<unknown>) => { pending.push(p); } } as unknown as ExecutionContext;
    const kv = env.KEYS;
    const fresh = { access: "a", refresh: "r", updated: Math.floor(Date.now() / 1000) };
    await kv.put("creator", JSON.stringify(fresh));
    worker.scheduled({} as ScheduledController, env, c);
    expect(pending).toHaveLength(1);
    expect(await pending[0]).toBe("skipped");
    await kv.delete("creator");
  });
});
