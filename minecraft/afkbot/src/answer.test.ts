import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildAnswerRequest, extractAnswerText, MAX_QUESTION_CHARS, MAX_REPLY_CHARS } from './answer'

const BASE_OPTS = { baseUrl: 'http://192.168.1.50:8000/v1', model: 'qwen/qwen3-coder-30b-a3b', apiKey: '', timeoutMs: 8_000, maxTokens: 96 }

test('omits the auth header when no api key is configured', () => {
  const { headers } = buildAnswerRequest('Josh', 'where is spawn?', BASE_OPTS)
  assert.equal('authorization' in headers, false)
})

test('sends a bearer auth header when an api key is configured', () => {
  const { headers } = buildAnswerRequest('Josh', 'where is spawn?', { ...BASE_OPTS, apiKey: 'sk-test' })
  assert.equal(headers.authorization, 'Bearer sk-test')
})

test('truncates an overlong question before sending it', () => {
  const longQuestion = `${'a'.repeat(MAX_QUESTION_CHARS + 50)}?`
  const { body } = buildAnswerRequest('Josh', longQuestion, BASE_OPTS)
  const userContent = (body as { messages: { role: string; content: string }[] }).messages[1]?.content ?? ''
  assert.equal(userContent.length, `Josh asked: `.length + MAX_QUESTION_CHARS)
})

test('carries model and max_tokens from options', () => {
  const { body } = buildAnswerRequest('Josh', 'hi?', { ...BASE_OPTS, model: 'llama-3.3-70b-versatile', maxTokens: 64 })
  assert.equal((body as { model: string }).model, 'llama-3.3-70b-versatile')
  assert.equal((body as { max_tokens: number }).max_tokens, 64)
})

test('extracts a normal completion', () => {
  const payload = { choices: [{ message: { content: 'spawn is at 0 64 0' } }] }
  assert.equal(extractAnswerText(payload), 'spawn is at 0 64 0')
})

test('clips an overlong completion', () => {
  const payload = { choices: [{ message: { content: 'a'.repeat(MAX_REPLY_CHARS + 50) } }] }
  assert.equal(extractAnswerText(payload).length, MAX_REPLY_CHARS)
})

test('collapses a multi-line completion to one line', () => {
  const payload = { choices: [{ message: { content: 'line one\nline two' } }] }
  assert.equal(extractAnswerText(payload), 'line one line two')
})

test('returns empty string for a malformed payload', () => {
  assert.equal(extractAnswerText({}), '')
  assert.equal(extractAnswerText({ choices: [] }), '')
  assert.equal(extractAnswerText({ choices: [{ message: {} }] }), '')
  assert.equal(extractAnswerText(null), '')
  assert.equal(extractAnswerText('not json'), '')
})
