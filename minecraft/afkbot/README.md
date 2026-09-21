[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

# minecraft-afk-bot

A headless Minecraft Bedrock client that holds a player slot so the chunks
around it keep ticking, and mob farms with them.

Mob farms need a *player* present in the dimension. Ticking areas keep chunks
loaded but never spawn mobs, so something has to stand there. This bot signs in
as a real Microsoft account, connects, asks for a chunk radius, and stays.

That is the whole job. It does not read chat, answer questions, run commands or
persist anything — [`minecraft-server-agent`][agent] does those. The process the
farms depend on is kept with as little in it as possible to go wrong.

[agent]: https://github.com/jdwillmsen/minecraft-server-agent

## Configuration

| Variable | Required | Default | Range | Meaning |
|---|---|---|---|---|
| `MC_HOST` | yes | — | non-empty | Server hostname |
| `MC_USERNAME` | yes | — | non-empty | Key the auth token caches under, **not** the gamertag the server shows |
| `MC_PORT` | no | `19132` | 1–65535 | Server port |
| `MC_VIEW_DISTANCE` | no | `12` | 1–64 | Chunk radius to request. The default is Bedrock's maximum tick-distance; the server grants what is asked for, so a smaller value holds proportionally less world ([why][radius]) |
| `AUTH_CACHE_DIR` | no | `/data/auth` | — | Where the Xbox Live token cache lives |
| `RECONNECT_MIN_MS` | no | `5000` | 1–3600000 | Backoff floor |
| `RECONNECT_MAX_MS` | no | `300000` | 1–3600000, ≥ `RECONNECT_MIN_MS` | Backoff ceiling |
| `LOG_LEVEL` | no | `info` | — | `debug` adds debug lines; anything else is info |

Every value is validated at startup. A missing host or an out-of-range number
fails immediately rather than surfacing later, further from the cause.

[radius]: docs/decisions.md#2026-09-08-chunk-radius-defaults-to-the-maximum

## Running it

The bot needs a Microsoft account that owns Minecraft, separate from any
account a human plays on, and a one-time device-code login by hand. See
[docs/operations.md](docs/operations.md).

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
| `protocol_spoofed` | warn | Announcing the server's protocol number instead of the compiled-in one ([why][drift]) |
| `reconnecting` | info | Backing off before the next attempt |
| `session_ended` | info | Connection closed without an error |
| `session_error` | error | Connection failed or dropped |
| `respawn_failed` | error | The respawn handshake could not be completed |
| `config_invalid` | error | Startup validation failed; the process exits |
| `auth_failed` | error | Device-code login or token refresh failed; the process exits |

A killed player keeps its session: the connection stays open, the server keeps
listing it, and the pod stays Ready while the bot sits on a death screen
holding nothing. The bot completes the respawn handshake itself and re-requests
its chunk radius afterwards, since that is per-session state the server drops.

[drift]: docs/protocol-drift.md

## Development

```bash
go build ./...
go test ./...
docker build -t minecraft-afk-bot:dev .
```

The image is distroless and runs as nonroot, so there is no shell in it —
`kubectl exec` into a running bot will not work. Read the logs instead.

`internal/mcauth`, `internal/liveness`, `internal/logging`, `internal/mcproto`
and `internal/skin` are copies of packages from [`minecraft-server-agent`][agent]'s
`pkg/`, each carrying a header saying so. **Fix bugs upstream first, then port
them here** ([why copied][copies]).

[copies]: docs/decisions.md#shared-packages-are-copied-not-imported

## Documentation

- [docs/operations.md](docs/operations.md) — first run, allowlist, auth cache
- [docs/protocol-drift.md](docs/protocol-drift.md) — staying connectable across Bedrock releases
- [docs/decisions.md](docs/decisions.md) — why the bot is shaped this way
