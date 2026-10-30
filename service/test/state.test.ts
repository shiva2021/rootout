import { describe, expect, it } from "vitest";
import { signState, verifyState } from "../src/state";
import { env } from "./helpers";

const mid = "0123456789abcdef0123456789abcdef";

describe("state", () => {
  it("round trip", async () => {
    const s = await signState(env, { mid, nonce: "n", exp: 1600 });
    expect(await verifyState(env, s, 1000)).toEqual({ mid });
  });

  it("expired → null", async () => {
    const s = await signState(env, { mid, nonce: "n", exp: 1600 });
    expect(await verifyState(env, s, 1600)).toBeNull();
    expect(await verifyState(env, s, 2000)).toBeNull();
  });

  it("tampered sig → null", async () => {
    const s = await signState(env, { mid, nonce: "n", exp: 1600 });
    const [p, sig] = s.split(".");
    const flipped = (sig[0] === "A" ? "B" : "A") + sig.slice(1);
    expect(await verifyState(env, `${p}.${flipped}`, 1000)).toBeNull();
  });

  it("tampered payload → null", async () => {
    const s = await signState(env, { mid, nonce: "n", exp: 1600 });
    const sig = s.split(".")[1];
    const forged = btoa(JSON.stringify({ mid, nonce: "n", exp: 999999 })).replace(/=+$/, "");
    expect(await verifyState(env, `${forged}.${sig}`, 1000)).toBeNull();
  });

  it("wrong secret → null", async () => {
    const s = await signState(env, { mid, nonce: "n", exp: 1600 });
    expect(await verifyState({ ...env, STATE_SECRET: "other" }, s, 1000)).toBeNull();
  });

  it("bad mid or garbage → null", async () => {
    const s = await signState(env, { mid: "nope", nonce: "n", exp: 1600 });
    expect(await verifyState(env, s, 1000)).toBeNull();
    expect(await verifyState(env, "", 1000)).toBeNull();
    expect(await verifyState(env, "a.b.c", 1000)).toBeNull();
    expect(await verifyState(env, "!!!.???", 1000)).toBeNull();
  });
});
