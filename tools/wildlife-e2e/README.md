# wildlife-e2e

Agent-run, deterministic in-game tests for the six tamable animals
(`packs/lab-mods-wildlife`), using the real interactions: two offline crew bots
join a throwaway Bedrock preview, feed, sit, hit and walk like players.

```
tools/wildlife-e2e/run.sh                      # every scenario
tools/wildlife-e2e/run.sh --list               # scenario ids
tools/wildlife-e2e/run.sh --only tame-lion --only safety-   # id or id prefix
```

Needs docker, go, python3 and the passwordless `sudo` that `tools/lab-preview`
already uses. It builds crew, starts its own preview (`wildlife-e2e` on UDP
19160 by default, `WILDLIFE_E2E_NAME` / `WILDLIFE_E2E_PORT` to change), stages
the checkout's pack, and removes the container, staged world and bots on exit,
whether it passes, fails or is killed. Exit status is 0 only when no scenario
failed. A skipped scenario names why in the report.

Never aim it at the lab or FWB: it is RakNet and offline, and the bots run with
`CREW_HARNESS=true`, which opens the entity task templates.

## Reading the evidence

Each run writes `~/evidence/wildlife/<YYYYMMDD-HHMMSS>-<sha>/`
(`WILDLIFE_EVIDENCE_DIR` moves the root; nothing lands in the checkout):

| File | What |
|---|---|
| `report.json` | `{run_id, commit, server, scenarios:[{id, title, status pass/fail/skip, started_at, ended_at, assertions:[{name, ok, detail}], artifacts}]}` |
| `timeline.jsonl` | `{t_ms, source harness/server/bot, scenario, event, ...}` per line: assertions, bot health changes, every change of a pet's tamed/sitting/owner/target |
| `server.log` | the preview's whole console log, `gameops:event` lines included |
| `bots/*.log` | crew's own logs |

A failed assertion in `report.json` is a finding. Do not weaken the assertion: file
a Jira Task under JDW-320 with the `detail` and the matching timeline lines.

## How it works

- `harness.py`: preview wrapper, bots (`/v1/tasks`, `/v1/state`), a watcher that polls both bots every 0.2 s and logs state changes, the evidence writer.
- `world.py`: fixed arena (flat world, bots at x10/z10 and x16/z10, ticking area, no spawning, day, no regeneration), summon, give, `tame()` (feeds until tamed; the 0.33 chance is the one genuine randomness, so it retries up to 40 times and reports the count).
- `adapter.py`: the only place that reads what the pack did. `gameops:event` lines once the pack emits them, otherwise the console hooks; a question the pack cannot answer returns `None` and the scenario skips that check with a reason.
- `scenarios.py`: the scenarios.

Pets are observed through entity metadata the bots receive (tamed, sitting, owner,
target), so "the pet went for B" is the pet's target id equal to B's unique id.

## Scenarios

| Id | Checks |
|---|---|
| `tame-<species>` (6) | the animal's food, fed by real interaction, tames it to the feeder; retries up to 40 feeds for the 0.33 chance and reports the count |
| `follow-owner`, `sit-stand`, `sit-stranger` | a standing pet follows; empty-hand interact sits and stands; a sitting pet stays; a stranger cannot sit it |
| `owner-defence` | a pet targets and kills a zombie that hurts its owner |
| `eco-*` | wild lion hunts an unnamed sheep; wild tiger takes a pig but spares a named cow; lion and hyena fight; a tamed lion leaves livestock alone; pets fight wild rivals but not another player's pet |
| `aura-lion-roar`, `aura-bear-guard` | the owner gets Strength / Resistance; a stranger and a sitting pet do not |
| `aura-hyena-pack`, `aura-leopard-night` | pack of one / two / four; night only. Self auras leave no mark a player can read, so these need the pack's `gameops:event` lines and skip without them |
| `burst-tiger`, `burst-cheetah` | burst starts on a landed hit, never on a timer, then tires and is ready again (needs `gameops:event`) |
| `safety-*` (6) | B hits A, A hits B, A hits its own pet, wild animals among players, a provoked wild animal, a sitting pet beside a rival: no animal ever targets a player, and no player is hurt by one |

`WILDLIFE_PACK_DIR=<dir>` stages another pack checkout (for example a candidate release) instead of the one in this repo.

## Limits

- Needs no real client, so what a client draws (models, animations, sounds) is out of scope; the camera and clip streams cover that.
- Tamed pets start standing; the suite asserts that rather than assuming a sit.
- Fixed seed: the preview is a flat world, so terrain does not depend on one; coordinates, rules (no spawning, fixed day, no regeneration) and the cleanup before every scenario make runs repeatable. Genuine randomness is only the taming chance, and mob AI timing.
