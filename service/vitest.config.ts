import { cloudflareTest } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.toml" },
      miniflare: {
        bindings: {
          STATE_SECRET: "test-state-secret",
          PATREON_CLIENT_ID: "cid",
          PATREON_CLIENT_SECRET: "csecret",
          PATREON_CAMPAIGN_ID: "999",
          PUBLIC_ORIGIN: "https://keys.test",
        },
      },
    }),
  ],
});
