import type { Options } from 'bedrock-protocol'

export interface Config {
  host: string
  port: number
  username: string
  version: NonNullable<Options['version']>
  viewDistance: number
  profilesFolder: string
  reconnectMinMs: number
  reconnectMaxMs: number
}

function required(name: string): string {
  const value = process.env[name]
  if (value === undefined || value.trim() === '') {
    throw new Error(`${name} is required`)
  }
  return value.trim()
}

function positiveInt(name: string, fallback: number): number {
  const raw = process.env[name]
  if (raw === undefined || raw.trim() === '') return fallback
  const value = Number(raw)
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`${name} must be a positive integer, got ${JSON.stringify(raw)}`)
  }
  return value
}

export function loadConfig(): Config {
  const config: Config = {
    host: required('MC_HOST'),
    port: positiveInt('MC_PORT', 19132),
    // Not the gamertag the server sees — Xbox Live supplies that. This is the
    // key prismarine-auth files the cached token under inside profilesFolder,
    // so changing it orphans the existing cache and forces a fresh device-code
    // login.
    username: required('MC_USERNAME'),
    // minecraft-data carries no entry for every Bedrock point release, so the
    // protocol version the client negotiates is pinned separately from the
    // server's own version rather than derived from it — the server runs
    // 1.26.43.1, for which no protocol data exists.
    //
    // The cast is around a stale upstream typing, not a wrong value: the
    // shipped index.d.ts stops its Version union at 1.21.93 before jumping to
    // 1.26.0, omitting the 1.26.x releases the library itself supports. Its
    // own options.js sets CURRENT_VERSION to 1.26.40 and minecraft-data has
    // the matching entry. Pinned explicitly rather than left to default so a
    // library upgrade cannot move the negotiated protocol on its own.
    version: (process.env.MC_VERSION?.trim() || '1.26.40') as NonNullable<Options['version']>,
    // Must be a writable path that survives restarts. prismarine-auth caches
    // the Xbox Live tokens here; losing it means another interactive
    // device-code login.
    profilesFolder: process.env.AUTH_CACHE_DIR?.trim() || '/data/auth',
    // Governs only how many chunks the server sends this client. Simulation
    // is bounded by the server's own tick-distance, so a small value here
    // costs bandwidth and nothing else — it does not shrink the ticking area
    // the bot's presence keeps active.
    viewDistance: positiveInt('MC_VIEW_DISTANCE', 4),
    reconnectMinMs: positiveInt('RECONNECT_MIN_MS', 5_000),
    reconnectMaxMs: positiveInt('RECONNECT_MAX_MS', 300_000),
  }

  if (config.reconnectMaxMs < config.reconnectMinMs) {
    throw new Error('RECONNECT_MAX_MS must be greater than or equal to RECONNECT_MIN_MS')
  }

  return config
}
