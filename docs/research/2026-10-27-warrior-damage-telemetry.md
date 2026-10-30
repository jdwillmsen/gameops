# Warrior damage telemetry (2026-10-27)

Where `tactics.Scorecard.DamageDealt` and `DamageTaken` can come from, now that
the Bedrock console cannot report them. Tags: **[CODE]** read in this repo,
**[SPIKE]** observed in an earlier live spike, **[INF]** inferred, not yet seen.

## Answer

- **Damage taken is already measured.** Each warrior counts the health it loses
  per fight and in total, and `GET /v1/state` serves it as
  `tasks.fight.{total,current}.damage_taken`. Nothing reads it into a scorecard
  yet. That is the only missing step.
- **Damage dealt needs the mob's health stream.** The server sends other
  entities' `minecraft:health` in `AddActor.Attributes` and `UpdateAttributes`
  [SPIKE]. A drop, credited to the unit's own recent swing, is its damage
  dealt. No server change.
- Recommendation: (a)+(b), no behaviour pack. Prototype: `life.DamageSink`,
  `DamageCounter`, `DamageWatcher` in `minecraft/crew/internal/life/damage.go`,
  wired to nothing.

## What is observable

| Signal | Source | What it gives | Status |
|---|---|---|---|
| Own health value | `UpdateAttributes` for the unit's runtime ID | exact HP lost per change, so damage taken including falls, fire and hunger | [CODE] `life/telemetry.go:159`, `life/life.go:229`, `belief/belief.go:115` |
| Own hurt animation | `ActorEvent` hurt on the unit | a blow with no amount | [CODE] `life/telemetry.go:144` |
| Other entity's health | `AddActor.Attributes` (baseline), `UpdateAttributes` (changes) | HP removed from that mob; the attribute cannot go below 0, so a kill counts the health that was left, not the overkill | [SPIKE] on the ender dragon: real HP in both packets plus an `ActorEvent` hurt on each blow (dragon spike, row 1c). A cow went 10, 6, 2 under 4-damage hits (row 3e); the log does not say which packet gave the reading. Ordinary hostile mobs: [INF], same mechanism |
| Other entity's hurt/death | `ActorEvent` hurt, death | a blow landed, no amount | [CODE] used for `hits_landed`, `life/telemetry.go:339` |
| Boss health | `BossEvent` | health as a fraction | [SPIKE] dragon only (row 3c) |
| Attacker | nothing | no packet names who dealt a blow | [CODE] stated in `matchHit` and `blowAttacker` |
| `SetActorData` | entity metadata | flags and look; health is an attribute, not metadata | [CODE] the camera reads it for look only, `camera/hub.go:403`. [INF] no health key |
| Console | command replies | none: no amounts, no reliable death text | [CODE] `arena/recorder.go:13` |

The unit's own telemetry never uses the other-entity attribute stream:
`belief/entities.go:85` drops `AddActor.Attributes`, and both `UpdateAttributes`
handlers return early for any runtime ID but the unit's.

### Limits that matter for scoring

- **No attacker.** With several warriors on one mob, each credits a teammate's
  blow to its own swing, so a unit's Dealt is an upper bound, as its
  `hits_landed` already is (`life/telemetry.go:335`). A duel does not have this
  problem: one side's damage dealt is the other side's damage taken, which the
  victim measures exactly.
- **Absorption** is a separate attribute that also expires by itself, so it
  cannot be counted without inventing damage. A golden-apple or `absorption`
  buffed target reads low until its hearts are gone; the arena tier buffs use
  this (`2026-10-23-arena-mob-buffs.md`).
- **Regeneration and health boost** only move health; a drop is a drop. A mob
  that regenerates between two blows is measured correctly.
- **Visibility.** A unit sees only entities in its view distance. A mob out of
  view takes damage the unit never hears of.
- **"Fight" is the unit's engagement**, not the arena round. `fightStarted`
  (`life/telemetry.go:260`) fires on each `defend_started`, so one round can hold
  several. A round's scorecard should take the difference of two `total`
  readings, one at the director's `FightStart` and one at `Finish`.
- **Damage taken counts everything**, not only enemy blows. That is right for a
  score that punishes health lost, and wrong for any use that wants enemy
  damage only (`blowAttacker` already splits those in the log).

## Options

