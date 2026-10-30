import type { Env } from "./env";
import { importPrivateKey } from "./sign";

// One imported key per isolate, re-imported only if SIGNING_KEY changes.
let signing: { source: string; key: Promise<CryptoKey> } | null = null;
let verifying: { source: string; key: Promise<CryptoKey> } | null = null;

export function signingKey(env: Env): Promise<CryptoKey> {
  if (signing?.source !== env.SIGNING_KEY) {
    const key = importPrivateKey(env.SIGNING_KEY);
    signing = { source: env.SIGNING_KEY, key };
    key.catch(() => { if (signing?.key === key) signing = null; });
  }
  return signing!.key;
}

// The public half is derived from the private key: import as extractable, export the JWK, drop `d`.
async function derivePublic(pkcs8b64: string): Promise<CryptoKey> {
  const der = Uint8Array.from(atob(pkcs8b64), (ch) => ch.charCodeAt(0));
  const priv = await crypto.subtle.importKey("pkcs8", der, { name: "Ed25519" }, true, ["sign"]);
  const jwk = (await crypto.subtle.exportKey("jwk", priv)) as JsonWebKey;
  delete jwk.d;
  jwk.key_ops = ["verify"];
  return crypto.subtle.importKey("jwk", jwk, { name: "Ed25519" }, false, ["verify"]);
}

export function verifyingKey(env: Env): Promise<CryptoKey> {
  if (verifying?.source !== env.SIGNING_KEY) {
    const key = derivePublic(env.SIGNING_KEY);
    verifying = { source: env.SIGNING_KEY, key };
    key.catch(() => { if (verifying?.key === key) verifying = null; });
  }
  return verifying!.key;
}
