# minecraft-afk-bot

A headless Minecraft Bedrock client. It holds a player slot on the FWB server,
mirrors in-game chat to stdout as structured JSON, and answers chat questions
via any OpenAI-compatible LLM backend.

## Why it exists

Three jobs, one process:

**Keeping a player in the world.** Mob farms need a player present in the
dimension — ticking areas keep chunks loaded but never spawn mobs. This bot is
that player.

**Reading chat.** The Bedrock dedicated server never writes player chat to its
console or to a log file, and no `server.properties` setting changes that.
`content-log-console-output-enabled` covers pack and content *errors*, not
chat. The only way to observe chat without modifying the world is to be a
connected client, which is what this is. Each chat message becomes one JSON
line on stdout, so the cluster's existing log pipeline makes it searchable
with no extra plumbing.

**Answering questions.** Any chat line containing `?` — from a player, or from
the server console itself via `send-command say` — other than the bot's own
broadcasts or a listed sibling bot, is sent to a configured LLM backend, and
the answer is broadcast back as chat under the bot's own name (`src/answer.ts`,
`src/reply.ts`, `src/throttle.ts`). Off by default (`MC_ANSWER_ENABLED`); a
failed, slow, or rate-limited answer is logged and otherwise ignored — it
never affects the connection.

Answering is guarded against runaway cost/GPU load even though the default
backend is free: a hard per-request timeout, a cap of one in-flight LLM call
at a time (a burst of questions drops the extras rather than queueing them),
a per-asker cooldown, and a global per-minute cap. See `src/throttle.ts`.

Sending messages into the server for anything else does not need this bot —
the server image ships `send-command`, so `say` and `tellraw` already work
from outside.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `MC_HOST` | yes | — | Server hostname |
| `MC_USERNAME` | yes | — | Key the auth token caches under, not the gamertag the server shows |
| `MC_PORT` | no | `19132` | Server port |
| `MC_VERSION` | no | baked at build | Protocol version to negotiate |
| `MC_PROTOCOL_SPOOF` | no | `true` | Announce the server's advertised protocol number when it outruns the baked data (see src/protocol.ts) |
| `MC_VIEW_DISTANCE` | no | `4` | Chunks the server sends this client |
| `AUTH_CACHE_DIR` | no | `/data/auth` | Where the Xbox Live token cache lives |
| `RECONNECT_MIN_MS` | no | `5000` | Backoff floor |
| `RECONNECT_MAX_MS` | no | `300000` | Backoff ceiling |
| `MC_ANSWER_ENABLED` | no | `false` | Answer chat questions. Enable on exactly one bot per server — two answering bots answer each other forever |
| `MC_LLM_BASE_URL` | no | `http://192.168.1.50:8000/v1` | OpenAI-compatible `/chat/completions` base URL. Defaults to the cluster's local vLLM instance — unmetered, no auth needed |
| `MC_LLM_MODEL` | no | `qwen/qwen3-coder-30b-a3b` | Model name to request |
| `MC_LLM_API_KEY` | no | — | Bearer token, if the backend needs one (the local vLLM default does not) |
| `MC_LLM_TIMEOUT_MS` | no | `8000` | Abort an LLM call after this long |
| `MC_LLM_MAX_TOKENS` | no | `96` | Output token cap per answer |
| `MC_ANSWER_COOLDOWN_MS` | no | `30000` | Minimum gap between two answers to the same asker (server-console messages share one cooldown bucket, separate from any player's) |
| `MC_ANSWER_MAX_PER_MINUTE` | no | `6` | Global answer-rate cap, regardless of asker |
| `MC_LLM_MAX_IN_FLIGHT` | no | `1` | Concurrent LLM calls allowed; a question arriving while at the cap is dropped, not queued |
| `MC_BOT_NAMES` | no | — | Comma-separated Xbox Live gamertags of sibling bots on the same server, so this bot never answers one into a loop. Case-insensitive; this bot's own resolved gamertag is always added automatically |

### Trying a different LLM backend

`MC_LLM_BASE_URL`/`MC_LLM_MODEL`/`MC_LLM_API_KEY` work with any OpenAI-compatible
`/chat/completions` endpoint — verify the exact model ID against the
provider's current docs before setting it:

| Provider | `MC_LLM_BASE_URL` | `MC_LLM_API_KEY` |
|---|---|---|
| Local vLLM (default) | `http://192.168.1.50:8000/v1` | not needed |
| Groq free tier | `https://api.groq.com/openai/v1` | Groq API key |
| Google AI Studio (Gemini) | `https://generativelanguage.googleapis.com/v1beta/openai/` | AI Studio API key |
| `platform-litellm` gateway | `http://platform-litellm.ai-sre.svc.cluster.local:4000/v1` | a scoped LiteLLM virtual key — never the master key, and never `sre-investigator`, whose free quota is shared with AI-SRE tooling |

`MC_VERSION` is effectively build-time. The image strips the `minecraft-data`
directories for every other version, so overriding it at runtime will fail to
load. Rebuild with `--build-arg MC_VERSION=...` instead.

The version to set is the one whose protocol number the server reports, which
is not the same string as the server's version. Ask the server directly:

```bash
node -e "require('bedrock-protocol').ping({host:'<host>',port:<port>}).then(r=>console.log(r.version, r.protocol))"
```

Bedrock 1.26.43 answers `2168`, which is the protocol `minecraft-data` files
under `1.26.40` — hence the default.

## First run

The bot signs in as a real Microsoft account, so it needs one that owns
Minecraft, separate from any account a human plays on. The first start has to
be completed by hand:

1. Start it and watch the logs for a `device_code_required` event carrying a
   `user_code` and `verification_uri`.
2. Open that URL, enter the code, sign in as the bot's account.
3. The token caches under `AUTH_CACHE_DIR`. Keep that on a volume — losing it
   means doing this again.

Then add the bot to the server's allowlist. Its XUID appears in the server log
when it first connects (`Player connected: <name>, xuid: <xuid>`).

The bot is an ordinary player once connected: it occupies a slot, shows up in
`/list`, and triggers join and leave broadcasts.

## Output

```json
{"ts":"2026-08-19T02:14:03.120Z","level":"info","event":"chat","chat_type":"chat","player":"Dotablaze","xuid":"2535473803383948","message":"hello"}
```

Lifecycle events use the same shape with `event` set to `starting`, `joined`,
`spawned`, `disconnected`, `kicked`, `reconnecting`, `device_code_required`,
`replied` (an answer was broadcast), `answer_skipped` (cooldown/rate-limit/
in-flight cap — see `reason`), or `answer_error` (LLM call or send failed).
Errors go to stderr.

The `chat` event is logged for every `text` packet type the server sends, not
just the ones the bot can answer — useful for confirming what a console
`send-command say`/`tellraw` message actually looks like on the wire (`type`,
`category`, `source_name`) before deciding whether to widen `ANSWERABLE_TYPES`
in `src/reply.ts` to cover it.

## Development

```bash
npm ci --ignore-scripts   # skips the native RakNet build; enough to typecheck
npm run typecheck
docker build -t minecraft-afk-bot:dev .
```

A full `npm ci` compiles a C++ addon and needs `cmake`, `make`, and a C++
compiler on the host. The image build carries its own toolchain, so building
the image works regardless.
