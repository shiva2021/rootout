// Signed OAuth state: base64url(JSON payload) + "." + base64url(HMAC-SHA-256), keyed by STATE_SECRET.
import type { Env } from "./env";
import { isMid } from "./sign";

const enc = new TextEncoder();
const dec = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false });

function b64urlEncode(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function b64urlDecode(s: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]+$/.test(s) || s.length % 4 === 1) return null;
  try {
    const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (s.length % 4)) % 4));
    return Uint8Array.from(bin, (ch) => ch.charCodeAt(0));
  } catch {
    return null;
  }
}

const hmacKey = (env: Env) =>
  crypto.subtle.importKey("raw", enc.encode(env.STATE_SECRET), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);

async function mac(env: Env, data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.sign("HMAC", await hmacKey(env), data));
}

function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  let diff = a.length ^ b.length;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ (b[i % b.length] ?? 0);
  return diff === 0;
}

export async function signState(env: Env, payload: { mid: string; nonce: string; exp: number }): Promise<string> {
  const body = enc.encode(JSON.stringify({ mid: payload.mid, nonce: payload.nonce, exp: payload.exp }));
  return `${b64urlEncode(body)}.${b64urlEncode(await mac(env, body))}`;
}

/** Returns the machine id, or null when the mac is wrong, the state has expired or the mid is malformed. */
export async function verifyState(env: Env, s: string, now: number): Promise<{ mid: string } | null> {
  const parts = s.split(".");
  if (parts.length !== 2) return null;
  const body = b64urlDecode(parts[0]);
  const sig = b64urlDecode(parts[1]);
  if (!body || !sig) return null;
  if (!sameBytes(sig, await mac(env, body))) return null;
  let o: unknown;
  try {
    o = JSON.parse(dec.decode(body));
  } catch {
    return null;
  }
  if (typeof o !== "object" || o === null) return null;
  const r = o as Record<string, unknown>;
  if (typeof r.exp !== "number" || r.exp <= now) return null;
  if (typeof r.mid !== "string" || !isMid(r.mid)) return null;
  return { mid: r.mid };
}
