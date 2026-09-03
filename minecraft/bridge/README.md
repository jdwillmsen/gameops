# mc-console-bridge

A sidecar for the FWB Bedrock server pod that exposes the server console over
HTTP, behind a fixed command allowlist. It is the only component with write
access to the console — nothing upstream of it (including any LLM) can run
an arbitrary command.

## Why this exists

Mojang's Bedrock dedicated server has no plugin API, no RCON, and no admin
API. The only way to write to the running server is its console stdin. This
bridge speaks the console's real transport (`mc-server-runner`'s
`WEBSOCKET_CONSOLE`) and turns it into a small, auditable HTTP surface for
`minecraft-server-agent`.

## Console protocol (as verified against a live server)

`mc-server-runner`'s websocket console is **not** a raw command socket:

- Endpoint is `/console`, not the bind address's root path.
- Auth is via the `Sec-WebSocket-Protocol` header:
  `mc-server-runner-ws-v1, <password>` — not a query param, not
  `Authorization`.
- Messages are JSON: send `{"type":"stdin","data":"<command>\n"}`; the server
  broadcasts `{"type":"stdout"|"stderr","data":"..."}` for console output and
  `{"type":"logHistory","lines":[...]}` once on connect.
- **Origin checking is on by default and rejects every dial** unless the
  server container turns it off — see
  [Server-side prerequisites](#server-side-prerequisites).
- The protocol carries no request/response correlation id. The bridge
  serializes its own commands, so two concurrent `POST /command` calls cannot
  collect each other's output — but everything the console broadcasts during
  the collection window is returned, so unrelated server-side output can
  still appear. This is a protocol limitation, not fixable client-side.
- Observed: `say` produced no console output at all with zero players
  online (reproduced twice); `list` round-tripped correctly. Flagging as a
  known quirk — announcements to an empty server may be silent.

## Allowlist

`POST /command` accepts only commands matching one of these templates
(`allowlist.go`); anything else is refused with `403` and never reaches the
console:

| Template | Example |
|---|---|
| `list` | `list` |
| `say <text>` | `say hello` |
| `title <target> <title\|subtitle\|actionbar\|clear\|reset> [...]` | `title @a actionbar Hi` |
| `tellraw <target> <json with "rawtext">` | `tellraw @a {"rawtext":[{"text":"hi"}]}` |
| `time query <day\|daytime\|gametime>` | `time query day` |
| `gamerule <name>` (no value — read-only query) | `gamerule doDaylightCycle` |

Commands containing a newline or carriage return are refused outright, so
one HTTP request cannot smuggle a second console line.

## Permissions

`GET /permissions` parses `<DATA_DIR>/permissions.json` into
`{xuid: "operator"|"member"|"visitor"}`. An XUID **absent** from that file is
not "visitor" — it falls back to the server's
`default-player-permission-level` (in `server.properties`, not read by this
service). Callers must treat a missing XUID as unknown, not as a specific
level.

`GET /allowlist` parses `<DATA_DIR>/allowlist.json`.

Either file being absent is normal on a fresh volume — the server writes them
lazily — so both endpoints answer `404` in that case. `500` is reserved for a
genuine fault: an unreadable mount or unparseable contents.

## Environment variables

| Var | Required | Default | Meaning |
|---|---|---|---|
| `BRIDGE_TOKEN` | yes | — | Bearer token required on every `/command`, `/permissions`, `/allowlist`, `/events` call |
| `CONSOLE_PASSWORD` | yes | — | Must match the server's `WEBSOCKET_PASSWORD` |
| `HTTP_ADDR` | no | `:8080` | Bridge's own HTTP bind address |
| `CONSOLE_ADDR` | no | `127.0.0.1:8765` | Server's websocket console address |
| `COMMAND_TIMEOUT_MS` | no | `2000` | Bounds the console write for one `/command`, and caps the window spent collecting that command's output. The HTTP response write deadline is derived from it |
| `DATA_DIR` | no | `/data` | Mounted server data volume (read-only) |

## Server-side prerequisites

The bridge cannot connect at all unless the **server** container — not this
sidecar — runs `mc-server-runner` with `WEBSOCKET_CONSOLE=true`, a
`WEBSOCKET_PASSWORD` matching the bridge's `CONSOLE_PASSWORD`, and
`WEBSOCKET_DISABLE_ORIGIN_CHECK=true` (or `WEBSOCKET_ALLOWED_ORIGINS` covering
the Origin this bridge sends, which by default is none). Disabling the origin
check adds no exposure here: the console is bound to loopback (`127.0.0.1`)
and is unreachable from outside the pod.

Those values are owned by the deployment chart
(`jdw-deployments/charts/minecraft-fwb`, `extraEnv`); the list above is only
the contract this bridge depends on, not a second copy of the chart's config.

## Endpoints

| Route | Auth | Purpose |
|---|---|---|
| `POST /command` | bearer | `{"command": "..."}` → allowlist check → console → `{"rule": "...", "output": "..."}` |
| `GET /permissions` | bearer | Parsed `permissions.json` |
| `GET /allowlist` | bearer | Parsed `allowlist.json` |
| `GET /events?since=<id>` | bearer | Typed events (connect/disconnect/crash/content-error), fed from the same console websocket's stdout/stderr/logHistory broadcasts - no separate log-tailing |
| `GET /healthz` | none | Liveness - process is up. Stays green while the console is down, since a restart cannot fix a server that has not opened its console yet |
| `GET /readyz` | none | Readiness - 200 only while the console websocket is established, so a bridge whose console auth is rejected stops receiving traffic |
| `GET /metrics` | none | Prometheus |

## Build and test

```sh
go test ./... -race      # unit tests
gofmt -l . && go vet ./...
docker build -t mc-console-bridge .
```

The binary is static (`CGO_ENABLED=0`) and ships on a distroless nonroot
base, so the sidecar image carries no shell. CI runs these same checks
(`.github/workflows/ci.yml`).

## Status

Stage 0/1 scaffold: allowlist, permissions/allowlist parsing, console
websocket client, and HTTP surface are implemented and tested. `/events` is
wired end-to-end off the console websocket's own broadcasts (no separate
log source), with a bounded in-memory buffer and ID-based paging. The
crash/content-log detection regexes in `events.go` are still best-effort -
they haven't been validated against a real crash or content-log error, only
against the documented connect/disconnect line shapes.
