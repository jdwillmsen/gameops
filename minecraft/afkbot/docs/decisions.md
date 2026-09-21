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
credential in this bot's Docker build. It is public as of 2026-09-16 and the
import is now possible, but the copies stay for the reasons that outlived that
one:

- A shared module couples the workload the farms depend on to the agent's
  release cadence. The bot should not need a new agent release to ship, or
  inherit a regression from one.
- Vendoring costs 2177 files and 24MB to share 514 lines, and turns every
  dependency bump into a diff nobody reads.

Five small, finished packages are the cheaper duplication. Revisit if they
stop being finished.

[agent]: https://github.com/jdwillmsen/minecraft-server-agent
