import type { Options } from 'bedrock-protocol'

export interface Config {
  host: string
  port: number
  username: string
  version: NonNullable<Options['version']>
  protocolSpoof: boolean
  viewDistance: number
  profilesFolder: string
  reconnectMinMs: number
  reconnectMaxMs: number
  answerEnabled: boolean
  llmBaseUrl: string
  llmModel: string
  llmApiKey: string
  llmTimeoutMs: number
  llmMaxTokens: number
  answerCooldownMs: number
  answerMaxPerMinute: number
  answerMaxInFlight: number
  // Lowercased. Never includes the bot's own gamertag — that name is only
  // known once bedrock-protocol resolves it post-login, so index.ts adds it
  // to this set at runtime rather than storing it here.
  botNames: string[]
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

function stringList(name: string): string[] {
  const raw = process.env[name]
  if (raw === undefined) return []
  return raw
    .split(',')
    .map((entry) => entry.trim().toLowerCase())
    .filter((entry) => entry !== '')
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
    // Announce the server's advertised protocol number when it differs from
    // what the baked data maps to (see negotiateProtocol). On by default:
    // without it the bot is offline from the moment the LATEST-tracking
    // server crosses a protocol bump until upstream minecraft-data catches
    // up, which is exactly the window it exists to cover.
    protocolSpoof: process.env.MC_PROTOCOL_SPOOF?.trim().toLowerCase() !== 'false',
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
    // Off by default: a second bot pointed at the same server with this on
    // would answer the first bot's replies right back, forever. Flip it on
    // exactly one bot.
    answerEnabled: process.env.MC_ANSWER_ENABLED?.trim().toLowerCase() === 'true',
    // Defaults to the cluster's own local instance: unmetered and free,
    // unlike a shared hosted free tier this bot could otherwise contend with.
    // Any OpenAI-compatible endpoint works here, so pointing this at a
    // different backend instead is just env vars — see the README.
    llmBaseUrl: process.env.MC_LLM_BASE_URL?.trim() || 'http://192.168.1.50:8000/v1',
    llmModel: process.env.MC_LLM_MODEL?.trim() || 'qwen/qwen3-coder-30b-a3b',
    // Empty by default: the local vLLM endpoint takes no auth. A hosted
    // OpenAI-compatible API (Groq, Gemini, LiteLLM) needs a real key here.
    llmApiKey: process.env.MC_LLM_API_KEY?.trim() || '',
    llmTimeoutMs: positiveInt('MC_LLM_TIMEOUT_MS', 8_000),
    llmMaxTokens: positiveInt('MC_LLM_MAX_TOKENS', 96),
    answerCooldownMs: positiveInt('MC_ANSWER_COOLDOWN_MS', 30_000),
    answerMaxPerMinute: positiveInt('MC_ANSWER_MAX_PER_MINUTE', 6),
    answerMaxInFlight: positiveInt('MC_LLM_MAX_IN_FLIGHT', 1),
    // Gamertags of sibling bots on the same server (e.g. fwb-afk-bot-2), so
    // this bot never answers one of them into a chat loop. Not this bot's
    // own name — see the Config field comment.
    botNames: stringList('MC_BOT_NAMES'),
  }

  if (config.reconnectMaxMs < config.reconnectMinMs) {
    throw new Error('RECONNECT_MAX_MS must be greater than or equal to RECONNECT_MIN_MS')
  }

  return config
}
