import { errorPage, homePage, html } from "../page";
import { authorizeUrl } from "../patreon";
import type { Handler } from "../router";
import { isMid } from "../sign";
import { signState } from "../state";

const STATE_LIFE = 600;

export const homeHandler: Handler = async (req) => {
  const m = new URL(req.url).searchParams.get("m");
  return html(200, homePage(m !== null && isMid(m) ? m : null));
};

export function startHandler(deps: { now?: () => number } = {}): Handler {
  const now = deps.now ?? (() => Math.floor(Date.now() / 1000));
  return async (req, env) => {
    const m = new URL(req.url).searchParams.get("m");
    if (m === null || !isMid(m)) return html(400, errorPage("That machine code is not valid. Run rootout license on your Mac to see it."));
    const state = await signState(env, { mid: m, nonce: crypto.randomUUID(), exp: now() + STATE_LIFE });
    return new Response(null, { status: 302, headers: { location: authorizeUrl(env, state), "cache-control": "no-store" } });
  };
}
