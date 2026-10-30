import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { verifyState } from "../src/state";
import { env } from "./helpers";

const ctx = { waitUntil() {}, passThroughOnException() {} } as unknown as ExecutionContext;
const SELF = { fetch: (url: string, _init?: RequestInit) => worker.fetch(new Request(url), env, ctx) };

const mid = "0123456789abcdef0123456789abcdef";

describe("home", () => {
  it("prefills a valid mid in a form posting to /start", async () => {
    const res = await SELF.fetch(`https://keys.test/?m=${mid}`);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/html");
    expect(res.headers.get("cache-control")).toBe("no-store");
    const html = await res.text();
    expect(html).toContain('action="/start"');
    expect(html).toContain('method="GET"');
    expect(html).toContain(`name="m"`);
    expect(html).toContain(`value="${mid}"`);
    expect(html).toContain("Log in with Patreon");
  });

  it("without m explains rootout license", async () => {
    const html = await (await SELF.fetch("https://keys.test/")).text();
    expect(html).toContain("rootout license");
    expect(html).toContain("Log in with Patreon");
  });

  it("an invalid m is not echoed", async () => {
    const html = await (await SELF.fetch(`https://keys.test/?m=${encodeURIComponent('"><script>x</script>')}`)).text();
    expect(html).not.toContain("<script>x");
    expect(html).toContain("rootout license");
  });
});

describe("start", () => {
  it("valid mid redirects 302 to patreon.com/oauth2/authorize with scope=identity and a verifiable state", async () => {
    const res = await SELF.fetch(`https://keys.test/start?m=${mid}`, { redirect: "manual" });
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.origin + loc.pathname).toBe("https://www.patreon.com/oauth2/authorize");
    expect(loc.searchParams.get("scope")).toBe("identity");
    const state = loc.searchParams.get("state")!;
    expect(await verifyState(env, state, Math.floor(Date.now() / 1000))).toEqual({ mid });
    expect(await verifyState(env, state, Math.floor(Date.now() / 1000) + 601)).toBeNull();
  });

  it("bad mid → 400", async () => {
    for (const q of ["", "?m=nope", `?m=${mid.toUpperCase()}`]) {
      const res = await SELF.fetch(`https://keys.test/start${q}`, { redirect: "manual" });
      expect(res.status).toBe(400);
      expect(res.headers.get("content-type")).toContain("text/html");
    }
  });
});
