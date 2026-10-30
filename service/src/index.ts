import type { Env } from "./env";
import { callbackHandler } from "./routes/callback";
import { homeHandler, startHandler } from "./routes/home";
import { type Handler, json, route } from "./router";

// Stubs until the handlers land in later tasks.
const notImplemented: Handler = async () => json(501, { reason: "not_implemented" });

const table = {
  "GET /": homeHandler,
  "GET /start": startHandler(),
  "GET /callback": callbackHandler(),
  "POST /renew": notImplemented,
  "POST /trial": notImplemented,
};

export default {
  fetch: (req: Request, env: Env, ctx: ExecutionContext): Promise<Response> => route(req, env, ctx, table),
  scheduled: async (_event: ScheduledController, _env: Env, _ctx: ExecutionContext): Promise<void> => {},
};
