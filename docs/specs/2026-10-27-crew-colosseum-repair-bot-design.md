# Colosseum repair bot

**Status:** phase 1 (library only), draft for review
**Date:** 2026-10-27
**Ticket:** JDW-247
**Builds on:** [crew squads and arena](2026-10-23-crew-squads-and-arena-design.md), `lab/colosseum/` (generator, `layout.json`), `internal/arena`

## Why

Fights damage the Colosseum: creepers and boss effects blow holes, mobs and warriors leave debris, a gate is left open. Today the only fix is a full rebuild (about 30 minutes of console time). A repair bot restores only what changed.

## Decisions

- **Plan, not memory.** The desired state is the generator's `clear.cmds` then `build.cmds` (fill lines, later overrides earlier). `internal/repair` parses them (`ParseOps`) and paints a dense grid per 16-layer slab. `layout.json` has no blocks, so it supplies the footprint, pen and gate boxes and the report zones only.
- **Live state through a `Reader`.** Bedrock's console cannot dump blocks. Phase 1 ships `OpsReader`, which reads a scan written as the same fill/setblock lines. Phase 2 adds a reader over a mender unit's loaded subchunks (the crew client already holds them) so the bot scans without the console. A cell the scan cannot vouch for is `Unknown`: counted as unscanned, never repaired. The same goes for a cell the plan never mentions.
- **Diff, then merge.** Differing cells become runs, then rectangles (rows merged across z, layers across y), each one block, so a collapsed wall is a few fills. Plain `fill` is exact because a rectangle holds only cells that differ.
- **Order.** Planned-air fills first (clears intruders and debris), then solids bottom-up, then attachables and fluids bottom-up (`IsLate`, a name heuristic mirroring the generator's `late` materials). Fills are split under 32768 blocks.
- **Idempotent.** Every command is an absolute block state, so a second run, or a run on an intact arena, changes nothing. The tests apply the plan to a simulated world twice and re-diff to zero.
- **Strictly the footprint.** `Footprint(layout)` is the generator build box (centre -120..+119 x, -106..+105 z, y 36..122; the layout does not carry it, so it is restated and tested against the layout's areas and gates). A plan op or repair outside it is an error, not a clip. Ticking areas (`crew-repair-N`, 10 x 10 chunks) are added for the damaged tiles only and removed afterwards, even after a failure.
- **Dry run.** `Options.DryRun` sends nothing; the report still lists commands, damage by kind (missing, intruder, replaced), by zone (floor, stands-and-wall, plaza, underground, pen-gate-N, start-gate) and the top block changes.
- **Ticking areas.** Stale `crew-repair-N` areas are removed before the adds, a rejected add (including "already exists") aborts the run, and the bot waits `LoadWait` (5 s) after adding before the first fill. The 10-area server cap is not checked against areas other tools hold; a rejected add aborts. `Report.Status` (`dry_run`, `applied`, `aborted`, `refused`) and `Error` record how a run ended.
- **Out of scope.** Entities and container contents (armour stands, chests): `decor.cmds` owns them. Bulk loss (a rebuild is cheaper) falls back to `structure load` of the 48-block tiles saved before a build; not implemented.

## Build permission on the lab

Repair commands go through the same `arena.Console` the director uses (`send-command` in the lab container), which runs as the server console: no player op, no gamemode, no cheats flag. The bot needs no in-game permission and no new credential, only the kubectl or docker access `arena.sh` already uses. Lab only; `Apply` is never pointed at FWB. A later mender unit that reads blocks as a client still sends its writes through this console path, so the unit needs no operator rights either.

## Not repairing during a fight

The director tags its mobs `arena_wave` and `arena_boss` (`arena.FightCommands`). `Apply`:

1. refuses when any non-crew player is online (`arena.CheckAlone`);
2. counts tagged mobs, waits `Quiet` (default 30 s, several wave pauses, since the count is zero between waves), counts again;
3. re-checks players and tagged mobs every `Batch` (100) commands and stops (`ErrFightActive` for a mob) as soon as either fails, removing its ticking areas;
4. treats an unreadable reply as a refusal.

The double sample is probabilistic until an `arena_active` marker exists. Zero tagged mobs does not prove the director is finished (a wave book with long `pause` values, or the first spawn still to come). Procedure: run the bot after `arena.sh run` returns its result line, never concurrently. Phase 2 should have the director hold an `arena_active` marker (a tag on a fixed marker entity) for the whole run, which makes step 2 exact.

## Open questions

- Scan source for phase 2: mender unit's loaded chunks (range-limited, so several passes) versus `structure save` tiles decoded offline.
- Should the director publish `arena_active`? It is a small change to `internal/arena`, which another thread is working near.
- Gate state: a pen gate left open is damage by this diff (the plan says iron bars). Confirm that is wanted before a wave run is over, or the bot would close gates mid-run (the fight guard prevents it).
- Is the `IsLate` heuristic acceptable, or should the generator emit a stage per fill?

## Phases

| # | Output |
|---|--------|
| 1 | `internal/repair`: parse, diff, plan, report, guarded apply; unit tests (this change) |
| 2 | Reader over a mender unit's world view; `repair` command in `cmd/` and `arena.sh repair`; mender role in the crew roster; `arena_active` marker |
| 3 | Live trial on the lab after a boss run, with the owner watching |
