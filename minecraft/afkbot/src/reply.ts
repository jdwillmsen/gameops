/** Every `text` packet type the protocol defines. Logged in full so the
 *  server's own console messages (`say`, `tellraw`) can be observed and
 *  their real shape confirmed before ANSWERABLE_TYPES is ever widened to
 *  include them. */
export const TEXT_TYPES = new Set([
  'raw',
  'chat',
  'translation',
  'popup',
  'jukebox_popup',
  'tip',
  'system',
  'whisper',
  'announcement',
  'json_whisper',
  'json',
  'json_announcement',
])

/** Types that carry a `source_name` at all, per the Bedrock protocol schema.
 *  Everything else (`raw`, `json`, ...) has no sender field to key a reply
 *  or a per-author cooldown on, so it is never a candidate for answering. */
export const ANSWERABLE_TYPES = new Set(['chat', 'announcement', 'whisper'])

/** Cooldown-map key for a message with no player identity — an empty
 *  `source_name` and `xuid` together, which is what a server console command
 *  (`send-command say ...`) looks like on the wire. */
export const SERVER_ORIGIN_KEY = '<server>'

interface ChatPacket {
  type: string
  source_name?: string
  xuid?: string
  message?: string
}

/**
 * Identity to key a per-author cooldown on: the lowercased sender name, or
 * SERVER_ORIGIN_KEY when both `source_name` and `xuid` are empty. Bedrock
 * gamertag casing on the wire is not guaranteed to match how a human typed
 * it into config, hence the lowercasing.
 */
export function chatAuthor(packet: ChatPacket): string {
  const name = packet.source_name?.trim() ?? ''
  const xuid = packet.xuid?.trim() ?? ''
  return name === '' && xuid === '' ? SERVER_ORIGIN_KEY : name.toLowerCase()
}

/**
 * True for a chat line worth answering: an answerable type, containing '?',
 * from neither a listed bot account nor a packet with a blank name but a
 * non-empty xuid (malformed/unidentifiable — not the same thing as the
 * server's genuinely blank identity, so it is dropped rather than guessed
 * at). `botNames` must already include the bot's own resolved gamertag, not
 * just its sibling bots, so a reply can never trigger itself into a loop —
 * see buildReplyPacket, which is why it always sets a real `source_name`.
 */
export function isQuestion(packet: ChatPacket, botNames: ReadonlySet<string>): boolean {
  if (!ANSWERABLE_TYPES.has(packet.type)) return false

  const author = chatAuthor(packet)
  // '' is chatAuthor's output for a blank name paired with a non-empty xuid
  // — unidentifiable, not the same thing as the server's genuinely blank
  // identity (SERVER_ORIGIN_KEY), so it is dropped rather than guessed at.
  if (author === '') return false
  if (author !== SERVER_ORIGIN_KEY && botNames.has(author)) return false

  return (packet.message ?? '').includes('?')
}

/**
 * Display name for the LLM prompt/log — real casing, trimmed, or 'the
 * server' for a server-origin message. Derived from chatAuthor rather than
 * re-trimming source_name itself, so a whitespace-only name can never read
 * as a real asker here while chatAuthor buckets the same packet as
 * server-origin for the cooldown key.
 */
export function chatAskerLabel(packet: ChatPacket): string {
  return chatAuthor(packet) === SERVER_ORIGIN_KEY ? 'the server' : (packet.source_name?.trim() || 'a player')
}

/** Shape bedrock-protocol expects to broadcast a chat line as the bot.
 *  `category`/`has_filtered_message` are required by the 1.26.x wire schema
 *  (protodef throws on a missing mapper value without them). `category:
 *  'authored'` is the schema's own player-chat-vs-system-message flag
 *  (mapper values message_only/authored/parameters) — this message has a
 *  named sender, so it is authored, not a bare system string. */
export function buildReplyPacket(botUsername: string, message: string) {
  return {
    type: 'chat',
    category: 'authored',
    needs_translation: false,
    source_name: botUsername,
    xuid: '',
    platform_chat_id: '',
    has_filtered_message: false,
    filtered_message: '',
    message,
  }
}
