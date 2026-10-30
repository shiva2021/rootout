export interface TokenPair { access: string; refresh: string; updated: number }
export interface Device { mid: string; iat: number }

const DEVICE_CAP = 2;

/** The current creator token pair, or the previous one when the current is missing. */
export async function getCreator(kv: KVNamespace): Promise<TokenPair | null> {
  return (await kv.get<TokenPair>("creator", "json")) ?? (await kv.get<TokenPair>("creator_prev", "json"));
}

// putCreator and addDevice are read-modify-write on KV, so they are not atomic. Concurrent requests can briefly
// exceed the device cap or clobber creator_prev. That is accepted: traffic is low and the next write self-heals.

/** Keeps the pair being replaced as creator_prev so a failed refresh write never loses both. */
export async function putCreator(kv: KVNamespace, pair: TokenPair): Promise<void> {
  const current = await kv.get("creator");
  if (current !== null) await kv.put("creator_prev", current);
  await kv.put("creator", JSON.stringify(pair));
}

export async function getDevices(kv: KVNamespace, userId: string): Promise<Device[]> {
  return (await kv.get<Device[]>(`devices:${userId}`, "json")) ?? [];
}

/** Newest last, capped at 2. A known mid changes nothing; otherwise the oldest by iat makes room. */
export async function addDevice(
  kv: KVNamespace, userId: string, mid: string, now: number,
): Promise<{ devices: Device[]; evicted: Device | null }> {
  const devices = await getDevices(kv, userId);
  if (devices.some((d) => d.mid === mid)) return { devices, evicted: null };
  devices.push({ mid, iat: now });
  let evicted: Device | null = null;
  if (devices.length > DEVICE_CAP) {
    let oldest = 0;
    for (let i = 1; i < devices.length - 1; i++) if (devices[i].iat < devices[oldest].iat) oldest = i;
    evicted = devices.splice(oldest, 1)[0];
  }
  await kv.put(`devices:${userId}`, JSON.stringify(devices));
  return { devices, evicted };
}

export async function hasDevice(kv: KVNamespace, userId: string, mid: string): Promise<boolean> {
  return (await getDevices(kv, userId)).some((d) => d.mid === mid);
}

export async function trialSeen(kv: KVNamespace, mid: string): Promise<boolean> {
  return (await kv.get(`trial:${mid}`)) !== null;
}

export async function markTrial(kv: KVNamespace, mid: string, now: number): Promise<void> {
  await kv.put(`trial:${mid}`, JSON.stringify({ iat: now }));
}

export async function getMember(kv: KVNamespace, userId: string): Promise<string | null> {
  return kv.get(`member:${userId}`);
}

export async function putMember(kv: KVNamespace, userId: string, memberId: string): Promise<void> {
  await kv.put(`member:${userId}`, memberId);
}
