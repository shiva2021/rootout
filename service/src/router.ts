import type { Env } from "./env";

export type Handler = (req: Request, env: Env, ctx: ExecutionContext) => Promise<Response>;
export type RouteKey = "GET /" | "GET /start" | "GET /callback" | "POST /renew" | "POST /trial";

export const json = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/** Exact method and path match; anything else is a 404. */
export function route(req: Request, env: Env, ctx: ExecutionContext, table: Record<RouteKey, Handler>): Promise<Response> {
  const key = `${req.method} ${new URL(req.url).pathname}`;
  const handler = Object.hasOwn(table, key) ? table[key as RouteKey] : undefined;
  return handler ? handler(req, env, ctx) : Promise.resolve(json(404, { reason: "not_found" }));
}
