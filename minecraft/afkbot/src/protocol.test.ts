import { test } from 'node:test'
import assert from 'node:assert/strict'
import { applyAdvertisedProtocol } from './protocol'

// The live bug this locks down: server advertised 2169 (1.26.45), library
// mapped 1.26.40 to 2168, server kicked the bot with failed_client before
// login. The fix announces the advertised number over the baked schema.
test('re-maps the baked version to a newer advertised protocol', () => {
  const versions = { '1.26.40': 2168 }
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: '2169', version: '1.26.45' })
  assert.equal(versions['1.26.40'], 2169)
})

test('leaves the mapping alone when the server matches it', () => {
  const versions = { '1.26.40': 2168 }
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: '2168', version: '1.26.40' })
  assert.equal(versions['1.26.40'], 2168)
})

test('clears a spoof when the server moves back to the baked protocol', () => {
  const versions = { '1.26.40': 2169 }
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: '2168', version: '1.26.40' })
  assert.equal(versions['1.26.40'], 2168)
})

test('ignores an unparsable advertisement', () => {
  const versions = { '1.26.40': 2168 }
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: undefined, version: undefined })
  assert.equal(versions['1.26.40'], 2168)
})

test('is idempotent across repeated pings of the same newer server', () => {
  const versions = { '1.26.40': 2168 }
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: '2169' })
  applyAdvertisedProtocol(versions, 2168, '1.26.40', { protocol: '2169' })
  assert.equal(versions['1.26.40'], 2169)
})
