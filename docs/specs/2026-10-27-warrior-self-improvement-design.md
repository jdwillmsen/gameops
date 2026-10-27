# Self-improving warriors — design

Epic JDW-285. Owner brief (2026-10-27): warriors that improve their combat over
time, kite, handle every mob type, learn and enact strategies, train for PvP
against real players, start with bot-vs-bot, and stay controllable. Run many
agents in parallel.

## Intent

Today every warrior tactic is a compile-time constant in `life/warrior.go`
(`kiteBackoff`, `healBelow`, `orbitMin` ...). Nothing can improve. The fix is to
make those numbers a runtime **Profile**, score real fights, and let a
lab-only trainer search for better Profiles, per opponent **family**.

## Decisions (my calls unless noted)

- Learning = parameter and tactic-selection search (evolution strategy with
  repeated, side-swapped evaluation), not a neural policy: every change is a
  readable diff and rolls back. A learned policy is a later spike.
- Training runs on the crew lab only (owner: players online is fine there).
  FWB is never touched.
- A Profile reaches **production** units only through the promotion gate and
  needs the owner's sign-off (R3). Lab units may load candidate Profiles freely.
- Default Profile reproduces today's behaviour exactly; a unit with no profile
  is unchanged.
- Opponent families: zombie, skeleton, creeper, spider, blaze, enderman, witch,
  illager, brute, boss, player (another bot or a human fighting as a player).

## Control (owner requirement)

1. Kill switch: one flag (`tactics.training=off`) stops every trainer run and
   makes lab units fall back to the default Profile within one fight.
2. Bounds: every parameter has a closed range (`tactics.Bounds`); loads are
   clamped, so no learned value can make a unit inert or suicidal.
3. Hard caps on every loop: max generations, max wall-clock, max fights, max
   consecutive failures. No uncapped loop.
4. Lineage: every Profile records its parent and the scorecards behind it.
   `tacticsctl rollback` restores any ancestor.
5. Promotion gate: `tacticsctl promote` refuses without an owner approval file.

## Contract (committed first, on this branch)

`minecraft/crew/internal/tactics`: `Params`, `Bounds`, `Profile` (default +
per-family overrides), `Scorecard` and its `Score()`. Every stream builds on it.

## Work streams

Phase 1a (parallel, disjoint packages):
- S1 engine: warrior reads a Profile at runtime; family classifier; a tactic
  hook registry so family tactics are new files, not edits to shared ones.
- S2 telemetry: fight recorder turns a fight into Scorecards (JSONL).
- S3 trainer: `cmd/trainer` ES loop, champion store, lineage, hard caps.
- S4 duel: `arenadirector duel`, bot vs bot, mirrored kits, side swap.
- S5 mob book: scenario book covering every family and tier.
- S6 controls: kill switch, lab-only guard, `tacticsctl` promote/rollback.
- S7 delivery: squad service serves a Profile to lab units.
- S11 report: leaderboard and per-family progress report.

Phase 1b (after S1 merges): kiting for every role, PvP tactics, and one tactic
per family (creeper, skeleton, spider, blaze, enderman, witch, illager, brute,
boss) — each in its own file behind the hook registry.

Phase 2: overnight capped loop, integration fights, reviewed results.

## Non-goals

Neural policies; any change to the FWB server; auto-promotion to production;
touching players' inventories.

## Success

A Profile found by the trainer beats the default Profile over >= 30 mirrored
fights per family at a stated confidence, and the whole loop can be stopped by
one command.
