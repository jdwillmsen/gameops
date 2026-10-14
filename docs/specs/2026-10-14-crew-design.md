# Crew — programmable Bedrock worker units

**Status:** accepted (Wave 0)
**Date:** 2026-10-14
**Plans:** `docs/superpowers/plans/2026-10-14-crew-wave1-worker.md` (Wave 1).
Later waves get their own plan once the wave before them has landed.

## Why

FWB's Bedrock stack has two automated actors, and both must stay as they are:

| Actor | Job | Stays |
| --- | --- | --- |
| `afk-bot-*` | Hold a player slot so farms tick | Minimal: no chat, no LLM, no skills (`minecraft/afkbot/docs/decisions.md`) |
| `agent` | Chat, tools, presence API, optional LLM answers | The interactive brain for players, not a worker |

Missing is a **programmable worker**: a unit that mines, gathers, crafts and
deposits on an operator's instruction, plus a **control room** where operators
watch each unit's first-person view and assign it tasks.

Mindcraft, Mineflayer and the Andy models are Java-only. FWB is Bedrock on
`gophertunnel`, so those projects are reference reading, not dependencies.

## Decisions

| # | Decision | Why |
| --- | --- | --- |
| D1 | New actor kinds `crew` (worker) and `camera` (POV follower). AFK bots are not evolved. | Keeps the process farms depend on minimal. |
| D2 | The runtime is deterministic: typed Go skills, a task planner built from recipe tables, and an executor. No live LLM in any crew process. | Predictable, testable, free to run. Revisit only through a later decision record. |
| D3 | Free or local models are used **offline** only, to help author and review skills. | They are a development aid, not a runtime dependency. |
| D4 | Cameras show a true POV: a full Bedrock client per camera, following its crew unit, captured and streamed to the browser over WebRTC. | Owner wants a "security camera" view, not a reconstructed one. |
| D5 | Control plane is a new `crew-control` service. It owns task queues, telemetry and **presence for crew/camera actors**. | Keeps the chat agent's blast radius small. The agent's presence API is FWB-only, and crew starts on the lab. Settles the brainstorm's open choice 2. |
| D6 | Crew does real work on a **lab world** first: a separate Bedrock server with cheats on, not FWB. | FWB is pure survival (no cheats). Automated gathering there needs the group's agreement first. |
| D7 | Spectator mode and teleport-follow cameras run on the **lab only**. On FWB a camera is a survival player or absent, until a later rule decision. | FWB's no-cheats rule limits the console to read-only and messaging commands. |
| D8 | Home is `minecraft/crew`, `minecraft/crew-control` and later `minecraft/crew-console` in this repo. Tracked in JDW and deployed on the jdwillmsen tenant. | Siblings of afkbot, agent and mcmap. |
| D9 | Crew reuses `minecraft/agent/pkg/*` (auth, dial, protocol negotiation, skin, respawn, logging). | One copy of the connection code. afkbot's copies are `internal/` and cannot be imported. |

## Standing rules every wave inherits

- **No FWB coordinates, seeds or structure positions** in code, tests, docs,
  PR text or Jira. Coordinates are seed-equivalent. Crew never logs a
  position. Positions travel only in the status API's response bodies.
- **No-cheats on FWB.** No `/give`, `/summon`, `/tp`, `/gamemode` or kills on FWB,
  ever. The lab is the only place cheats are on.
- **The homelab GPU is not a camera host by default.** The RTX 5090 is passed
  through to the vLLM VM, and restarting that VM has wedged the card before.
  No camera work may stop, restart or reassign that VM without the owner's
  go-ahead.
- Crew units and cameras use their own Microsoft accounts. They never share
  an AFK bot's or the agent's.

## Architecture (target)

```
Crew Console (web) ── HTTPS/WS ──► crew-control ──► crew-N  (gophertunnel, skills)
      │                              │  tasks, telemetry,       │
      │ WebRTC                       │  crew/camera presence    │ RakNet
      ▼                              ▼                          ▼
 stream gateway (MediaMTX) ◄── camera-N (full Bedrock client) ──► lab Bedrock server
```

- **crew-N**: headless Go client. It builds a picture of the world from
  packets (position, health, inventory, nearby blocks), runs skills, and
  serves its state over a local HTTP status API.
