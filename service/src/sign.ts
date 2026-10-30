// Ed25519 licence keys, byte-compatible with internal/license (Go).
export type Kind = "patron" | "trial" | "admin";
export interface Claims { v: 1; kind: Kind; sub: string; mid: string; iat: number; exp?: number }

export const PATRON_LIFE = 35 * 86400;
export const TRIAL_LIFE = 7 * 86400;

const PREFIX = "ro1";
const MID_RE = /^[0-9a-f]{32}$/;
const enc = new TextEncoder();
const dec = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false });

export function isMid(s: string): boolean {
  return MID_RE.test(s);
}

/** Canonical JSON bytes: key order v, kind, sub, mid, iat, exp (exp omitted when undefined). */
export function encodeClaims(c: Claims): Uint8Array {
  const o: Record<string, unknown> = { v: c.v, kind: c.kind, sub: c.sub, mid: c.mid, iat: c.iat };
  if (c.exp !== undefined) o.exp = c.exp;
  return enc.encode(JSON.stringify(o));
}

function b64urlEncode(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Strict unpadded base64url, like Go's RawURLEncoding; returns null on bad input.
function b64urlDecode(s: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]*$/.test(s) || s.length % 4 === 1) return null;
  const std = s.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (s.length % 4)) % 4);
  try {
    const bin = atob(std);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  } catch {
    return null;
  }
}

export async function importPrivateKey(pkcs8b64: string): Promise<CryptoKey> {
  const bin = atob(pkcs8b64);
  const der = Uint8Array.from(bin, (ch) => ch.charCodeAt(0));
  return crypto.subtle.importKey("pkcs8", der, { name: "Ed25519" }, false, ["sign"]);
}

export async function importPublicKey(rawHex: string): Promise<CryptoKey> {
  if (!/^[0-9a-f]{64}$/.test(rawHex)) throw new Error("public key must be 64 hex characters");
  const raw = Uint8Array.from(rawHex.match(/../g)!, (h) => parseInt(h, 16));
  return crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, true, ["verify"]);
}

export async function sign(c: Claims, priv: CryptoKey): Promise<string> {
  const payload = encodeClaims(c);
  const sig = new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, priv, payload));
  return `${PREFIX}.${b64urlEncode(payload)}.${b64urlEncode(sig)}`;
}

/** Returns the claims, or null on any failure. Mirrors Go's Decode and Claims.Valid. */
export async function verify(key: string, pub: CryptoKey): Promise<Claims | null> {
  const parts = key.trim().split(".");
  if (parts.length !== 3 || parts[0] !== PREFIX) return null;
  const payload = b64urlDecode(parts[1]);
  const sig = b64urlDecode(parts[2]);
  if (!payload || !sig) return null;
  let ok = false;
  try {
    ok = await crypto.subtle.verify({ name: "Ed25519" }, pub, sig, payload);
  } catch {
    return null;
  }
  if (!ok) return null; // signature is checked before the payload is parsed
  let o: unknown;
  try {
    o = JSON.parse(dec.decode(payload));
  } catch {
    return null;
  }
  if (typeof o !== "object" || o === null) return null;
  const r = o as Record<string, unknown>;
  if (r.v !== 1) return null;
  if (r.kind !== "patron" && r.kind !== "trial" && r.kind !== "admin") return null;
  if (typeof r.sub !== "string") return null;
  if (typeof r.mid !== "string" || !isMid(r.mid)) return null;
  if (typeof r.iat !== "number" || !Number.isInteger(r.iat)) return null;
  const exp = r.exp;
  if (exp !== undefined && (typeof exp !== "number" || !Number.isInteger(exp))) return null;
  if (r.kind !== "admin" && (exp === undefined || exp === 0 || exp <= r.iat)) return null;
  const c: Claims = { v: 1, kind: r.kind, sub: r.sub, mid: r.mid, iat: r.iat };
  if (exp !== undefined && exp !== 0) c.exp = exp;
  return c;
}
