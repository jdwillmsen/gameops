# minecraft-afk-bot

[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

A headless Minecraft Bedrock client that holds a player slot on the FWB server
so mob farms keep running.

## Why it exists

Mob farms need a *player* present in the dimension. Ticking areas keep chunks
loaded but never spawn mobs, so something has to stand there. This bot is that
something: it signs in as a real Microsoft account, connects, asks for a chunk
radius, and stays.

That is the whole job. It does not read chat, answer questions, run commands
or persist anything — [`minecraft-server-agent`][agent] does those. The two
were deliberately separated so that the process the farms depend on has as
little in it as possible to go wrong.

[agent]: https://github.com/jdwillmsen/minecraft-server-agent

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `MC_HOST` | yes | — | Server hostname |
| `MC_USERNAME` | yes | — | Key the auth token caches under, **not** the gamertag the server shows |
| `MC_PORT` | no | `19132` | Server port |
| `MC_VIEW_DISTANCE` | no | `12` | Chunk radius to request |
| `AUTH_CACHE_DIR` | no | `/data/auth` | Where the Xbox Live token cache lives |
| `RECONNECT_MIN_MS` | no | `5000` | Backoff floor |
| `RECONNECT_MAX_MS` | no | `300000` | Backoff ceiling |
| `LOG_LEVEL` | no | `info` | `debug` adds debug lines; anything else is info |

Every value is validated at startup. A missing host or a non-positive number
fails immediately rather than surfacing later, further from the cause.

### Chunk radius

`MC_VIEW_DISTANCE` decides how much world this bot actually holds, so it is
the one setting worth understanding.

It defaulted to `4` on the reasoning that the server's `tick-distance` governs
what simulates around a player, making a low request harmless. Measured
against the live server, that is wrong — the server grants what the client
asks for:

| Requested | Granted |
|---|---|
| 4 | 5 |
| 12 | 12 |

A bot asking for 4 holds five chunks, and nothing beyond them ticks. The
default is now 12, Bedrock's maximum tick-distance, so the farm the bot exists
to keep running is covered.

This failure is invisible from outside: the farm still produces, just less, and
no metric or log line separates "working" from "working over a fifth of the
area". That is the reason for the maximum rather than a value someone has to
reason about.

## Protocol drift

The server runs Mojang's `LATEST` and upgrades itself on restart, so the
protocol number it speaks moves without anyone deciding it should. Bedrock
point releases regularly bump that number without changing a single packet —
and the server still hard-kicks any client announcing the older one
(`play_status: failed_client`, before login even starts).

That has happened: 1.26.45 shipped as protocol 2169 over unchanged packets and
took every bot offline at once.

So the bot pings the server before each session and announces whatever number
it advertises, while still speaking the packet schema it was built against
(`internal/mcproto`). A spoof is logged as `protocol_spoofed`. On a release
that genuinely changes the schema this trades a clean pre-login kick for a
parse failure mid-join — the same reconnect loop either way, with the cause
named in the log rather than inferred.

A ping failure is not fatal: the bot falls back to its compiled-in number,
which is what it would have used anyway.

`cmd/protocolcheck` answers the related question out of band — whether the
version this binary speaks still matches production. The `jdw-deployments`
version-check CronJob dispatches it hourly, since it is the only thing with a
network path to the LAN-only server.

## First run

The bot signs in as a real Microsoft account, so it needs one that owns
Minecraft, separate from any account a human plays on. The first start has to
be completed by hand:

1. Start it and watch the logs for a line reading
   `Authenticate at https://www.microsoft.com/link using the code XXXXXXXX.`
2. Open that URL and enter the code — **in a private window, or signed out of
   any account you do not want to authenticate instead.** The page
   authenticates whatever Microsoft account the browser already has active; it
   does not prompt you to choose.
3. The token caches under `AUTH_CACHE_DIR`. Keep that on a volume — losing it
   means doing this again.

Then add the bot to the server's allowlist. Its XUID appears in the server log
when it first connects (`Player connected: <name>, xuid: <xuid>`).

The bot is an ordinary player once connected: it occupies a slot, shows up in
`/list`, and triggers join and leave broadcasts.

Each `MC_USERNAME` gets its own cache file, so several accounts can share one
`AUTH_CACHE_DIR` volume. The cache format is specific to this implementation —
a bot migrating here from the TypeScript version cannot reuse the old cache
and has to sign in again.

## Output

One JSON line per event on stdout, errors on stderr:

```json
{"event":"chunk_radius_granted","level":"info","radius":12,"timestamp":"2026-09-08T05:22:58.584243833Z"}
```

| Event | Level | Meaning |
|---|---|---|
| `spawned` | info | Connected and placed in the world |
| `chunk_radius_granted` | info | The server's answer to the radius request — the only evidence the bot loads what it asked for |
| `died` | info | Death seen; a respawn was requested |
| `respawned` | info | Back in the world, radius re-requested |
| `protocol_spoofed` | warn | Announcing the server's protocol number instead of the compiled-in one |
| `reconnecting` | info | Backing off before the next attempt |
| `session_ended` | info | Connection closed without an error |
| `session_error` | error | Connection failed or dropped |
| `respawn_failed` | error | The respawn handshake could not be completed |
| `config_invalid` | error | Startup validation failed; the process exits |
| `auth_failed` | error | Device-code login or token refresh failed; the process exits |

Death matters more than it looks. A killed player keeps its session: the
connection stays open, the server keeps listing it, and the pod stays Ready
while the bot sits on a death screen holding nothing. The bot completes the
respawn handshake itself and re-requests its chunk radius afterwards, since
that is per-session state the server drops.

## Shared code

`internal/mcauth`, `internal/liveness`, `internal/logging`, `internal/mcproto`
and `internal/skin` are copies of packages from [`minecraft-server-agent`][agent]'s
`pkg/`, each carrying a header saying so.

Copied rather than imported because that module is private: importing it would
put a credential in this bot's Docker build. Vendoring costs 2177 files and
24MB to share around 500 lines and turns every dependency bump into a diff
nobody reads.

**Fix bugs upstream first, then port them here.**

## Development

```bash
go build ./...
go test ./...
docker build -t minecraft-afk-bot:dev .
```

The image is distroless and runs as nonroot, so there is no shell in it —
`kubectl exec` into a running bot will not work. Read the logs instead.