- **camera-N**: a full Bedrock client in spectator mode on the lab,
  following crew-N. Its display is captured with ffmpeg and published to
  MediaMTX.
- **crew-control**: task CRUD (`POST /v1/crew/{id}/tasks`, cancel, list),
  telemetry fan-out (SSE), and parking for crew/camera actors. It uses the
  same `internal/presenceapi` contract shape as the agent's API.
- **Crew Console**: a camera wall, a task board, unit detail, and a deep
  link into mcmap at the unit's position (lab map only).

## Waves

Each wave ends in something an operator can watch work. A wave's detailed
plan is written when the wave starts, from what the wave before it learned.

| Wave | Delivers | Done when | Plan |
| --- | --- | --- | --- |
| 1 | Lab world + empty `crew` worker with a status API | crew-1 joins the lab, reports state, survives a server restart and a death | written |
| 2 | POV spike: one camera following one player on the lab, in a browser | ≥10 min of a stable stream, unattended | spike brief below; plan after |
| 3 | Skill runtime + `crew-control` MVP | operator assigns "mine 16 cobblestone → deposit in chest", it completes without an LLM | after W1 |
| 4 | Crew Console MVP | one operator runs a W3 task while watching the POV, from the UI alone | after W2 + W3 |
| 5 | Scale + polish: crew-2/camera-2, more skills, failure taxonomy, optional offline skill-authoring CLI | two units work side by side for an hour | after W4 |
| Gate | FWB rollout | group agrees crew may work on FWB, and on whether cameras appear there | owner decision |

Waves 1 and 2 are independent and can run in parallel. Wave 2 only needs
the lab world from Wave 1's Task 1.

### Wave 2 spike brief (POV camera)

Timebox: 3 working sessions. The output is a findings doc plus one running
stream. Production-quality code is not expected.

Packagings to try, in this order:

1. **Linux + `mcpelauncher`** (unofficial launcher for the Android Bedrock
   build) under Xvfb with Mesa software rendering. It is lightest on hardware
   and fits a container. Requires a game licence on the camera account.
2. **Windows VM** running the official Bedrock client, using WARP software
   rendering or a non-passthrough GPU.
3. **Wine/Proton** with the Windows client. Listed in the brainstorm, but the
   GDK build is the least likely to run.

For each: does it join the lab's pinned server version? Does `/gamemode spectator`
plus a periodic `/tp @s <crew>` (lab only) hold the camera behind its unit?
What frame rate does capture reach (target 15 fps at 720p)? What CPU and RAM
does it cost? Stream with `ffmpeg` → MediaMTX (WHIP/RTSP in, WebRTC out) and
play in a bare HTML page. Record the findings in
`docs/specs/<date>-crew-camera-spike.md`. Do not include any FWB data.

### Wave 3 outline (for its plan)

- `minecraft/crew/internal/belief`: inventory and nearby blocks are added to
  Wave 1's position and health.
- `internal/skill`: `type Skill interface { Name() string; Ready(belief.Snapshot) error; Step(ctx, Actuator) (Status, error) }`.
  First skills: `Navigate(waypoint)`, `MineBlock(type, qty)`, `DepositChest(waypoint)`.
- Tasks are JSON templates in v1 (brainstorm open choice 1, settled as JSON).
  The planner expands a template into a skill sequence from a recipe table.
- `minecraft/crew-control`: a task queue (Postgres, same operator as the
  agent) and SSE telemetry. Crew polls it for work, the same way afkbot polls
  presence.
- Tests replay recorded packet fixtures captured on the lab, never on FWB.

## Non-goals (v1)

Making AFK bots smarter. Any live LLM in crew. Running Java bots against
FWB. Open-world survival without an assigned task. Passing as human.

## Risks

| Risk | Mitigation |
| --- | --- |
| Camera clients are heavy | W2 measures cost before W4 depends on it; start at 1 camera |
| Protocol drift between Mojang releases | The lab pins the same server image tag as FWB, and crew reuses `mcproto.Negotiate` |
| A camera needs a GPU and the only GPU belongs to vLLM | Software rendering first. GPU sharing is an owner decision |
| Operators expect "AI" | Console copy says "programmable units" |
