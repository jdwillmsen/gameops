# Wildlife test harness, logging and video evidence

Epic JDW-320. Status: approved by the owner on 2026-10-31 (scope and order).

## Why

The six tamable animals (tiger, cheetah, lion, black leopard, brown bear,
hyena; pack `packs/lab-mods-wildlife`) shipped to the crew lab with checks that
only reach as far as console hooks and unit tests. Three things have never been
seen in a real game: taming by the real interaction, the player-safety rules
(a pet must never go for a player), and how the animals look and move. The
owner wants agents to test all of it thoroughly, the server to log far more,
and video evidence. Every mechanism that is missing is built or flagged.

## What exists today (verified 2026-10-31, `origin/main`)

- **Preview server.** `tools/lab-preview` runs a throwaway Bedrock server
  (1.26.60.6, RakNet, offline mode, cheats on). `tools/lab-preview-smoke.sh`
  builds the crew bot and joins it as a player (`MC_OFFLINE=true`), then uses
  console commands and the pack's `scriptevent` hooks. Taming there uses the
  `gameops:tame` hook, not a real interaction.
- **Crew units** (`minecraft/crew`): movement, block actions, hotbar select,
  entity attack (`UseItemOnEntity` Attack action), eating. A token-guarded task
  API (`POST /v1/tasks`) with templates such as `goto`, `inspect`, `craft`.
  **Missing:** any entity Interact action (no taming, no sit toggle), a task
  verb for it, and the unit's belief only tracks Ignited, Charged, Angry for
  entities.
- **Camera** (`minecraft/crew/internal/camera`): a three.js page fed over a
  websocket from packet data. Models come from vanilla assets fetched at
  runtime; units decline the server's resource pack, so a custom entity draws
  as a coloured stand-in box. **No video anywhere**; the repo's "recorders" log
  positions, fight scorecards and LLM chats.
- **NetherNet.** The lab and FWB serve NetherNet (jdw-deployments #385, #389).
  The NetherNet dialer is on main (gameops #313 to #316) but needs a signed-in
  Microsoft account and refuses offline mode, so deterministic harness runs use
  the RakNet preview.
- **Logging.** The lab chart sets `CONTENT_LOG_FILE_ENABLED` only. The itzg
  image maps `CONTENT_LOG_LEVEL`, `CONTENT_LOG_CONSOLE_OUTPUT_ENABLED`,
  `ITEM_TRANSACTION_LOGGING_ENABLED`, `EMIT_SERVER_TELEMETRY`. The lab has no
  census or TPS exporter (FWB has them).
- **Devbox tools.** ffmpeg (x11grab, libx264, nvenc encoders), Xvfb, x11vnc,
  Chrome 151 and Playwright, docker, `/dev/kvm`. No NVIDIA driver, no Bedrock
  client of any kind, no qemu/wine/android emulator.

## Workstreams

Each is its own ticket, thread, worktree and PR series. They coordinate only
through the contracts below.

| Ticket | Workstream | Owns (paths) |
|---|---|---|
| JDW-321 | Real-interaction harness and scenario runner | `minecraft/crew/internal/act`, `task`, `belief`; `tools/wildlife-e2e/`; `tools/lab-preview-smoke.sh` |
| JDW-322 | Verbose logging, telemetry, structured pack log lines | `charts/crew-lab` (jdw-deployments); `packs/lab-mods-wildlife/behavior_pack/scripts` |
| JDW-323 | Camera renders the wildlife models; recorder to MP4 | `minecraft/crew/internal/camera`, `cmd/crew/camera.go`; `tools/wildlife-evidence/` |
| JDW-324 | Blockbench model clips | `tools/model-preview/` |
| JDW-325 | Spike: real Bedrock client capture | `docs/research/` only |

Order of value: harness, logging, camera video, clips; the spike runs alongside.
Streams must not edit each other's paths. `tools/lab-preview` is shared:
JDW-321 owns changes to it; other streams ask through a short issue comment or
a tiny PR rather than editing it in parallel.

## Contracts

1. **Evidence bundle.** One directory per run: `~/evidence/wildlife/<run-id>/`
   with `report.json`, `timeline.jsonl`, `server.log`, `videos/*.mp4`,
   `README.md` (what each file is). `<run-id>` is `YYYYMMDD-HHMMSS-<short sha>`.
   Never inside a repo checkout; nothing from it is committed except small
   representative stills in docs when a PR needs them.
2. **`report.json`.** `{run_id, commit, server: {version, transport}, scenarios:
   [{id, title, status: pass|fail|skip, started_at, ended_at, assertions:
   [{name, ok, detail}], artifacts: [relative paths]}]}`. JDW-321 produces it;
   JDW-323 attaches video paths to scenarios.
3. **`timeline.jsonl`.** One JSON object per line: `{t_ms, source:
   harness|server|bot, scenario, event, ...}`. JDW-323 turns it into video
   captions.
4. **Structured pack log lines (JDW-322 produces, JDW-321 consumes).** One
   line per event, prefix `gameops:event ` then a single JSON object:
   `{"ev": "tamed|sit|stand|aura|burst|hit|ability", "type":
   "gameops:lion", "id": "<entity id>", "owner": "<player id or null>", ...}`.
   Until JDW-322's pack release is live, JDW-321 asserts through the existing
   hooks (`gameops:census`, `gameops:effects`, console `testfor`/`querytarget`)
   behind a small adapter so the switch is one file.
5. **Camera model source.** JDW-323 adds an env var `CAMERA_PACK_DIR` pointing
   at a pack's `resource_pack` directory; the camera loads geometry, textures
   and animations from it. The recorder reads the unit to follow and the output
   path from its arguments.
6. **Preview server settings.** The preview runs the same logging env as the lab
   chart after JDW-322 (verbose content log, console output on) so harness runs
   see what the lab will show.

## Guardrails

- Lab and preview only. **Never FWB**, never operator commands there.
- No credential is minted, printed or put in a transcript. Microsoft
  device-code logins stay with the owner; harness bots are offline RakNet bots.
- Each PR carries a `risk/` label from `change-class`; JDW-322's lab rollout
  restarts the lab game server (the owner approved this; players online is
  fine, FWB untouched).
- Findings that are product bugs (an animal attacks a player, a tame fails, a
  pose is broken) become their own tickets under JDW-320 with the evidence
  attached; the harness reports them, it does not hide them.
- Packs change only with a version bump in both manifests and the dependency;
  a new pack version reaches the lab through the `lab-packs` image release and
  a hand bump of the tag in `charts/crew-lab/values.yaml` with
  `tools/crew-lab-kit-rev.py --write`.

## Out of scope

Training bots, FWB changes, changing wildlife behavior (other than bug fixes
the harness uncovers and the structured log lines of JDW-322).
