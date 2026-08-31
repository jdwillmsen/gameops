import { log } from './log'

/**
 * Bedrock point releases regularly bump the protocol *number* without
 * changing the protocol *schema* (1.26.40 -> 1.26.45 shipped as "Various bug
 * fixes" with zero packet changes), but the server hard-kicks any client
 * announcing an older number — `play_status: failed_client` before login
 * even starts. minecraft-data usually gains the new version entry days to
 * weeks later, and this image prunes all but one version's data anyway.
 *
 * So before each session: ping the server, and when it advertises a
 * different protocol number than the baked data maps to, re-map the baked
 * version to the advertised number. The session then announces the server's
 * own number while speaking the baked schema. On a schema-changing release
 * this trades a clean pre-login kick for a mid-join parse failure — same
 * reconnect loop either way, and the spoof log line names the culprit.
 */

const PING_TIMEOUT_MS = 5_000

interface Advertisement {
  protocol?: string | number
  version?: string
}

/**
 * Pure decision core: mutates `versions[version]` to match the advertised
 * protocol number. `baked` is what the library mapped `version` to before any
 * spoofing, so a server moving back (restore, rollback) clears the override.
 */
export function applyAdvertisedProtocol(
  versions: Record<string, number>,
  baked: number,
  version: string,
  advertisement: Advertisement,
): void {
  const advertised = Number(advertisement.protocol)
  if (!Number.isInteger(advertised) || advertised <= 0) {
    log('warn', 'protocol_advertisement_invalid', { protocol: advertisement.protocol ?? null })
    return
  }

  if (advertised === versions[version]) return

  if (advertised === baked) {
    versions[version] = baked
    log('info', 'protocol_spoof_cleared', { version, protocol: baked })
    return
  }

  versions[version] = advertised
  log('warn', 'protocol_spoofed', {
    version,
    baked_protocol: baked,
    advertised_protocol: advertised,
    server_version: advertisement.version ?? null,
  })
}

let bakedProtocols: Record<string, number> | undefined

/**
 * Ping the server and align the announced protocol number with what it
 * advertises. Never throws: on any ping failure the last known mapping
 * stands and the connection attempt proceeds — the server may be
 * mid-restart, which the caller's reconnect loop already handles.
 *
 * bedrock-protocol is required lazily so this module stays importable where
 * the native RakNet addon is not built (CI runs `npm ci --ignore-scripts`).
 */
export async function negotiateProtocol(host: string, port: number, version: string): Promise<void> {
  // Internal module path, not public API. bedrock-protocol is pinned to an
  // exact version in package.json, so the path and the shape of `Versions`
  // only move under a reviewed dependency bump.
  const { Versions } = require('bedrock-protocol/src/options') as { Versions: Record<string, number> }
  bakedProtocols ??= { ...Versions }
  const baked = bakedProtocols[version]
  if (baked === undefined) return

  const { ping } = require('bedrock-protocol') as {
    ping: (opts: { host: string; port: number }) => Promise<Advertisement>
  }

  let advertisement: Advertisement
  try {
    advertisement = await Promise.race([
      ping({ host, port }),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error(`ping timed out after ${PING_TIMEOUT_MS}ms`)), PING_TIMEOUT_MS).unref(),
      ),
    ])
  } catch (error) {
    log('warn', 'protocol_ping_failed', { error: (error as Error).message })
    return
  }

  applyAdvertisedProtocol(Versions, baked, version, advertisement)
}
