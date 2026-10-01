# mc-console-bridge

[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

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
- Origin checking is on by default. `mc-server-runner` compares the request's
  `Origin` against `WEBSOCKET_ALLOWED_ORIGINS` by exact string equality, and
  its flag parser drops blank entries from that list — so a client that sends
  no `Origin` at all can never be allow-listed. The bridge therefore always
  sends one (`CONSOLE_ORIGIN`); see
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
| `kick <gamertag>` or `kick "<gamertag>"`, only for a gamertag in `BRIDGE_KICKABLE` | `kick "Afk Bot Two"` |

Commands containing a newline or carriage return are refused outright, so
one HTTP request cannot smuggle a second console line.

`kick` exists so the agent can force one of its own actors (itself or an AFK
bot) off the server; it is never a moderation tool. It takes exactly one
target and no reason. A gamertag with spaces has to be quoted, since unquoted
Bedrock would read everything after the first space as the reason. The name
must equal an entry in `BRIDGE_KICKABLE` apart from ASCII letter case.
Selectors are always refused, and with `BRIDGE_KICKABLE` empty every kick is,
so a leaked bridge token cannot remove a real player.

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
| `CONSOLE_ORIGIN` | no | `mc-console-bridge://sidecar` | `Origin` sent on the console handshake; must appear verbatim in the server's `WEBSOCKET_ALLOWED_ORIGINS` when its origin check is enabled. Must be `scheme://host[:port]` — anything else fails startup |
| `COMMAND_TIMEOUT_MS` | no | `2000` | Bounds the console write for one `/command`, and caps the window spent collecting that command's output. The HTTP response write deadline is derived from it |
| `DATA_DIR` | no | `/data` | Mounted server data volume (read-only). `worlds/` under it is what `/snapshot` reads |
| `SNAPSHOT_MAX_HOLD_MS` | no | `300000` | Longest one `/snapshot` may keep world saving paused, however slow its caller is |
| `BRIDGE_KICKABLE` | no | empty | Comma-separated gamertags `kick` may name (the server's own actors). Empty refuses every kick. An entry containing `"`, `\` or a control character, or starting with `@`, fails startup |

## Server-side prerequisites

The bridge cannot connect at all unless the **server** container — not this
sidecar — runs `mc-server-runner` with `WEBSOCKET_CONSOLE=true`, a
`WEBSOCKET_PASSWORD` matching the bridge's `CONSOLE_PASSWORD`, and either

- `WEBSOCKET_ALLOWED_ORIGINS` containing this bridge's `CONSOLE_ORIGIN`
  verbatim (default `mc-console-bridge://sidecar`) — the preferred setting,
  which keeps the origin check enabled; or
- `WEBSOCKET_DISABLE_ORIGIN_CHECK=true`, which adds no exposure here because
  the console is bound to loopback (`127.0.0.1`) and is unreachable from
  outside the pod.

The default `CONSOLE_ORIGIN` uses a scheme browsers cannot load pages from,
so no website can mint that origin; allow-listing it leaves the check
meaningful against Cross-Site WebSocket Hijacking in a way that
allow-listing, say, `http://localhost` would not.

Those values are owned by the deployment chart
(`jdw-deployments/charts/minecraft-fwb`, `extraEnv`); the list above is only
the contract this bridge depends on, not a second copy of the chart's config.

## Endpoints

| Route | Auth | Purpose |
|---|---|---|
| `POST /command` | bearer | `{"command": "..."}` → allowlist check → console → `{"rule": "...", "output": "..."}` |
| `GET /permissions` | bearer | Parsed `permissions.json` |
| `GET /allowlist` | bearer | Parsed `allowlist.json` |
| `GET /events?since=<id>` | bearer | Typed events (connect/disconnect/crash/content-error), fed from the same console websocket's stdout/stderr/logHistory broadcasts - no separate log-tailing. Events replayed from a logHistory backfill (sent on every connect/redial) carry `"backfill":true`; live stdout/stderr events omit the field, so a consumer can ignore replayed history instead of treating it as a fresh arrival |
| `POST /snapshot` | bearer | `{"have": {"<world>/db/000123.ldb": <size>, ...}}` → a tar of the world as of one consistent save. See [Snapshots](#snapshots) |
| `GET /healthz` | none | Liveness - process is up. Stays green while the console is down, since a restart cannot fix a server that has not opened its console yet |
| `GET /readyz` | none | Readiness - 200 only while the console websocket is established, so a bridge whose console auth is rejected stops receiving traffic |
| `GET /metrics` | none | Prometheus |

## Snapshots

`POST /snapshot` copies the world out of a running server without stopping
it. The bridge pauses saving (`save hold`), waits for the server to report
the save complete and list each file with its length (`save query`), streams
the files cut to exactly those lengths, and resumes saving (`save resume`) on
every path out, including a caller that disconnects or stalls.

The response is a tar stream:

1. `snapshot.json`: `{"files":[{"name","size"}]}`, every file in the save,
   whether or not it is sent.
2. The files the caller lacks. LevelDB `.ldb` tables never change once
   written, so one named in `have` at the same size is skipped; every other
   file is always sent. After the first call a snapshot moves megabytes, not
   the whole world.
3. `snapshot.ok`, empty, last. **A stream without it is not a snapshot** and
   must be discarded: the status line is already sent when a copy fails
   midway, so the marker is the only completion signal.

| Status | Meaning |
|---|---|
| `409` | Another snapshot is running, or the server refused the pause (`The command is already running`) at any point during this one. The bridge then does not resume: something else believes the pause is its own, is still copying under it, and resumes when it is done |
| `502` | Console not connected, or the server's file list could not be trusted |
| `503` | An earlier snapshot's resume has not been confirmed yet, or the bridge is shutting down |
| `504` | The server did not report the save complete in time |

### The resume is owed until the server confirms it

A world left paused keeps every change in memory. So from the moment a
`save hold` may have reached the server, a resume is owed, and it stays owed
until the server answers `Changes to the world are resumed.` or
`A previous save has not been completed.` (what it says when nothing is
paused, for instance after a restart). If the console connection drops first,
the bridge retries every few seconds once it is back, and refuses new
snapshots in the meantime. `mc_console_bridge_snapshot_resume_owed` is 1 for
as long as that lasts; it should never stay 1 for more than a few seconds.

Only whole lines in the server's own log format count as its answers. The
console also carries script output and player names, and a line that merely
contains the words is not the server speaking.

The save commands run on a context of their own, not the request's: a
console write on a cancelled context closes the websocket, and a caller going
away is exactly when the resume has to get through. On shutdown the bridge
ends any snapshot, waits for its resume, and only then closes the console.

The save commands are deliberately not in the `/command` allowlist: a client
that could pause saving without the bridge guaranteeing the resume could
leave the world unsaved until the next restart.

The bridge cannot see a pause taken through another channel, such as
`send-command save hold` from `kubectl exec`. It refuses to start while one
is active and backs off if one starts during a snapshot, but that job's copy
is only safe if it does not overlap a snapshot at all, so callers keep clear
of the times those jobs run.

## Build and test

```sh
go test ./... -race      # unit tests
gofmt -l . && go vet ./...
docker build -t mc-console-bridge .
```

The binary is static (`CGO_ENABLED=0`) and ships on a distroless nonroot
base, so the sidecar image carries no shell. CI runs these same checks
(`.github/workflows/ci.yml` at the repo root).

## Releases

Releases are cut by
[`semantic-release.yml`](../../.github/workflows/semantic-release.yml), not by
hand. After CI passes on a push to `main`, it reads the
[Conventional Commits](https://www.conventionalcommits.org/) since this
component's last tag that touch this component (its directory, a path in
`depends` in its `component.yaml`, or `go.mod`/`go.sum`): `feat` cuts a minor
version; `fix`, `perf`, `build` and `chore(deps)` a patch (so dependency
security fixes ship); `ci`, `docs`, `test` and other `chore` commits cut
nothing. A breaking change cuts a major. When it cuts a version it tags
`bridge-v<version>`, writes the GitHub release, and runs
[`release.yml`](../../.github/workflows/release.yml); pushing a
`bridge-v<version>` tag by hand still runs `release.yml` directly.

Either way, `release.yml` runs this component's tests against that tag and
then publishes a
`linux/amd64` image to GHCR, then mirrors it to Docker Hub:

```
ghcr.io/<owner>/mc-console-bridge:<version>
ghcr.io/<owner>/mc-console-bridge:sha-<commit>
docker.io/<username>/mc-console-bridge:<version>
docker.io/<username>/mc-console-bridge:sha-<commit>
```

The Docker Hub tags are copied registry-to-registry from GHCR, not rebuilt,
so both registries serve identical digests. Deployments (the Helm chart and
the cluster) keep pulling from GHCR; Docker Hub is a redundant copy, and its
mirror job runs after the GHCR publish so a Docker Hub failure never blocks
it. The same job pushes `README.docker.md` as the Docker Hub Overview.

Each image carries OCI labels and index annotations (title, description,
source, license, version, revision) plus provenance (`mode=max`) and SBOM
attestations. Inspect them with:

```sh
docker buildx imagetools inspect ghcr.io/<owner>/mc-console-bridge:<version> --format '{{ json .Provenance }}'
docker buildx imagetools inspect ghcr.io/<owner>/mc-console-bridge:<version> --format '{{ json .SBOM }}'
```

The Docker Hub mirror is skipped until a human does a one-time setup: a
`DOCKERHUB_USERNAME` repository variable (`jdwillmsen`) and a
`DOCKERHUB_TOKEN` secret holding a Docker Hub personal access token with
Read, Write, Delete scope (the Overview update needs Delete). Set the token
from a terminal outside any agent session so it never lands in a transcript:

```sh
gh variable set DOCKERHUB_USERNAME --body jdwillmsen
gh secret set DOCKERHUB_TOKEN   # paste the token at the prompt
```

`latest` is never published. This sidecar has write access to the server
console, so a moving tag would let a later push silently replace what a
running deployment already trusts — deployments (the Helm chart) pin the
version tag instead.

`<version>` is the tag without its leading `v`, and must be a semantic
version; the workflow refuses anything else, so a moving name like `latest`
or a branch name cannot reach either registry.
`.github/workflows/release.yml` can also be dispatched manually with a
version whose `v<version>` tag already exists, which re-publishes from that
tag — never from a branch.

## Status

Stage 0/1 scaffold: allowlist, permissions/allowlist parsing, console
websocket client, and HTTP surface are implemented and tested. `/events` is
wired end-to-end off the console websocket's own broadcasts (no separate
log source), with a bounded in-memory buffer and ID-based paging. The
crash/content-log detection regexes in `events.go` are still best-effort -
they haven't been validated against a real crash or content-log error, only
against the documented connect/disconnect line shapes.
