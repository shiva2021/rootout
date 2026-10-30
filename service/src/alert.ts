import type { Env } from "./env";

/** Placeholder: S5 replaces the body with a GitHub issue. */
export async function alert(env: Env, title: string, body: string): Promise<void> {
  if (!env.ALERT_GITHUB_TOKEN) return; // unconfigured (tests, local dev): stay quiet
  console.warn(`alert: ${title}: ${body}`);
}
