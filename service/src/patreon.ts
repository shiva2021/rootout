import type { Env } from "./env";
import type { TokenPair } from "./kv";

const AUTHORIZE = "https://www.patreon.com/oauth2/authorize";
const TOKEN = "https://www.patreon.com/api/oauth2/token";
const API = "https://www.patreon.com/api/oauth2/v2";
const MEMBER_FIELDS = "patron_status,currently_entitled_amount_cents,is_gifted";

/** Any non-2xx from Patreon. */
export class PatreonError extends Error {
  constructor(readonly status: number, message = `Patreon returned HTTP ${status}`) {
    super(message);
    this.name = "PatreonError";
  }
}

/** HTTP 401: the token is dead. */
export class PatreonAuthError extends PatreonError {
  constructor() {
    super(401, "Patreon rejected the token");
    this.name = "PatreonAuthError";
  }
}

export interface Membership {
  userId: string;
  memberId: string | null;
  patronStatus: string | null;
  cents: number;
  gifted: boolean;
}

export const isActive = (m: Membership): boolean =>
  m.patronStatus === "active_patron" && (m.cents > 0 || m.gifted === true);

export function authorizeUrl(env: Env, state: string): string {
  const q = new URLSearchParams({
    response_type: "code",
    client_id: env.PATREON_CLIENT_ID,
    redirect_uri: env.PUBLIC_ORIGIN + "/callback",
    scope: "identity",
    state,
  });
  return `${AUTHORIZE}?${q}`;
}

async function checked(res: Response): Promise<unknown> {
  if (res.status === 401) throw new PatreonAuthError();
  if (!res.ok) throw new PatreonError(res.status);
  return res.json();
}

async function tokenRequest(
  form: Record<string, string>, fetchFn: typeof fetch, clock: () => number,
): Promise<TokenPair> {
  const res = await fetchFn(TOKEN, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form).toString(),
  });
  const o = (await checked(res)) as { access_token?: unknown; refresh_token?: unknown };
  if (typeof o.access_token !== "string" || typeof o.refresh_token !== "string") {
    throw new PatreonError(res.status, "Patreon token response is missing tokens");
  }
  return { access: o.access_token, refresh: o.refresh_token, updated: clock() };
}

const nowSeconds = () => Math.floor(Date.now() / 1000);

export function exchangeCode(
  env: Env, code: string, fetchFn: typeof fetch = fetch, clock: () => number = nowSeconds,
): Promise<TokenPair> {
  return tokenRequest({
    code,
    grant_type: "authorization_code",
    client_id: env.PATREON_CLIENT_ID,
    client_secret: env.PATREON_CLIENT_SECRET,
    redirect_uri: env.PUBLIC_ORIGIN + "/callback",
  }, fetchFn, clock);
}

export function refreshToken(
  env: Env, refresh: string, fetchFn: typeof fetch = fetch, clock: () => number = nowSeconds,
): Promise<TokenPair> {
  return tokenRequest({
    grant_type: "refresh_token",
    refresh_token: refresh,
    client_id: env.PATREON_CLIENT_ID,
    client_secret: env.PATREON_CLIENT_SECRET,
  }, fetchFn, clock);
}

interface JsonApiResource {
  id?: string;
  type?: string;
  attributes?: { patron_status?: string | null; currently_entitled_amount_cents?: number; is_gifted?: boolean };
  relationships?: {
    campaign?: { data?: { id?: string } | null };
    user?: { data?: { id?: string } | null };
    memberships?: { data?: { id?: string }[] };
  };
}

function toMembership(userId: string, member: JsonApiResource | null): Membership {
  const a = member?.attributes ?? {};
  return {
    userId,
    memberId: member?.id ?? null,
    patronStatus: a.patron_status ?? null,
    cents: a.currently_entitled_amount_cents ?? 0,
    gifted: a.is_gifted === true,
  };
}

const bearer = (token: string): RequestInit => ({ headers: { authorization: `Bearer ${token}` } });

/** The signed-in user's own membership of our campaign. Other campaigns are ignored. */
export async function identityMembership(
  accessToken: string, campaignId: string, fetchFn: typeof fetch = fetch,
): Promise<Membership> {
  const q = new URLSearchParams({
    include: "memberships.campaign",
    "fields[member]": MEMBER_FIELDS,
    "fields[campaign]": "",
  });
  const body = (await checked(await fetchFn(`${API}/identity?${q}`, bearer(accessToken)))) as {
    data?: JsonApiResource; included?: JsonApiResource[];
  };
  const userId = body.data?.id;
  if (typeof userId !== "string") throw new PatreonError(200, "Patreon identity response has no user id");
  const ours = (body.included ?? []).find(
    (r) => r.type === "member" && r.relationships?.campaign?.data?.id === campaignId,
  );
  return toMembership(userId, ours ?? null);
}

/** One member record, read with the creator token. */
export async function memberById(
  creatorAccess: string, memberId: string, fetchFn: typeof fetch = fetch,
): Promise<Membership> {
  const q = new URLSearchParams({ include: "user", "fields[member]": MEMBER_FIELDS });
  const body = (await checked(
    await fetchFn(`${API}/members/${encodeURIComponent(memberId)}?${q}`, bearer(creatorAccess)),
  )) as { data?: JsonApiResource };
  const userId = body.data?.relationships?.user?.data?.id;
  if (!body.data || typeof userId !== "string") throw new PatreonError(200, "Patreon member response has no user id");
  return toMembership(userId, body.data);
}
