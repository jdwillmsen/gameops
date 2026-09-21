# Decisions

Why the bot is shaped the way it is. Dated, because the reasoning was true
against a particular state of the world and can stop being true.

## 2026-09-08: rewritten from TypeScript in Go

The Node implementation used roughly eight times the CPU and memory of the Go
agent while doing considerably less: 48–56m CPU and 92–97Mi, against 6m and
12Mi.

The structural reason mattered more. Respawn-on-death, device-code auth,
structured logging and a generated skin already existed in
[`minecraft-server-agent`][agent]'s `pkg/`, and writing them a second time in
another language guarantees the second copy rots.

## 2026-09-08: presence only, chat moved to the agent

The bot answered chat, called an LLM, and carried `MC_ANSWER_*` and `MC_LLM_*`
configuration. All of it moved to the agent, and was dropped here rather than
carried across dead.

The process mob farms depend on should have as little in it as possible to go
wrong. Two programs that both connect, both answer, and both hold a slot also
overlap in ways nobody tracks.

## 2026-09-08: chunk radius defaults to the maximum

`MC_VIEW_DISTANCE` defaulted to `4`, on the reasoning that the server's
`tick-distance` governs what simulates around a player, making a low request
harmless. Measured against the live server, that is wrong — the server grants
what the client asks for:

| Requested | Granted |
|---|---|
| 4 | 5 |
| 12 | 12 |

A bot asking for 4 holds five chunks, and nothing beyond them ticks: the farm
it exists to keep running was covered about a fifth of the way.

The failure is invisible from outside. The farm still produces, just less, and
no metric or log line separates "working" from "working over a fifth of the
area" — which is why the default is the maximum rather than a value someone
has to reason about.

12 is Bedrock's maximum tick-distance and the largest radius measured as
granted in full. Nothing above it was tried, so it is a verified ceiling
rather than a known limit.

## 2026-09-17: numeric settings are bounded, and view distance is held at the protocol's width

`MC_VIEW_DISTANCE=256` once passed a `> 0` check, was stored as an `int32`,
and reached `uint8(cfg.ViewDistance)` as `0` — a bot connected, healthy, and
loading nothing.

`Config.ViewDistance` is now a `uint8`, the width
`RequestChunkRadius.MaxChunkRadius` uses, and it is parsed at that width. The
value that wrapped cannot be represented, rather than being guarded from a
distance.

Every other numeric variable gained a ceiling for the same reason: each is
narrowed or range-bound somewhere that cannot report a problem. A bound that
holds only for the default is what let this through.

## Shared packages are copied, not imported

`internal/mcauth`, `internal/liveness`, `internal/logging`, `internal/mcproto`
and `internal/skin` are copies of the agent's `pkg/` packages, each carrying a
header saying so. **Fix bugs upstream first, then port them here.**

Originally the agent's module was private, so importing it would have put a
credential in this bot's Docker build. It went public on 2026-09-16, and the
decision was revisited on 2026-09-21 package by package rather than as a
block. All five stay copies, for different reasons.

### `mcauth` is a fork, not a copy

The copy here is the agent's `mcauth` as exported, identical in code. Every
difference since comes from one upstream change: moving the token cache
behind a `Store` so a warm standby can read it from Postgres, with a gate
that stops the standby rotating the refresh token the live agent holds.

None of that applies here. The bot deploys with `Recreate` onto a
ReadWriteOnce volume, so there is never a second process holding the same
account and nothing to gate. Importing upstream `mcauth` would bring that
machinery in unused and change the one call the bot makes to it.

So `mcauth` is maintained here in its own right. Upstream `mcauth` changes
are not ported by default; a fix to the token handling both programs share
still is, adapted to this version.

### The other four are copies of finished code

`liveness`, `logging`, `mcproto` and `skin` match upstream in everything but
their headers, and upstream has not changed them since 2026-09-08, when its
last change to them was ported here. Importing them is possible, and was
measured against a pinned `v0.18.0`:

- Minimum version selection takes the agent's dependency floors as the bot's.
  `go mod tidy` raised 20 of the module versions the bot builds with,
  `pion/webrtc` among them, in a change whose subject was four import paths.
  After that, an agent release that bumps a shared dependency moves the
  bot's too.
- The agent tags a release for its own changes, 21 of them in its first 16
  days, of which three touched `pkg/` at all. Renovate would propose every
  one here, and each is a review of an agent diff to find nothing.
- `pkg/` is `v0` and its API is not stable: the `Store` change above altered
  `mcauth.TokenSource`'s signature in a minor release.

Build cost is not a reason either way. No vendoring is needed now that the
module is public, and the image came out 1.5KB smaller with the import. Cold
build times were set by load on the build host rather than by the change:
the unchanged tree alone ranged from 136s to 209s.

Keeping four unchanged packages in step is cheaper than any of that.

### When upstream moves

For `liveness`, `logging`, `mcproto` and `skin`, "finished" means their code
matches upstream apart from the header. When upstream changes one, port the
change here in the same shape, or record here why this copy stays behind.
If they start changing often enough that porting is the bigger cost, that is
the signal to revisit the import.

[agent]: https://github.com/jdwillmsen/minecraft-server-agent
