# minecraft-afk-bot

A headless Minecraft Bedrock client. It holds a player slot on the FWB server
and mirrors in-game chat to stdout as structured JSON.

## Why it exists

Two jobs, one process:

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

Sending messages *into* the server does not need this bot — the server image
ships `send-command`, so `say` and `tellraw` already work from outside.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `MC_HOST` | yes | — | Server hostname |
| `MC_USERNAME` | yes | — | Key the auth token caches under, not the gamertag the server shows |
| `MC_PORT` | no | `19132` | Server port |
| `MC_VERSION` | no | baked at build | Protocol version to negotiate |
| `MC_VIEW_DISTANCE` | no | `4` | Chunks the server sends this client |
| `AUTH_CACHE_DIR` | no | `/data/auth` | Where the Xbox Live token cache lives |
| `RECONNECT_MIN_MS` | no | `5000` | Backoff floor |
| `RECONNECT_MAX_MS` | no | `300000` | Backoff ceiling |

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
`spawned`, `disconnected`, `kicked`, `reconnecting`, or `device_code_required`.
Errors go to stderr.

## Development

```bash
npm ci --ignore-scripts   # skips the native RakNet build; enough to typecheck
npm run typecheck
docker build -t minecraft-afk-bot:dev .
```

A full `npm ci` compiles a C++ addon and needs `cmake`, `make`, and a C++
compiler on the host. The image build carries its own toolchain, so building
the image works regardless.
