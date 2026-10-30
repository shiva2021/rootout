import { afterEach, describe, expect, it, vi } from "vitest";
import type { Env } from "../src/env";

describe("defaultLimiter", () => {
  afterEach(() => vi.restoreAllMocks());

  it("warns once per isolate when the RATE binding is missing", async () => {
    vi.resetModules();
    const { defaultLimiter } = await import("../src/ratelimit");
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const env = {} as Env;
    expect((await defaultLimiter(env).limit("1.2.3.4")).success).toBe(true);
    defaultLimiter(env);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls[0][0]).toContain("in-memory rate limiter");
  });

  it("uses the binding without warning when RATE is set", async () => {
    vi.resetModules();
    const { defaultLimiter } = await import("../src/ratelimit");
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const binding = { limit: vi.fn(async () => ({ success: false })) };
    const env = { RATE: binding } as unknown as Env;
    expect((await defaultLimiter(env).limit("k")).success).toBe(false);
    expect(binding.limit).toHaveBeenCalledWith({ key: "k" });
    expect(warn).not.toHaveBeenCalled();
  });
});
