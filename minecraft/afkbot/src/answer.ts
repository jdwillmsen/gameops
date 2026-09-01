import Anthropic from '@anthropic-ai/sdk'

// Bedrock chat renders long lines awkwardly and the server-side chat limit is
// tight; keep replies to a single short sentence.
const MAX_REPLY_CHARS = 200

let client: Anthropic | undefined

function getClient(): Anthropic {
  client ??= new Anthropic()
  return client
}

/** Asks Claude to answer a player's in-game question as the server's voice. */
export async function answerQuestion(player: string, question: string): Promise<string> {
  const response = await getClient().messages.create({
    model: 'claude-opus-5',
    max_tokens: 300,
    output_config: { effort: 'low' },
    system:
      'You are the voice of a Minecraft Bedrock server, replying directly in its own chat. ' +
      `Answer the player's question in one short, plain sentence, under ${MAX_REPLY_CHARS} characters. No markdown, no roleplay asterisks.`,
    messages: [{ role: 'user', content: `${player} asked: ${question}` }],
  })

  const text = response.content.find((block): block is Anthropic.TextBlock => block.type === 'text')?.text ?? ''
  return text.trim().slice(0, MAX_REPLY_CHARS)
}
