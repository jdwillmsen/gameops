// Bedrock chat renders long lines awkwardly and the server-side chat limit is
// tight; keep replies to a single short sentence, and bound the question we
// forward too (it comes straight from player-typed chat).
export const MAX_REPLY_CHARS = 200
export const MAX_QUESTION_CHARS = 256

export interface AnswerOptions {
  baseUrl: string
  model: string
  apiKey: string
  timeoutMs: number
  maxTokens: number
}

const SYSTEM_PROMPT =
  'You are the voice of a Minecraft Bedrock server, replying directly in its own chat. ' +
  `Answer the question in one short, plain sentence, under ${MAX_REPLY_CHARS} characters. ` +
  'No markdown, no roleplay asterisks, and never end your reply with a question mark.'

/** Pure request-shape builder — kept separate from the fetch call so the
 *  auth-header and truncation logic can be tested without a network. */
export function buildAnswerRequest(
  asker: string,
  question: string,
  opts: AnswerOptions,
): { url: string; headers: Record<string, string>; body: unknown } {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  // Omitted, not sent empty: the local vLLM endpoint takes no auth at all,
  // and some OpenAI-compatible backends reject a present-but-empty header.
  if (opts.apiKey !== '') headers.authorization = `Bearer ${opts.apiKey}`

  return {
    url: `${opts.baseUrl.replace(/\/+$/, '')}/chat/completions`,
    headers,
    body: {
      model: opts.model,
      max_tokens: opts.maxTokens,
      messages: [
        { role: 'system', content: SYSTEM_PROMPT },
        { role: 'user', content: `${asker} asked: ${question.slice(0, MAX_QUESTION_CHARS)}` },
      ],
    },
  }
}

/** Pure response parser. Returns '' on anything that isn't a normal
 *  OpenAI-shaped completion — a new/misbehaving backend should fall silent,
 *  not throw and take the answer path down with it. */
export function extractAnswerText(payload: unknown): string {
  if (typeof payload !== 'object' || payload === null) return ''
  const choices = (payload as { choices?: unknown }).choices
  if (!Array.isArray(choices) || choices.length === 0) return ''
  const message = (choices[0] as { message?: unknown } | undefined)?.message
  if (typeof message !== 'object' || message === null) return ''
  const content = (message as { content?: unknown }).content
  if (typeof content !== 'string') return ''
  // Bedrock chat is single-line; collapse anything a model wrapped anyway.
  return content.trim().replace(/\s+/g, ' ').slice(0, MAX_REPLY_CHARS)
}

export async function answerQuestion(asker: string, question: string, opts: AnswerOptions): Promise<string> {
  const { url, headers, body } = buildAnswerRequest(asker, question, opts)
  const response = await fetch(url, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(opts.timeoutMs),
  })

  if (!response.ok) {
    throw new Error(`llm request failed: ${response.status} ${response.statusText}`)
  }

  return extractAnswerText(await response.json())
}
