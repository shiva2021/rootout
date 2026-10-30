import { markTrial, trialSeen } from "../kv";
import { signingKey } from "../keys";
import type { RateLimiter } from "../ratelimit";
import { type Handler, json } from "../router";
import { TRIAL_LIFE, isMid, sign } from "../sign";
import { limited, readJson } from "./body";
import { defaultLimiter } from "./renew";

/** One 7-day trial per machine id. Costs one KV write per new mid, hence the rate limit. */
export function trialHandler(deps: { now?: () => number; limiter?: RateLimiter } = {}): Handler {
  const now = deps.now ?? (() => Math.floor(Date.now() / 1000));
  return async (req, env) => {
    const over = await limited(req, deps.limiter ?? defaultLimiter(env));
    if (over) return over;
    const body = await readJson(req);
    if (body instanceof Response) return body;
    const mid = body.mid;
    if (typeof mid !== "string" || !isMid(mid)) return json(400, { reason: "bad_request" });
    if (await trialSeen(env.KEYS, mid)) return json(409, { reason: "trial_used" });

    const t = now();
    const priv = await signingKey(env); // before the KV write so a key failure never burns the trial
    const key = await sign({ v: 1, kind: "trial", sub: "trial", mid, iat: t, exp: t + TRIAL_LIFE }, priv);
    await markTrial(env.KEYS, mid, t);
    return json(200, { key });
  };
}
