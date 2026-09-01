import { mkdirSync } from 'node:fs'
import { createClient } from 'bedrock-protocol'
import { loadConfig, type Config } from './config'
import { log } from './log'
import { negotiateProtocol } from './protocol'
import { answerQuestion } from './answer'
import { CHAT_TYPES, isQuestion, buildReplyPacket } from './reply'

let shuttingDown = false

/**
 * Runs one connection to exhaustion. Resolves when the session ends for any
 * reason; the caller decides whether to open another. Never rejects, so a
 * failure mode inside the client cannot bypass the backoff in run().
 */
function session(config: Config): Promise<void> {
  return new Promise((resolve) => {
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
      if (!CHAT_TYPES.has(packet.type)) return
      log('info', 'chat', {
        chat_type: packet.type,
        player: packet.source_name || null,
        xuid: packet.xuid || null,
        message: packet.message,
      })

      if (isQuestion(packet, config.username)) {
        answerQuestion(packet.source_name || 'a player', packet.message).then(
          (reply) => {
            client.queue('text', buildReplyPacket(config.username, reply))
            log('info', 'replied', { player: packet.source_name || null, reply })
          },
          (error: Error) => {
            // Never let a bad LLM call take down the connection — the chat
            // mirror above already logged the question either way.
            log('error', 'answer_error', { error: error.message })
          },
        )
      }
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

  log('info', 'starting', {
    host: config.host,
    port: config.port,
    username: config.username,
    version: config.version,
    profiles_folder: config.profilesFolder,
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
