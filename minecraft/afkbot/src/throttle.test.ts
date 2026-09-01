import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createAnswerGate } from './throttle'

test('blocks a second request from the same key inside the cooldown, then allows it after', () => {
  const gate = createAnswerGate({ cooldownMs: 30_000, maxPerMinute: 100, maxInFlight: 10 })
  assert.equal(gate.tryAcquire('josh', 0), 'ok')
  gate.release()
  assert.equal(gate.tryAcquire('josh', 10_000), 'cooldown')
  assert.equal(gate.tryAcquire('josh', 30_000), 'ok')
})

test('keys cooldowns independently per author', () => {
  const gate = createAnswerGate({ cooldownMs: 30_000, maxPerMinute: 100, maxInFlight: 10 })
  assert.equal(gate.tryAcquire('josh', 0), 'ok')
  gate.release()
  assert.equal(gate.tryAcquire('<server>', 1_000), 'ok')
})

test('rate-limits after maxPerMinute requests, then refills as the window slides', () => {
  const gate = createAnswerGate({ cooldownMs: 0, maxPerMinute: 2, maxInFlight: 10 })
  assert.equal(gate.tryAcquire('a', 0), 'ok')
  gate.release()
  assert.equal(gate.tryAcquire('b', 100), 'ok')
  gate.release()
  assert.equal(gate.tryAcquire('c', 200), 'rate_limited')
  assert.equal(gate.tryAcquire('c', 60_001), 'ok')
})

test('caps in-flight requests and frees a slot on release', () => {
  const gate = createAnswerGate({ cooldownMs: 0, maxPerMinute: 100, maxInFlight: 1 })
  assert.equal(gate.tryAcquire('a', 0), 'ok')
  assert.equal(gate.tryAcquire('b', 1), 'busy')
  gate.release()
  assert.equal(gate.tryAcquire('b', 2), 'ok')
})
