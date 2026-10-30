import { afterEach, describe, expect, it, vi } from "vitest";
import { alert } from "../src/alert";
import { env, jsonResponse, stubFetch } from "./helpers";

const ISSUES = "https://api.github.com/repos/shiva2021/rootout/issues";

afterEach(() => vi.restoreAllMocks());

describe("alert", () => {
  it("comments on an existing open issue", async () => {
    const { fetchFn, calls } = stubFetch(
      jsonResponse(200, [{ number: 3, title: "other" }, { number: 7, title: "creator token missing" }]),
      jsonResponse(201, {}),
    );
    await alert(env, "creator token missing", "body text", fetchFn);
    expect(calls).toHaveLength(2);
    expect(calls[0].url).toBe(`${ISSUES}?labels=rootout-keys&state=open&per_page=50`);
    expect(calls[0].init.method ?? "GET").toBe("GET");
    expect(calls[0].init.headers).toMatchObject({
      Authorization: "Bearer ghp_test",
      Accept: "application/vnd.github+json",
      "X-GitHub-Api-Version": "2022-11-28",
      "User-Agent": "rootout-keys",
    });
    expect(calls[1].url).toBe(`${ISSUES}/7/comments`);
    expect(calls[1].init.method).toBe("POST");
    expect(JSON.parse(calls[1].init.body as string)).toEqual({ body: "body text" });
  });

  it("creates when none", async () => {
    const { fetchFn, calls } = stubFetch(jsonResponse(200, [{ number: 3, title: "other" }]), jsonResponse(201, {}));
    await alert(env, "creator token refresh failed", "boom", fetchFn);
    expect(calls[1].url).toBe(ISSUES);
    expect(calls[1].init.method).toBe("POST");
    expect(JSON.parse(calls[1].init.body as string)).toEqual({
      title: "creator token refresh failed", body: "boom", labels: ["rootout-keys"],
    });
  });

  it("GitHub 500 does not throw", async () => {
    const err = vi.spyOn(console, "error").mockImplementation(() => {});
    const { fetchFn, calls } = stubFetch(new Response("", { status: 500 }));
    await expect(alert(env, "t", "b", fetchFn)).resolves.toBeUndefined();
    expect(calls).toHaveLength(1);
    expect(err).toHaveBeenCalledTimes(1);
  });

  it("network failure and bad JSON do not throw", async () => {
    const err = vi.spyOn(console, "error").mockImplementation(() => {});
    const boom = (async () => { throw new Error("net down"); }) as typeof fetch;
    await expect(alert(env, "t", "b", boom)).resolves.toBeUndefined();
    await expect(alert(env, "t", "b", stubFetch(new Response("not json", { status: 200 })).fetchFn)).resolves.toBeUndefined();
    expect(err).toHaveBeenCalledTimes(2);
  });

  it("unconfigured warns and makes no request", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { fetchFn, calls } = stubFetch();
    await alert({ ...env, ALERT_GITHUB_TOKEN: "" }, "creator token missing", "b", fetchFn);
    expect(calls).toHaveLength(0);
    expect(warn).toHaveBeenCalledWith("alert (unconfigured): creator token missing");
  });
});
