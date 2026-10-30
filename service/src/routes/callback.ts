import { alert } from "../alert";
import type { Env } from "../env";
import { addDevice, getCreator, putMember } from "../kv";
import { deniedPage, errorPage, html, keyPage } from "../page";
import {
  PatreonAuthError, PatreonError, exchangeCode, identityMembership, isActive, memberById, type Membership,
} from "../patreon";
import type { Handler } from "../router";
import { signingKey } from "../keys";
import { PATRON_LIFE, sign } from "../sign";
import { verifyState } from "../state";

const REASONS: Record<string, string> = {
  declined_patron: "your last payment was declined",
  former_patron: "your membership has ended",
  free_member: "you follow for free; a paid tier is needed",
  not_a_member: "this Patreon account is not a member yet; a paid tier is needed",
};
const reasonText = (key: string): string => REASONS[key] ?? REASONS.free_member;

export function callbackHandler(deps: { fetchFn?: typeof fetch; now?: () => number } = {}): Handler {
  const fetchFn = deps.fetchFn ?? ((input, init) => fetch(input, init));
  const now = deps.now ?? (() => Math.floor(Date.now() / 1000));
  return async (req, env, ctx) => {
    const q = new URL(req.url).searchParams;
    const code = q.get("code");
    const state = q.get("state");
    const t = now();
    const verified = code && state ? await verifyState(env, state, t) : null;
    if (!code || !verified) {
      return html(400, errorPage("This link has expired or is invalid; start again from the key page."));
    }
    const mid = verified.mid;

    try {
      const user = await exchangeCode(env, code, fetchFn, now);
      let m: Membership = await identityMembership(user.access, env.PATREON_CAMPAIGN_ID, fetchFn);
      if (m.memberId === null) return html(200, deniedPage(reasonText("not_a_member"), env.PATREON_URL));

      // The creator's member record is authoritative; the user's own identity data is the fallback only
      // when the creator token is missing or rejected.
      const creator = await getCreator(env.KEYS);
      if (creator === null) {
        ctx.waitUntil(alert(env, "creator token missing", "No creator token in KV; /callback used identity data."));
      } else {
        try {
          m = await memberById(creator.access, m.memberId, fetchFn);
        } catch (e) {
          if (!(e instanceof PatreonAuthError)) throw e;
          ctx.waitUntil(alert(env, "creator token rejected", "Patreon returned 401 for the creator token; /callback used identity data."));
        }
      }

      if (!isActive(m)) return html(200, deniedPage(reasonText(m.patronStatus ?? "free_member"), env.PATREON_URL));

      // Sign first so a signing failure leaves KV untouched.
      const key = await sign(
        { v: 1, kind: "patron", sub: m.userId, mid, iat: t, exp: t + PATRON_LIFE },
        await signingKey(env),
      );
      const { devices } = await addDevice(env.KEYS, m.userId, mid, t);
      await putMember(env.KEYS, m.userId, m.memberId!);
      return html(200, keyPage(key, devices, mid));
    } catch (e) {
      if (e instanceof PatreonError) return html(502, errorPage("Patreon is not responding; try again in a minute."));
      throw e;
    }
  };
}
