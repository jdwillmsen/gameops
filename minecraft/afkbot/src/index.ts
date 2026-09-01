import { mkdirSync } from 'node:fs'
import { createClient, type Client } from 'bedrock-protocol'
import { loadConfig, type Config } from './config'
import { log } from './log'
import { negotiateProtocol } from './protocol'
import { answerQuestion } from './answer'
import { TEXT_TYPES, isQuestion, chatAuthor, buildReplyPacket, SERVER_ORIGIN_KEY } from './reply'
import { createAnswerGate, type AnswerGate } from './throttle'

let shuttingDown = false

// One gate for the whole process, not per-session: a reconnect should not
// hand a flood-in-progress a fresh budget.
let answerGate: AnswerGate | undefined

/**
 * bedrock-protocol reassigns `username` from the login-time value (the
 * prismarine-auth cache key, config.username) to the real Xbox Live
 * gamertag once auth completes — see postAuthenticate in its src/client/
 * auth.js. The shipped index.d.ts never grew this field on Client, hence
 * the cast. Using the pre-login value here would make the bot's own
 * self-exclusion check compare against a name the server never actually
 * sends, letting the bot answer its own broadcasts forever.
 */
function resolveSelfName(client: Client, fallback: string): string {
  const live = (client as unknown as { username?: string }).username
  return live && live.trim() !== '' ? live.trim() : fallback
}

/**
 * Runs one connection to exhaustion. Resolves when the session ends for any
 * reason; the caller decides whether to open another. Never rejects, so a
 * failure mode inside the client cannot bypass the backoff in run().
 */
function session(config: Config): Promise<void> {
  return new Promise((resolve) => {
    const configuredBotNames = new Set(config.botNames)
    const llmOptions = {
      baseUrl: config.llmBaseUrl,
      model: config.llmModel,
      apiKey: config.llmApiKey,
      timeoutMs: config.llmTimeoutMs,
      maxTokens: config.llmMaxTokens,
    }

    let settled = false
    const end = (event: string, fields: Record<string, unknown> = {}) => {
      if (settled) return
      settled = true
      log(event === 'session_error' ? 'error' : 'warn', event, fields)
      try {
        client.disconnect()
      } catch {
        // Already torn down by whatever ended the session; nothing to close.
      }
      resolve()
    }

    const client = createClient({
      host: config.host,
      port: config.port,
      username: config.username,
      version: config.version,
      viewDistance: config.viewDistance,
      offline: false,
      profilesFolder: config.profilesFolder,
      // Printed rather than thrown: the first start of a fresh token cache
      // genuinely requires a human, and the code is only visible here.
      onMsaCode: (data) => {
        log('warn', 'device_code_required', {
          user_code: data.user_code,
          verification_uri: data.verification_uri,
          expires_in_seconds: data.expires_in,
        })
      },
    })

    client.on('join', () => log('info', 'joined', { host: config.host, port: config.port }))

    client.on('spawn', () => log('info', 'spawned'))

    client.on('text', (packet) => {
      // Logged in full — including types the bot never answers — so the
      // real wire shape of a console `send-command say`/`tellraw` message
      // can be confirmed in Loki before ANSWERABLE_TYPES is ever widened to
      // include them (see reply.ts).
      if (!TEXT_TYPES.has(packet.type)) return
      log('info', 'chat', {
        chat_type: packet.type,
        category: packet.category ?? null,
        player: packet.source_name || null,
        xuid: packet.xuid || null,
        message: packet.message,
      })

      if (!config.answerEnabled) return

      const selfName = resolveSelfName(client, config.username)
      const botNames = new Set(configuredBotNames).add(selfName.trim().toLowerCase())
      if (!isQuestion(packet, botNames)) return

      const author = chatAuthor(packet)
      const source = author === SERVER_ORIGIN_KEY ? 'server' : 'player'
      const gateResult = answerGate!.tryAcquire(author, Date.now())
      if (gateResult !== 'ok') {
        log('info', 'answer_skipped', { reason: gateResult, source, player: packet.source_name || null })
        return
      }

      const startedAt = Date.now()
      answerQuestion(packet.source_name || 'the server', packet.message ?? '', llmOptions)
        .then((reply) => {
          client.queue('text', buildReplyPacket(selfName, reply))
          log('info', 'replied', { source, player: packet.source_name || null, reply, ms: Date.now() - startedAt })
        })
        .catch((error: Error) => {
          // Never let a bad LLM call take down the connection — the chat
          // mirror above already logged the question either way.
          log('error', 'answer_error', { source, player: packet.source_name || null, error: error.message, ms: Date.now() - startedAt })
        })
        .finally(() => answerGate!.release())
    })

    client.on('disconnect', (packet) => {
      end('disconnected', { reason: packet?.message ?? packet?.reason ?? null })
    })

    client.on('kick', (packet) => {
      end('kicked', { reason: packet?.message ?? null })
    })

    client.on('error', (error: Error) => {
      end('session_error', { error: error.message })
    })

    client.on('close', () => end('connection_closed'))
  })
}

