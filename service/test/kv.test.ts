import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  addDevice, getCreator, getDevices, getMember, hasDevice, markTrial, putCreator, putMember, trialSeen,
} from "../src/kv";
import { env } from "./helpers";

const kv = env.KEYS;
const pair = (n: string) => ({ access: "a" + n, refresh: "r" + n, updated: Number(n) });

beforeEach(async () => {
  for (const k of ["creator", "creator_prev", "devices:u1", "member:u1", "trial:" + "a".repeat(32)]) await kv.delete(k);
});

describe("kv", () => {
  it("addDevice caps at 2 and evicts oldest", async () => {
    expect((await addDevice(kv, "u1", "m1", 10)).evicted).toBeNull();
    expect((await addDevice(kv, "u1", "m2", 20)).evicted).toBeNull();
    const r = await addDevice(kv, "u1", "m3", 30);
    expect(r.evicted).toEqual({ mid: "m1", iat: 10 });
    expect(r.devices).toEqual([{ mid: "m2", iat: 20 }, { mid: "m3", iat: 30 }]);
    expect(await getDevices(kv, "u1")).toEqual(r.devices);
    expect(await hasDevice(kv, "u1", "m1")).toBe(false);
    expect(await hasDevice(kv, "u1", "m3")).toBe(true);
  });

  it("addDevice with an existing mid writes nothing and evicts nothing", async () => {
    await addDevice(kv, "u1", "m1", 10);
    await addDevice(kv, "u1", "m2", 20);
    const put = vi.spyOn(kv, "put");
    const r = await addDevice(kv, "u1", "m1", 99);
    expect(put).not.toHaveBeenCalled();
    put.mockRestore();
    expect(r.evicted).toBeNull();
    expect(r.devices).toEqual([{ mid: "m1", iat: 10 }, { mid: "m2", iat: 20 }]);
  });

  it("getDevices is empty for an unknown user", async () => {
    expect(await getDevices(kv, "nobody")).toEqual([]);
  });

  it("putCreator keeps the previous pair", async () => {
    await putCreator(kv, pair("1"));
    await putCreator(kv, pair("2"));
    expect(await kv.get("creator", "json")).toEqual(pair("2"));
    expect(await kv.get("creator_prev", "json")).toEqual(pair("1"));
  });

  it("getCreator falls back to creator_prev", async () => {
    expect(await getCreator(kv)).toBeNull();
    await kv.put("creator_prev", JSON.stringify(pair("1")));
    expect(await getCreator(kv)).toEqual(pair("1"));
    await kv.put("creator", JSON.stringify(pair("2")));
    expect(await getCreator(kv)).toEqual(pair("2"));
  });

  it("trial marks and member ids round trip", async () => {
    const mid = "a".repeat(32);
    expect(await trialSeen(kv, mid)).toBe(false);
    await markTrial(kv, mid, 123);
    expect(await trialSeen(kv, mid)).toBe(true);
    expect(await kv.get("trial:" + mid, "json")).toEqual({ iat: 123 });
    expect(await getMember(kv, "u1")).toBeNull();
    await putMember(kv, "u1", "m-77");
    expect(await getMember(kv, "u1")).toBe("m-77");
    expect(await kv.get("member:u1")).toBe("m-77");
  });
});
