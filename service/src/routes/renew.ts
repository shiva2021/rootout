import { alert } from "../alert";
import { getCreator, getMember, hasDevice } from "../kv";
import { signingKey, verifyingKey } from "../keys";
import { PatreonAuthError, PatreonError, isActive, memberById } from "../patreon";
import { type RateLimiter, defaultLimiter } from "../ratelimit";
import { type Handler, json } from "../router";
import { PATRON_LIFE, sign, verify } from "../sign";
import { limited, readJson } from "./body";

/** Renews a patron key. Never writes KV: the device iat is left alone so eviction order stays issue-time only. */
export function renewHandler(deps: { fetchFn?: typeof fetch; now?: () => number; limiter?: RateLimiter } = {}): Handler {
  const fetchFn = deps.fetchFn ?? ((input, init) => fetch(input, init));
  const now = deps.now ?? (() => Math.floor(Date.now() / 1000));
  return async (req, env, ctx) => {
    const over = await limited(req, deps.limiter ?? defaultLimiter(env));
    if (over) return over;
    const body = await readJson(req);
    if (body instanceof Response) return body;
    if (typeof body.key !== "string") return json(400, { reason: "bad_request" });

    const claims = await verify(body.key, await verifyingKey(env));
    if (!claims) return json(400, { reason: "bad_key" });
    if (claims.kind !== "patron") return json(403, { reason: "not_renewable" });
    if (!(await hasDevice(env.KEYS, claims.sub, claims.mid))) return json(403, { reason: "device_replaced" });
    const memberId = await getMember(env.KEYS, claims.sub);
    if (memberId === null) return json(403, { reason: "unknown_member" });

    const creator = await getCreator(env.KEYS);
    if (creator === null) {
      ctx.waitUntil(alert(env, "creator token missing", "No creator token in KV; /renew could not check membership."));
      return json(503, { reason: "creator_token" });
    }
    try {
      const m = await memberById(creator.access, memberId, fetchFn);
      // A corrupted member mapping must never renew against someone else's membership.
      if (m.userId !== claims.sub) return json(403, { reason: "unknown_member" });
      if (!isActive(m)) return json(403, { reason: m.patronStatus === "active_patron" ? "free_member" : (m.patronStatus ?? "inactive") });
    } catch (e) {
      if (e instanceof PatreonAuthError) {
        ctx.waitUntil(alert(env, "creator token rejected", "Patreon returned 401 for the creator token; /renew is refusing."));
        return json(503, { reason: "creator_token" });
      }
      if (e instanceof PatreonError) return json(503, { reason: "patreon_unavailable" });
      throw e;
    }

    const t = now();
    const key = await sign(
      { v: 1, kind: "patron", sub: claims.sub, mid: claims.mid, iat: t, exp: t + PATRON_LIFE },
      await signingKey(env),
    );
    return json(200, { key });
  };
}
