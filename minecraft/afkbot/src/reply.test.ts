import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isQuestion, buildReplyPacket } from './reply'

test('flags a question from another player', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh', message: 'where is the base?' }, 'Bot'), true)
})

test('ignores a statement with no question mark', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh', message: 'nice base' }, 'Bot'), false)
})

test('ignores the bot echoing its own reply', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Bot', message: 'is this a loop?' }, 'Bot'), false)
})

test('ignores non-chat text types even with a question mark', () => {
  assert.equal(isQuestion({ type: 'popup', source_name: 'Josh', message: 'continue?' }, 'Bot'), false)
})

test('treats a missing message as not a question', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh' }, 'Bot'), false)
})

test('builds a chat packet attributed to the bot', () => {
  assert.deepEqual(buildReplyPacket('Bot', 'the spawn is at 0 64 0'), {
    type: 'chat',
    needs_translation: false,
    source_name: 'Bot',
    xuid: '',
    platform_chat_id: '',
    filtered_message: '',
    message: 'the spawn is at 0 64 0',
  })
})
