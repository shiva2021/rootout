import type { Env } from "./env";

/** Placeholder: S5 replaces the body with a GitHub issue. */
export async function alert(_env: Env, title: string, body: string): Promise<void> {
  console.warn(`alert: ${title}: ${body}`);
}
