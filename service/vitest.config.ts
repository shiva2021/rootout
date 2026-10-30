import { cloudflareTest } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.toml" },
      miniflare: {
        bindings: {
          STATE_SECRET: "test-state-secret",
          SIGNING_KEY: "MC4CAQAwBQYDK2VwBCIEILjMQS9IV9OqOsA7Qwau7HvvLTjLUBO+qRNf2Qz0Avg+", // PKCS#8 of the golden seed
          PATREON_CLIENT_ID: "cid",
          PATREON_CLIENT_SECRET: "csecret",
          PATREON_CAMPAIGN_ID: "999",
          PUBLIC_ORIGIN: "https://keys.test",
          ALERT_GITHUB_TOKEN: "ghp_test",
          ALERT_REPO: "shiva2021/rootout",
        },
      },
    }),
  ],
});
