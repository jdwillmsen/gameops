export type GateResult = 'ok' | 'cooldown' | 'busy' | 'rate_limited'

export interface AnswerGate {
  /** Call before starting an LLM request. On 'ok' the caller owns a slot
   *  and must call release() exactly once, success or failure. */
  tryAcquire(key: string, now: number): GateResult
  release(): void
}

const RATE_WINDOW_MS = 60_000

/**
 * Bounds how often the bot calls the LLM backend, independent of $0 API
 * cost: an unthrottled per-message call is a self-inflicted DoS against the
 * box serving it (GPU queueing, contention with other workloads on the same
 * hardware) and a chat-spam vector. `now` is caller-supplied rather than
 * read from Date.now() internally so the gate's timing logic is testable
 * without real delays.
 */
export function createAnswerGate(limits: { cooldownMs: number; maxPerMinute: number; maxInFlight: number }): AnswerGate {
  const lastAnsweredAt = new Map<string, number>()
  const requestTimestamps: number[] = []
  let inFlight = 0

  return {
    tryAcquire(key: string, now: number): GateResult {
      if (inFlight >= limits.maxInFlight) return 'busy'

      while (requestTimestamps[0] !== undefined && now - requestTimestamps[0] >= RATE_WINDOW_MS) {
        requestTimestamps.shift()
      }
      if (requestTimestamps.length >= limits.maxPerMinute) return 'rate_limited'

      const last = lastAnsweredAt.get(key)
      if (last !== undefined && now - last < limits.cooldownMs) return 'cooldown'

      inFlight += 1
      requestTimestamps.push(now)
      lastAnsweredAt.set(key, now)
      // Bounds the map's size across a long-running session with many
      // distinct player names — anything past its own cooldown window is
      // dead weight.
      for (const [k, t] of lastAnsweredAt) {
        if (now - t >= limits.cooldownMs) lastAnsweredAt.delete(k)
      }

      return 'ok'
    },
    release(): void {
      inFlight = Math.max(0, inFlight - 1)
    },
  }
}
