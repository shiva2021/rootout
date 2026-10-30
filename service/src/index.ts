import type { Env } from "./env";
import { callbackHandler } from "./routes/callback";
import { homeHandler, startHandler } from "./routes/home";
import { renewHandler } from "./routes/renew";
import { trialHandler } from "./routes/trial";
import { route } from "./router";

const table = {
  "GET /": homeHandler,
  "GET /start": startHandler(),
  "GET /callback": callbackHandler(),
  "POST /renew": renewHandler(),
  "POST /trial": trialHandler(),
};

export default {
  fetch: (req: Request, env: Env, ctx: ExecutionContext): Promise<Response> => route(req, env, ctx, table),
  scheduled: async (_event: ScheduledController, _env: Env, _ctx: ExecutionContext): Promise<void> => {},
};
