/** Chat the server broadcasts to every client. Other `text` types are server
 *  plumbing (tips, popups, translated system strings) and would drown the
 *  interesting lines. */
export const CHAT_TYPES = new Set(['chat', 'announcement', 'whisper'])

interface ChatPacket {
  type: string
  source_name?: string
  message?: string
}

/**
 * True for a player's chat question worth answering. Excludes the bot's own
 * broadcasts by name so a reply can never trigger itself into a loop.
 */
export function isQuestion(packet: ChatPacket, botUsername: string): boolean {
  if (!CHAT_TYPES.has(packet.type)) return false
  if (packet.source_name === botUsername) return false
  return (packet.message ?? '').includes('?')
}

/** Shape bedrock-protocol expects to broadcast a chat line as the bot. */
export function buildReplyPacket(botUsername: string, message: string) {
  return {
    type: 'chat',
    needs_translation: false,
    source_name: botUsername,
    xuid: '',
    platform_chat_id: '',
    filtered_message: '',
    message,
  }
}