/**
 * Reconnect forever with exponential backoff and jitter. Bedrock disconnects
 * for ordinary reasons — server restart, world save, a rolling update of the
 * StatefulSet — so a session ending is expected rather than exceptional, and
 * exiting the process would just move the retry loop into Kubernetes with a
 * far blunter backoff.
 *
 * The delay is deliberately not reset to the floor on every successful
 * connection: a bot that connects and is immediately kicked (not on the
 * allowlist, expired credentials) would otherwise retry every few seconds
 * forever, hammering Xbox Live auth. It resets only once a session has lasted
 * long enough to count as genuinely working.
 */
const STABLE_SESSION_MS = 60_000

async function run(): Promise<void> {
  const config = loadConfig()
  mkdirSync(config.profilesFolder, { recursive: true })

  answerGate = createAnswerGate({
    cooldownMs: config.answerCooldownMs,
    maxPerMinute: config.answerMaxPerMinute,
    maxInFlight: config.answerMaxInFlight,
  })

  log('info', 'starting', {
    host: config.host,
    port: config.port,
    username: config.username,
    version: config.version,
    profiles_folder: config.profilesFolder,
    answer_enabled: config.answerEnabled,
    llm_base_url: config.llmBaseUrl,
    llm_model: config.llmModel,
    llm_key_present: config.llmApiKey !== '',
  })

  let delay = config.reconnectMinMs

  while (!shuttingDown) {
    // Every attempt, not just the first: the server restarts hourly when its
    // LATEST-tracking version moves, so the protocol can change mid-run.
    if (config.protocolSpoof) {
      await negotiateProtocol(config.host, config.port, config.version)
    }
    const startedAt = Date.now()
    await session(config)
    if (shuttingDown) break

    const lasted = Date.now() - startedAt
    delay = lasted >= STABLE_SESSION_MS ? config.reconnectMinMs : Math.min(delay * 2, config.reconnectMaxMs)

    // Jitter so a server restart does not have every future bot reconnecting
    // in lockstep.
    const wait = Math.round(delay * (0.5 + Math.random() * 0.5))
    log('info', 'reconnecting', { in_ms: wait, session_lasted_ms: lasted })
    await new Promise((resolve) => setTimeout(resolve, wait))
  }

  log('info', 'stopped')
}

for (const signal of ['SIGINT', 'SIGTERM'] as const) {
  process.on(signal, () => {
    if (shuttingDown) return
    shuttingDown = true
    log('info', 'shutdown_signal', { signal })
    // Give the current session a moment to close cleanly, then leave regardless.
    setTimeout(() => process.exit(0), 5_000).unref()
  })
}

run().catch((error: Error) => {
  log('error', 'fatal', { error: error.message })
  process.exit(1)
})
