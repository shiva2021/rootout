import { describe, expect, it } from "vitest";
import {
  PatreonAuthError, PatreonError, authorizeUrl, exchangeCode, identityMembership, isActive, memberById, refreshToken,
  type Membership,
} from "../src/patreon";
import { env, jsonResponse, stubFetch } from "./helpers";

const tokens = { access_token: "acc", refresh_token: "ref", expires_in: 2678400, scope: "identity", token_type: "Bearer" };

describe("patreon", () => {
  it("builds the authorize url", () => {
    const u = new URL(authorizeUrl(env, "st"));
    expect(u.origin + u.pathname).toBe("https://www.patreon.com/oauth2/authorize");
    expect(Object.fromEntries(u.searchParams)).toEqual({
      response_type: "code", client_id: "cid", redirect_uri: "https://keys.test/callback", scope: "identity", state: "st",
    });
  });

  it("exchangeCode posts form fields and parses tokens", async () => {
    const { fetchFn, calls } = stubFetch(jsonResponse(200, tokens));
    const pair = await exchangeCode(env, "the-code", fetchFn, () => 1000);
    expect(pair).toEqual({ access: "acc", refresh: "ref", updated: 1000 });
    expect(calls[0].url).toBe("https://www.patreon.com/api/oauth2/token");
    expect(calls[0].init.method).toBe("POST");
    expect(new Headers(calls[0].init.headers).get("content-type")).toBe("application/x-www-form-urlencoded");
    expect(Object.fromEntries(new URLSearchParams(calls[0].init.body as string))).toEqual({
      code: "the-code", grant_type: "authorization_code", client_id: "cid", client_secret: "csecret",
      redirect_uri: "https://keys.test/callback",
    });
  });

  it("refreshToken posts the refresh grant", async () => {
    const { fetchFn, calls } = stubFetch(jsonResponse(200, tokens));
    const pair = await refreshToken(env, "old-ref", fetchFn, () => 5);
    expect(pair).toEqual({ access: "acc", refresh: "ref", updated: 5 });
    expect(Object.fromEntries(new URLSearchParams(calls[0].init.body as string))).toEqual({
      grant_type: "refresh_token", refresh_token: "old-ref", client_id: "cid", client_secret: "csecret",
    });
  });

  it("identityMembership picks our campaign and ignores others", async () => {
    const { fetchFn, calls } = stubFetch(jsonResponse(200, {
      data: { id: "u1", type: "user", relationships: { memberships: { data: [{ id: "m-other", type: "member" }, { id: "m-ours", type: "member" }] } } },
      included: [
        { id: "m-other", type: "member", attributes: { patron_status: "active_patron", currently_entitled_amount_cents: 900, is_gifted: false }, relationships: { campaign: { data: { id: "111", type: "campaign" } } } },
        { id: "m-ours", type: "member", attributes: { patron_status: "active_patron", currently_entitled_amount_cents: 500, is_gifted: false }, relationships: { campaign: { data: { id: "999", type: "campaign" } } } },
        { id: "999", type: "campaign", attributes: {} },
      ],
    }));
    const m = await identityMembership("tok", "999", fetchFn);
    expect(m).toEqual({ userId: "u1", memberId: "m-ours", patronStatus: "active_patron", cents: 500, gifted: false });
    const u = new URL(calls[0].url);
    expect(u.origin + u.pathname).toBe("https://www.patreon.com/api/oauth2/v2/identity");
    expect(u.searchParams.get("include")).toBe("memberships.campaign");
    expect(u.searchParams.get("fields[member]")).toBe("patron_status,currently_entitled_amount_cents,is_gifted");
    expect(u.searchParams.get("fields[campaign]")).toBe("");
    expect(new Headers(calls[0].init.headers).get("authorization")).toBe("Bearer tok");
  });

  it("identityMembership returns memberId null when not a member", async () => {
    const { fetchFn } = stubFetch(jsonResponse(200, { data: { id: "u2", type: "user" } }));
    expect(await identityMembership("tok", "999", fetchFn)).toEqual({
      userId: "u2", memberId: null, patronStatus: null, cents: 0, gifted: false,
    });
  });

  it("memberById maps fields", async () => {
    const { fetchFn, calls } = stubFetch(jsonResponse(200, {
      data: { id: "m1", type: "member", attributes: { patron_status: null, currently_entitled_amount_cents: 0, is_gifted: true }, relationships: { user: { data: { id: "u9", type: "user" } } } },
    }));
    expect(await memberById("creator", "m1", fetchFn)).toEqual({ userId: "u9", memberId: "m1", patronStatus: null, cents: 0, gifted: true });
    const u = new URL(calls[0].url);
    expect(u.pathname).toBe("/api/oauth2/v2/members/m1");
    expect(u.searchParams.get("include")).toBe("user");
    expect(u.searchParams.get("fields[member]")).toBe("patron_status,currently_entitled_amount_cents,is_gifted");
    expect(new Headers(calls[0].init.headers).get("authorization")).toBe("Bearer creator");
  });

  it("isActive table", () => {
    const m = (patronStatus: string | null, cents: number, gifted: boolean): Membership =>
      ({ userId: "u", memberId: "m", patronStatus, cents, gifted });
    expect(isActive(m("active_patron", 500, false))).toBe(true);
    expect(isActive(m("active_patron", 0, true))).toBe(true);
    expect(isActive(m("active_patron", 0, false))).toBe(false);
    expect(isActive(m("declined_patron", 500, false))).toBe(false);
    expect(isActive(m("former_patron", 500, true))).toBe(false);
    expect(isActive(m(null, 500, true))).toBe(false);
  });

  it("401 throws PatreonAuthError", async () => {
    const a = stubFetch(jsonResponse(401, { errors: [] }));
    await expect(identityMembership("bad", "999", a.fetchFn)).rejects.toBeInstanceOf(PatreonAuthError);
    const b = stubFetch(jsonResponse(401, {}));
    await expect(memberById("bad", "m1", b.fetchFn)).rejects.toBeInstanceOf(PatreonAuthError);
    const c = stubFetch(jsonResponse(401, {}));
    await expect(refreshToken(env, "r", c.fetchFn)).rejects.toBeInstanceOf(PatreonAuthError);
  });

  it("other non-2xx throws PatreonError with the status", async () => {
    const { fetchFn } = stubFetch(jsonResponse(429, {}));
    const err = await identityMembership("t", "999", fetchFn).catch((e) => e);
    expect(err).toBeInstanceOf(PatreonError);
    expect(err).not.toBeInstanceOf(PatreonAuthError);
    expect(err.status).toBe(429);
  });
});
