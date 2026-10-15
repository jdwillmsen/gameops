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
| D4 | Every unit gets a **rendered camera**: crew forwards the chunks, entities and its own look direction it already receives, and the browser draws them as a first-person 3D view. True-pixel POV is a separate spike (a real Bedrock client fed crew's packet stream through a mirror proxy). | The rendered view needs no extra client, account or command, so it works on FWB under the no-cheats rule. Real-pixel cameras are heavy and have no Bedrock prior art. |
| D5 | Control plane is a new `crew-control` service. It owns task queues, telemetry and **presence for crew/camera actors**. | Keeps the chat agent's blast radius small. The agent's presence API is FWB-only, and crew starts on the lab. Settles the brainstorm's open choice 2. |
| D6 | Crew does real work on a **lab world** first: a separate Bedrock server with cheats on, not FWB. | FWB is pure survival (no cheats). Automated gathering there needs the group's agreement first. |
| D7 | No camera ever uses spectator mode or teleport on FWB. A spectator account following its unit is a lab-only fallback if the mirror proxy fails. | Bedrock spectator mode needs cheats, and FWB's no-cheats rule limits the console to read-only and messaging commands. |
| D8 | Home is `minecraft/crew`, `minecraft/crew-control` and later `minecraft/crew-console` in this repo. Tracked in JDW. Everything runs on the cluster: a `crew-lab` chart in `jdw-deployments` deployed by ArgoCD to `jdwillmsen-prd`, beside FWB. Docker Compose is for local development only. | Siblings of afkbot, agent and mcmap; same GitOps path as everything else. |
| D9 | Crew reuses `minecraft/agent/pkg/*` (auth, dial, protocol negotiation, skin, respawn, logging). | One copy of the connection code. afkbot's copies are `internal/` and cannot be imported. |

## Standing rules every wave inherits

- **No FWB coordinates, seeds or structure positions** in code, tests, docs,
  PR text or Jira. Coordinates are seed-equivalent. Crew never logs a
  position. Positions travel only over crew's own HTTP surface: the
  `/v1/state` response bodies, and from Wave 2a the camera websocket that
  carries chunks and the unit's look. Both stay in-cluster until
  crew-control puts authentication in front of them (Wave 3), and neither
  serves FWB data before the FWB rollout gate.
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
   │  3D camera tiles                │  tasks, telemetry,       │  RakNet
   │◄── WS: chunks, entities, look ──┼──────────────────────────┤
   │                                 │  crew/camera presence    ▼
   │ WebRTC (spike only)                                lab Bedrock server (k8s)
   ◄── MediaMTX ◄── real Bedrock client ◄── mirror proxy ◄── crew-N's packet stream
```

- **crew-N**: headless Go client. It builds a picture of the world from
  packets (position, health, inventory, nearby blocks), runs skills, and
  serves its state over a local HTTP status API.
- **Rendered camera**: crew streams the world data it already holds
  (chunks, nearby entities, its own position and look direction) over a
  websocket. The console draws it in 3D with block textures, the way
  prismarine-viewer does for Java bots. It costs no extra client, account
  or server slot.
- **Mirror camera (spike)**: a real Bedrock client connects to a small
  gophertunnel proxy that replays crew-N's server-to-client packets and
  drops the client's own input. The server only ever sees crew-N. The
  display is captured with ffmpeg and published to MediaMTX.
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
| 2a | Rendered camera spike: crew-1's view drawn in a browser from its own data | a recognisable first-person view of crew-1, updating live, for ≥10 min | spike brief below |
| 2b | Mirror camera spike: a real client rendering crew-1's packet stream | ≥10 min of a stable stream, unattended, or a written reason it cannot work | spike brief below |
| 3 | Skill runtime + `crew-control` MVP | operator assigns "mine 16 cobblestone → deposit in chest", it completes without an LLM | after W1 |
| 4 | Crew Console MVP | one operator runs a W3 task while watching the POV, from the UI alone | after W2 + W3 |
| 5 | Scale + polish: crew-2/camera-2, more skills, failure taxonomy, optional offline skill-authoring CLI | two units work side by side for an hour | after W4 |
| Gate | FWB rollout | group agrees crew may work on FWB, and on whether cameras appear there | owner decision |

Wave 2a needs Wave 1's crew command (Task 4). Wave 2b needs only the lab
world (Task 1), so it can run beside Wave 1.

### Wave 2a spike brief (rendered camera)

Timebox: 2 working sessions. Crew exposes a websocket on its status API
that streams the chunks and entities it receives plus its own position and
look direction. A bare HTML page draws them with three.js and Bedrock block
textures. Start from prismarine-viewer's renderer (Java, MIT) and the
experimental `bedrock-viewer` fork. Reuse mcmap's Bedrock chunk decoding
where it fits. Answer: does the view read as crew-1's first-person view?
What is crew's extra CPU and bandwidth per viewer? Record findings in
`docs/specs/<date>-crew-rendered-camera-spike.md`, with lab data only.

### Wave 2b spike brief (mirror camera)

Timebox: 3 working sessions. Build a gophertunnel proxy that a real
Bedrock client joins. It replays crew-1's server-to-client packets and
drops the client's input. Answer: does the client accept the stream and
hold crew-1's position, or does its own movement prediction fight it?
Which client packaging runs it: `mcpelauncher` (Linux, Android build)
under Xvfb with software rendering first, then a Windows VM with WARP?
What frame rate does capture reach (target 15 fps at 720p), and at what
CPU/RAM? Stream with ffmpeg → MediaMTX → WebRTC. If the proxy cannot
work, record why; the lab-only spectator camera is the fallback. Findings
go in `docs/specs/<date>-crew-mirror-camera-spike.md`.

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
| A real-pixel camera needs a GPU and the only GPU belongs to vLLM | The rendered camera needs none; the mirror spike uses software rendering. GPU sharing is an owner decision |
| Operators expect "AI" | Console copy says "programmable units" |