| | Cost | Attribution | Risk | Verdict |
|---|---|---|---|---|
| (a) Own health from packets | Done | n/a | none | Use as is, and feed scorecards |
| (b) Mob health deltas from packets | About 250 lines, client side | Window on own swings; upper bound in a squad | Low; reads only | Use for damage dealt |
| (c) Behaviour-pack script on `entityHurt` | Pack, a second script path to read the output (the console logs script `console` lines: see the mods spike), a lab server restart to load it | Exact: the event carries damage and damaging entity | Restart gate; pack only applies to the lab | Hold back; take it only if the measured error of (b) is too big |

(c) is the only exact source and the only one that reports absorption and
overkill. It is worth building if the trainer shows scores that are stable under
(a)+(b) but fail to track win rate. The existing stage-0 pack
(`packs/lab-mods-stage0`) and the verified `@minecraft/server` 2.10.0 path
(`2026-10-23-mods-spike-unverified-facts.md`) mean it is a small step later.

## Prototype

`minecraft/crew/internal/life/damage.go`, tests in `damage_test.go`.

- `DamageSink`: `FightStart()`, `Taken(amount)`, `Dealt(target, amount)`,
  `Unattributed(target, amount)`. `Dealt` is a drop on a mob the unit had a
  swing open at; `Unattributed` is every other drop on another entity. The
  split is itself a quality signal: a large Unattributed means swing windows or
  squad overlap are eating the number.
- `DamageCounter`: the sink that adds up. `Summary()` returns `Total` and
  `Fight`; `FightStart` zeroes `Fight` only.
- `DamageWatcher`: `Observe(pk)` from the read loop, `Swing(tick, target,
  ranged)` and `Tick(tick)` from the stepping goroutine, `FightStart()`,
  `Spawn(self)`. All sink calls happen in `Tick`. It reuses the existing swing
  windows (`meleeHitWindow` 8 ticks, `shotHitWindow` 40) and the open-swing cap.
- Guards: no drop on the first update with no `AddActor` baseline; baselines
  forgotten on `Spawn` and `ChangeDimension`, so a respawn or dimension change
  is not a blow; at most 256 tracked entities; a dead mob's entry is removed.

Tests (`go test -race ./internal/life/`): own swing credited, no swing,
wrong-target swing, expired swing, arrow window, one swing per drop, summed
drops, missing baseline, healing and other attributes, killing blow, own
health, respawn, dimension change, fight reset, caps, and two concurrent
tests.

## Wiring (done)

1. Unit: `noteSwing` also calls `watcher.Swing`, `Apply` calls `Observe`,
   `telemetryTick` calls `Tick`, `fightStarted` calls `FightStart`. One
   `DamageCounter` per `Life`; its `Dealt` is `damage_dealt` in
   `FightStats`, so `/v1/state` serves `tasks.fight.{total,current}.damage_dealt`
   next to `damage_taken`. `damage_taken` still comes from the telemetry's own
   health reading; the sink's `Taken` is not used. Other entities' health is
   also kept on `belief.Entity` (`Health`, `HealthKnown`).
2. Arena: `HTTPDamage.Totals` reads taken and dealt in one request per unit.
   The recorder puts the growth of each warrior's totals in `DamageTaken` and
   `DamageDealt`; a failed reading leaves both zero. A duel card takes
   `DamageTaken` from its own side and `DamageDealt` from the opponent side's
   taken, and reads nothing for an aborted round. `arenadirector duel` has no
   `--status-url` yet; the training bridge passes its source through.
3. Still open: the per-target ledger for squad mob fights. A squad's summed
   Dealt double-counts a teammate's blow, so `Score` credits Dealt only to a
   fight that was not won, and caps it.
4. Live check: the unit logs `health_seen` (entity type and first health) once
   per mob type per session, capped at 32 types. One warrior and one zombie on
   the lab should show `entity: zombie`; if it never appears, the [INF] below
   is false and Dealt stays zero.

## Open

- Confirm on the lab that an ordinary zombie's `UpdateAttributes` reach the
  client (one warrior, one zombie, log non-self `minecraft:health` changes). It
  is the one [INF] under the recommendation; the prototype is inert until then.
- Scorecard weights were re-set for a 20 HP unit (`tactics/scorecard.go`):
  taken costs 0.3 a point, so a lost bar (6) outweighs a minute of speed;
  dealt earns 0.2 a point, capped at 15, only on a loss or timeout. Re-check
  once real values flow.
