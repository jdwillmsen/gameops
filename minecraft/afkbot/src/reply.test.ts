import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isQuestion, buildReplyPacket, chatAuthor, chatAskerLabel, SERVER_ORIGIN_KEY } from './reply'

const BOT_NAMES = new Set(['bot', 'fwb-afk-bot-2'])

test('flags a question from another player', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh', message: 'where is the base?' }, BOT_NAMES), true)
})

test('ignores a statement with no question mark', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh', message: 'nice base' }, BOT_NAMES), false)
})

test('ignores a listed bot account, case-insensitively', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Bot', message: 'is this a loop?' }, BOT_NAMES), false)
})

test('ignores a sibling bot account by the same rule', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'FWB-AFK-Bot-2', message: 'are you there?' }, BOT_NAMES), false)
})

test('ignores non-chat text types even with a question mark', () => {
  assert.equal(isQuestion({ type: 'popup', source_name: 'Josh', message: 'continue?' }, BOT_NAMES), false)
})

test('treats a missing message as not a question', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: 'Josh' }, BOT_NAMES), false)
})

test('answers a server-console message with no source_name or xuid', () => {
  assert.equal(isQuestion({ type: 'announcement', source_name: '', xuid: '', message: 'server restarting soon?' }, BOT_NAMES), true)
})

test('answers a server-console message with fields entirely absent', () => {
  assert.equal(isQuestion({ type: 'announcement', message: 'anyone online?' }, BOT_NAMES), true)
})

test('drops a blank name paired with a non-empty xuid rather than guessing', () => {
  assert.equal(isQuestion({ type: 'chat', source_name: '', xuid: '123', message: 'who is this?' }, BOT_NAMES), false)
})

test('chatAuthor keys a real player by their lowercased name', () => {
  assert.equal(chatAuthor({ type: 'chat', source_name: 'Josh', xuid: '123' }), 'josh')
})

test('chatAuthor keys a server-console message under the shared sentinel', () => {
  assert.equal(chatAuthor({ type: 'announcement', source_name: '', xuid: '' }), SERVER_ORIGIN_KEY)
})

test('chatAskerLabel keeps a real player\'s original casing', () => {
  assert.equal(chatAskerLabel({ type: 'chat', source_name: 'Josh', xuid: '123' }), 'Josh')
})

test('chatAskerLabel calls a server-console message "the server"', () => {
  assert.equal(chatAskerLabel({ type: 'announcement', source_name: '', xuid: '' }), 'the server')
})

test('chatAskerLabel falls back for a whitespace-only name paired with an xuid', () => {
  assert.equal(chatAskerLabel({ type: 'chat', source_name: '   ', xuid: '123' }), 'a player')
})

test('builds a chat packet attributed to the bot', () => {
  assert.deepEqual(buildReplyPacket('Bot', 'the spawn is at 0 64 0'), {
    type: 'chat',
    category: 'authored',
    needs_translation: false,
    source_name: 'Bot',
    xuid: '',
    platform_chat_id: '',
    has_filtered_message: false,
    filtered_message: '',
    message: 'the spawn is at 0 64 0',
  })
})
