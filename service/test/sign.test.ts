// Fixture test/fixtures/golden.json is a copy; the source of truth is internal/license/testdata/golden.json (Go).
import { describe, expect, it } from "vitest";
import golden from "./fixtures/golden.json";
import { type Claims, encodeClaims, importPrivateKey, importPublicKey, sign, verify } from "../src/sign";

const PKCS8_PREFIX = "302e020100300506032b657004220420";

function hexToBytes(h: string): Uint8Array {
  return Uint8Array.from(h.match(/../g)!, (x) => parseInt(x, 16));
}
function toB64(b: Uint8Array): string {
  return btoa(String.fromCharCode(...b));
}
async function privFromSeed(seedHex: string): Promise<CryptoKey> {
  return importPrivateKey(toB64(hexToBytes(PKCS8_PREFIX + seedHex)));
}
const goldenClaims = golden.claims as Claims;
const text = (b: Uint8Array) => new TextDecoder().decode(b);

describe("sign", () => {
  it("test seed is sha256 of rootout-test-seed", async () => {
    const d = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode("rootout-test-seed")));
    expect([...d].map((x) => x.toString(16).padStart(2, "0")).join("")).toBe(golden.seed_hex);
  });

  it("encodes claims in Go key order", () => {
    expect(text(encodeClaims(goldenClaims))).toBe(
      '{"v":1,"kind":"patron","sub":"12345678","mid":"0123456789abcdef0123456789abcdef","iat":1730246400,"exp":1733270400}',
    );
  });

  it("signs the golden claims to the golden key", async () => {
    expect(await sign(goldenClaims, await privFromSeed(golden.seed_hex))).toBe(golden.key);
  });

  it("verifies the golden key with the golden public key", async () => {
    const pub = await importPublicKey(golden.pub_hex);
    expect(await verify(golden.key, pub)).toEqual(goldenClaims);
  });

  it("rejects bad keys", async () => {
    const pub = await importPublicKey(golden.pub_hex);
    const priv = await privFromSeed(golden.seed_hex);
    const [, payload, sig] = golden.key.split(".");

    expect(await verify("ro2." + payload + "." + sig, pub)).toBeNull(); // wrong prefix

    const tampered = btoa(text(encodeClaims({ ...goldenClaims, sub: "99999999" })))
      .replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    expect(await verify(`ro1.${tampered}.${sig}`, pub)).toBeNull(); // tampered payload

    const foreign = await privFromSeed("00".repeat(31) + "01");
    expect(await verify(await sign(goldenClaims, foreign), pub)).toBeNull(); // foreign key

    const noExp: Claims = { v: 1, kind: "patron", sub: "1", mid: goldenClaims.mid, iat: 1730246400 };
    expect(await verify(await sign(noExp, priv), pub)).toBeNull(); // patron without exp

    expect(await verify(await sign({ ...goldenClaims, mid: "XYZ" }, priv), pub)).toBeNull(); // bad mid
    expect(await verify(await sign({ ...goldenClaims, exp: goldenClaims.iat }, priv), pub)).toBeNull(); // exp <= iat
    expect(await verify(golden.key + "x", pub)).toBeNull();
    expect(await verify("", pub)).toBeNull();
  });

  it("admin claims omit exp", async () => {
    const admin: Claims = { v: 1, kind: "admin", sub: "me", mid: goldenClaims.mid, iat: 1730246400 };
    expect(text(encodeClaims(admin))).not.toContain("exp");
    const pub = await importPublicKey(golden.pub_hex);
    const key = await sign(admin, await privFromSeed(golden.seed_hex));
    expect(await verify(key, pub)).toEqual(admin);
  });
});

describe("sign key import errors", () => {
  it("a bad SIGNING_KEY fails with a clear message", async () => {
    await expect(importPrivateKey("!!!not base64!!!")).rejects.toThrow("SIGNING_KEY is not valid base64 PKCS#8");
  });
});
