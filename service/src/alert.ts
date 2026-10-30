import type { Env } from "./env";

const LABEL = "rootout-keys";

/**
 * Raises an operator alert as a GitHub issue: comments on an open issue with the same title, else opens one.
 * Never throws and never blocks a request path (callers hand it to waitUntil); failures are logged only.
 */
export async function alert(
  env: Env, title: string, body: string, fetchFn: typeof fetch = fetch,
): Promise<void> {
  if (!env.ALERT_GITHUB_TOKEN) {
    console.warn("alert (unconfigured): " + title);
    return;
  }
  const base = `https://api.github.com/repos/${env.ALERT_REPO}/issues`;
  const headers = {
    Authorization: `Bearer ${env.ALERT_GITHUB_TOKEN}`,
    Accept: "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "rootout-keys",
  };
  try {
    const list = await fetchFn(`${base}?labels=${LABEL}&state=open&per_page=50`, { headers });
    if (!list.ok) throw new Error(`list issues: HTTP ${list.status}`);
    const issues = (await list.json()) as { number: number; title: string }[];
    const existing = issues.find((i) => i.title === title);
    const post = await fetchFn(existing ? `${base}/${existing.number}/comments` : base, {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body: JSON.stringify(existing ? { body } : { title, body, labels: [LABEL] }),
    });
    if (!post.ok) throw new Error(`post issue: HTTP ${post.status}`);
  } catch (e) {
    console.error(`alert failed (${title}): ${e instanceof Error ? e.message : String(e)}`);
  }
}
