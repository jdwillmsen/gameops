# Crew enrichment: what the control plane could know and show (2026-10-27)

What crew units and crew-control already hold, what the Bedrock protocol hands
a unit that it decodes and then drops, and a ranked list of additions. Every
claim about code was read in the tree at `92699652`; protocol claims name the
packet, not a spec page.

## How to read this

- **Audience.** The owner and Josh Sorenson reviewing the fleet. The questions
  they ask are "is it healthy", "why did it stop", "is it getting worse" and,
  soon, "which world is behaving differently".
- **Cost** is the code surface plus the standing cost: bytes per poll, series
  count, rows. **Risk** is leakage (position, inventory, player names) and
  rollout (a unit change needs a crew image, a crew-control change does not).
- **Vocabularies are closed.** Any value that came from a unit reaches the
  console through a fixed list, and an unknown value renders as `other`
  (`docs/ui-design-principles.md`, rule 7). Every proposal below lists its list.
- **Public vs operator.** Public means a signed-out visitor may see it: no
  coordinates, no item stacks, no player names, no free text. Anything else is
  operator-only, signed-in, `Cache-Control: no-store`.

## What existed at the baseline (`92699652`)

The health and hunger gauges (item 1) are now implemented in crew-control.
Dashboard and replay-history integrations remain separate work.

| Surface | What it carries | Where |
| --- | --- | --- |
| Unit `/v1/state` | position, yaw, dimension, health, hunger, held slot, inventory (name, count), effects (id, name, amplifier, duration), unique id, current task and recent tasks, `tasks.fight` (swings, hits, damage taken, deaths; total and current) | `crew/internal/belief/belief.go`, `crew/internal/statusapi/statusapi.go`, `crew/internal/life/telemetry.go` |
| Poller | per unit `Status` (online, health, hunger, dead, task) and `Live` (inventory, effects, player id), then derived events: online, offline, died, respawned, task_*, stuck, low_health, low_hunger | `crew-control/internal/telemetry/poller.go`, `events.go` |
| Unit-pushed events | `kit_failed`, `died`, `respawned`, `session_error` with a closed `reason_code` | `crew-control/internal/events/unitevent.go` |
| Fleet metrics | task starts, finishes, failures by reason, durations, deaths, stalls, state gauges, time in state | `crew-control/internal/metrics/fleet.go` |
| Position stores | heat grid (4x4 blocks, 6 h) and replay rows (`crew_position`, 30 days), overworld only | `internal/heat`, `internal/replay` |
| World ledger | places, chests, stock as last seen, per-reading age | `internal/ledger` |
| Inventory history | per-unit item-total deltas as `inventory_changed` events | `internal/invhistory` |
| Insight engine | R1 zero success, R2 throughput drop, R3 long task, R4 common mode, over unit-log events only | `internal/insight/rules.go` |

## Decoded or held by a unit, but not reported

Verified by grep: crew-control has no reference to `tasks.fight`, to
`crew_movement_corrections_total`, or to any camera-side world state.

| Datum | Where the unit has it | Reported today |
| --- | --- | --- |
| Fight totals (swings, hits landed, damage taken, deaths) | `life.FightSummary` in `/v1/state` `tasks.fight`; Prometheus `crew_swings_total` etc. on the unit's own `/metrics` | No. crew-control never parses it, and cannot scrape unit metrics |
| Server corrections of predicted movement | `crew_movement_corrections_total` (`act/act.go`) and `CorrectPlayerMovePrediction` in belief | Unit `/metrics` only |
| Time of day, day-cycle rule | `Hub.setTime`, `gameRules` (`camera/hub_sky.go`) from `SetTime`, `GameRulesChanged` | Camera page only |
| Biome per column and climate table | `BiomeDefinitionList`, `subchunk.TopBiomes` (`camera/hub_sky.go`) | Camera tint only; the audit notes "crew gets no biome data" in belief |
| Nearby entities (type, position, head yaw, player flag) | `belief.Entities` (`belief/entities.go`) | Used by warrior logic; never reported |
| Stack data of held and carried items (damage, enchantments) | `Item.Wire` (`protocol.ItemStack`, `belief/inventory.go`) | `json:"-"`: name and count only |
| Armour on other actors | `MobArmourEquipment` in camera hub | Own armour is not tracked at all |
| Dimension | `Snapshot.Dimension` | Parsed by the poller into `Position.Dim`; only used to restrict heat and replay to the overworld |
| Chunk load state | `world/blocks` store, `LevelChunk`, `SubChunk`; `chunk_not_loaded` and `kit_chunk_not_loaded` reason codes | As failure reasons only |
| Path result | `internal/path` returns found or not; failure text becomes `no_path` | As failure reason only |
| Attributes other than health and hunger | `UpdateAttributes` carries saturation, exhaustion, level, experience, movement speed | Dropped: `belief.Apply` switches on two names |
| Collision flags | `InputFlagVerticalCollision` is sent in `PlayerAuthInput`; the unit computes collision locally | Not reported |
| Latency, server tick rate | `agent` exports `mc_agent_server_tps` and `mc_agent_link_rtt`; crew has no equivalent | No |
| Health and hunger | `Status.Health`, `Status.Hunger`, polled each second | In the SSE status and alerts, but **never as a metric**, so no chart, no history, no rate |

## Ranked list

Score is value for the two reviewers against cost and risk, 1 (low) to 5 (high).
"Needs unit" means a crew image change; otherwise crew-control alone.

| # | Enrichment | Value | Cost | Risk | Needs unit |
| --- | --- | --- | --- | --- | --- |
| 1 | Vitals gauges: health, hunger | 4 | 1 | 1 | no |
| 2 | Fight telemetry passthrough | 4 | 2 | 1 | no |
| 3 | World and dimension identity on every series and event | 5 | 2 | 1 | no |
| 4 | Movement corrections as a stuck cause | 4 | 2 | 1 | small |
| 5 | Effect events and effect alerts | 3 | 2 | 1 | no |
| 6 | Tool and armour wear | 4 | 3 | 2 | yes |
| 7 | Gather yield by item class | 4 | 3 | 1 | no |
| 8 | Environment: day phase, biome, weather | 3 | 3 | 1 | yes |
| 9 | Threat context: nearby hostile count | 3 | 3 | 2 | yes |
| 10 | Per-world comparison panel | 5 | 3 | 1 | no, after 3 |
| 11 | Food saturation, XP level | 2 | 2 | 1 | yes |
| 12 | Link latency and server tick rate | 3 | 3 | 1 | yes |
| 13 | Chunk-load state around the unit | 3 | 4 | 2 | yes |
| 14 | Path planning outcomes | 3 | 4 | 1 | yes |
| 15 | Travel distance and energy cost | 3 | 3 | 1 | no |

Items 1 to 10 have tickets under JDW-142 (keys in the last section).

### 1. Vitals gauges: health and hunger

- **Source.** `Status.Health`, `Status.Hunger` in `telemetry/poller.go`, from
  `/v1/state` `health`, `hunger` (`belief.Snapshot`, attributes
  `minecraft:health`, `minecraft:player.hunger`). `UnitState` (`telemetry/states.go`) is what
  `metrics.Fleet.ObservePoll` receives; it carries state only.
- **Fields.** Metrics `crew_fleet_unit_health` and `crew_fleet_unit_hunger`
  (gauges, label `unit`, value 0 to 20). Series are absent while the value is
  unknown or the unit is offline, so a flat line never means "no data".
- **Privacy.** Public: both are already in the public status stream. `/metrics`
  is cluster-only.
- **Surfaces.** Dashboard line panel per unit and a fleet minimum tile; alert
  rule `health_falling` (a drop of N over a window, earlier than `low_health`
  at 6); the unit detail sparkline.
- **Feeds.** The explainer can say "health was 4 for 40 s before it died".
  Replay can
  colour a trail by health if the position row is later widened.
- **Cost and risk.** Two gauges per unit, bounded by the roster. Nothing
  leaves the cluster. Cheapest item on the list.

### 2. Fight telemetry passthrough

- **Source.** `life.FightSummary` served as `tasks.fight` in `/v1/state`
  (`statusapi.go`, `life/telemetry.go`). Not parsed by crew-control.
- **Fields.** Metrics `crew_fleet_unit_swings_total`,
  `crew_fleet_unit_hits_landed_total`, `crew_fleet_unit_damage_taken_total`
  (counters from poll deltas, label `unit`; a total that goes backwards is a
  unit restart and re-baselines). Derived `hit_rate` is computed in the query,
  not stored. Event kind `fight_ended` with fields `swings`, `hits`, `damage`,
  `outcome` in `win | loss | disengaged | other`.
- **Privacy.** Public counts; the attacker type (`killer`) stays operator-only
  because it names mob types seen near a position.
- **Surfaces.** Arena panel: hit rate and damage per fight per unit; chart of
  damage taken per minute; alert `fight_losing` (damage taken without hits).
- **Feeds.** A new insight rule R5 "ineffective fighter" (many swings, no
  hits) and an explainer cause class `combat`.
- **Risk.** Counters reset on restart; the delta logic must treat that as a
  baseline, not a negative.

### 3. World and dimension identity

- **Source.** `Snapshot.Dimension`; the roster entry that owns the unit.
  Today heat and replay silently keep overworld rows only
  (`heat/sampler.go`, `replay/recorder.go`), and the event store has no world.
- **Fields.** `world` (closed: the roster's world slugs, `lab` today) and
  `dim` in `overworld | nether | end | other` on events, metrics labels and
  position rows. `crew_fleet_unit_dimension{unit}` gauge as the numeric id.
- **Privacy.** `world` and `dim` are public; positions stay operator-only.
- **Surfaces.** A world picker in the bar; a dimension chip on the wall tile;
  alert `unit_wrong_dimension` when a unit leaves the dimension its task
  expects.
- **Feeds.** Every rule becomes per-world with one grouping change. Without
  this, items 10 and any second world have nothing to compare.
- **Risk.** A new label multiplies series by the world count; bounded by the
  roster, but the metrics README must restate the worst case.

### 4. Movement corrections as a stuck cause

- **Source.** `CorrectPlayerMovePrediction` handled in `belief.Apply` and
  counted in `act/act.go` (`crew_movement_corrections_total`). A rubber-banding
  unit makes no progress and trips `stuck` with no reason.
- **Fields.** `/v1/state` gains `movement.corrections` (counter). crew-control
  emits event kind `rubberbanding` and reason code `movement_corrected` when
  the rate passes a threshold, and a metric
  `crew_fleet_unit_corrections_total{unit}`.
- **Privacy.** Public (a count).
- **Surfaces.** Stuck badge tooltip; chart of corrections per minute beside
  position; heat layer `corrections` on the 4x4 grid (operator-only, as heat is).
- **Feeds.** The explainer ranks `movement_corrected` above `no_progress`; R4
  common mode gets a second signal (every unit corrected at once is a server
  or physics change). Replay marks the correction instants.
- **Risk.** Needs one additive field in the unit state; the old poller ignores it.

### 5. Effect events and alerts

- **Source.** `belief.Effects`, `MobEffect` (`belief/effects.go`); the poller
  already reads `Live.Effects` for the detail page.
- **Fields.** Event kinds `effect_gained` and `effect_lost` with `effect` from
  the existing 36-name table (unknown id is `other`) and `amplifier`. Alert
  rules `harmful_effect` (poison, wither, fatal_poison, levitation, darkness,
  weakness, slowness, mining_fatigue, nausea, blindness).
- **Privacy.** Public name and amplifier, no duration.
- **Surfaces.** Effect chips on the wall tile; timeline marks.
- **Feeds.** Explainer: "mining slowed by mining_fatigue 2". Insight R2 gets a
  cause when throughput drops while an effect is on.

### 6. Tool and armour wear

- **Source.** `Item.Wire` (`protocol.ItemStack`) holds the stack's data,
  including damage, today `json:"-"`. Armour is carried in `InventoryContent`
  window `armor`, not tracked.
- **Fields.** `/v1/state` inventory item gains `durability_pct` (0 to 100,
  absent when the item has none). Event `tool_worn` (`slot` in
  `held | helmet | chest | legs | boots`, `bucket` in `low | critical`). Metric
  `crew_fleet_unit_tool_durability_pct{unit}` for the held tool.
- **Privacy.** Operator-only values; public sees only the bucket.
- **Surfaces.** Inventory panel bar; alert `tool_breaking` before a gather
  fails with `missing_tool`; per-world chart of tools consumed per hour.
- **Feeds.** Turns the `missing_tool` reason from a surprise into a predicted
  event, so the explainer can say "the axe broke" and R3 can discount a
  long task that ended in a break.
- **Risk.** Reading damage from the item data must handle every stack shape;
  fail closed to "unknown".

### 7. Gather yield by item class

- **Source.** Inventory deltas already recorded by `invhistory/tracker.go`
  (`inventory_changed`), and the task template. Heat's "mining" layer
  explicitly counts time, not breaks.
- **Fields.** Metric `crew_fleet_gathered_items_total{unit, class}` with
  `class` in `wood | stone | ore | dirt | crop | food | other`. Rate per
  minute is a query.
- **Privacy.** Public by class; exact item names stay operator-only.
- **Surfaces.** Stacked chart per class; heat layer `yield` (operator) weighting
  the position grid by items gained, which replaces the time proxy.
- **Feeds.** R2 can compare yield, not just completions (a unit completing
  empty hauls). The ledger's stock readings reconcile against it.

### 8. Environment: day phase, biome, weather

- **Source.** `Hub.setTime` and `gameRules` (clock), `biomeDefinitions` and
  `noteBiomes` (biome ids and names). Both live in the camera package, not in
  `belief`. Weather arrives as `LevelEvent` start and stop rain, which is not
  handled today.
- **Fields.** `/v1/state` `env`: `day_phase` in `dawn | day | dusk | night`,
  `biome` (name from a fixed list of the roughly 90 vanilla biomes, else
  `other`), `raining` boolean.
- **Privacy.** `day_phase` and `raining` public; `biome` operator-only (a biome
  narrows a position).
- **Surfaces.** Sun and rain chip on the wall; heat layer by biome; chart of
  deaths by `day_phase`.
- **Feeds.** Explainer: "died at night, 3 hostile mobs near". Replay shades the
  background by phase.

### 9. Threat context: nearby hostiles

- **Source.** `belief.Entities.Each` (type, position, player flag).
- **Fields.** `/v1/state` `threat`: `hostiles` (count within 16 blocks, capped
  at 9), `players` (count only, never names). Event `threat_high` when the
  count crosses 3.
- **Privacy.** Counts public; types and positions operator-only. Never list
  player names: the lab is a world real players join.
- **Surfaces.** Threat dot on the wall tile; alert `under_attack`; arena heat
  layer of where threats met units.
- **Feeds.** Cause class `combat` for deaths and stalls; R1 can tell a failing
  unit from a besieged one.

### 10. Per-world comparison panel

- **Source.** Depends on item 3. Reads existing and new per-world series.
- **Fields.** Panel contract (`README` "Panel contract") with a `world`
  grouping. Rows: units online, done per hour, failure rate, deaths per hour,
  stuck per hour, median task duration, mean health. Delta against the fleet
  median, shown as a number and a word (`above`, `near`, `below`), never colour
  alone.
- **Privacy.** Public aggregates only.
- **Surfaces.** Dashboard panel with a Table view; chart of done per hour per
  world with the categorical registry (max eight series, then "Other").
- **Feeds.** Insights run per world, and a new fleet rule R6 "one world
  diverges" fires when a world sits outside the other worlds' band for an
  hour.

### Lower ranks

- **11. Saturation and XP.** `UpdateAttributes` carries `minecraft:player.saturation`
  and `minecraft:player.level`; add two cases in `belief.Apply`. Value is low
  until food automation exists; hunger already alerts.
- **12. Latency and tick rate.** The agent already exports
  `mc_agent_server_tps` and `mc_agent_link_rtt`. Crew can measure RTT from
  `NetworkStackLatency` or its own request round trips. A slow server explains
  fleet-wide slowdowns, which R4 currently infers from session errors.
- **13. Chunk load state.** Count loaded sub-chunks around the unit from
  `world/blocks`; surfaces as a "world not loaded" cause for `chunk_not_loaded`.
  Costs a per-tick scan; sample at 1 Hz.
- **14. Path outcomes.** Event `path_result` with `found | none | partial` and
  `expansions` (capped integer). Needed to tell a bad target from a blocked
  route; costs a hook in `internal/path`.
- **15. Travel and energy.** Distance walked per unit is derivable from replay
  rows already stored; hunger drop per 100 blocks is the energy cost. Pure
  crew-control, but sampled at 2 s, so it under-reads fast movement.

## Privacy split in one table

| Public (signed out) | Operator only |
| --- | --- |
| health, hunger, dimension name, world slug, effect names, fight counts, correction counts, hostile and player counts, day phase, raining, yield by class, durability bucket, per-world aggregates | position, yaw, biome, exact item names and counts, durability percentages, killer and mob types, reason free text, chunk and path detail |

Player names never appear in either column.

## Cheap wins (report data the units already send)

1. Health and hunger gauges (item 1). Implemented with this document.
2. `tasks.fight` counters (item 2): the field is already on the wire.
3. `dimension` as a gauge and as a world label source (item 3).
4. Effects to events (item 5): the poller already parses them.
5. Yield by class (item 7): the inventory tracker already records the deltas.
6. Travel distance from stored replay rows (item 15): one query, no new data.

## Cross-world enrichment

A second world needs three things before any comparison is honest.

- **Identity.** `world` on roster entries, events, position rows and every
  metric. Item 3 is the prerequisite for everything else here.
- **Normalisation.** Raw counts differ by unit count; compare per
  unit-hour (`done / sum(crew_fleet_unit_state_seconds_total{state=~"working|idle"})`), and show the unit
  count beside every rate.
- **Context.** A world is not the same task mix, so compare by template
  (`gather_and_deposit` against itself), never overall. Day phase and biome
  (item 8) separate "this world is harder" from "this world is at night".

Useful per-world views: done per unit-hour by template; failure reasons as a
stacked share; deaths per 100 working hours with the `killer` class; mean
health; stuck rate with corrections (item 4) as a cause; yield per class per
hour (item 7). A divergence alert (R6) compares a world to the median of the
others and needs at least two other worlds, else it stays quiet.

## How each feeds the engines

- **Insight engine.** Rules are pure functions over `unitlog.Event`. Adding
  event kinds `effect_gained`, `tool_worn`, `rubberbanding`, `threat_high`,
  `fight_ended` to `unitlog` and the closed kind list is enough for new rules
  R5 (ineffective fighter) and R6 (world divergence) with no new plumbing.
  Metric-only items (1, 7) feed dashboards and alert rules, not R1 to R4.
- **Explainer.** `explain` ranks causes from events near a stall. New cause
  classes `movement`, `combat`, `wear`, `environment` come from items 4, 2/9,
  6 and 8, each only when its event sits inside `CauseWindow` of the stall.
- **Replay.** Replay joins position rows with events by time. Items 4, 5, 9
  add event marks for free once the kinds exist; item 8 shades the background;
  item 1 would need a health column on `crew_position` (a migration, so it
  is a separate decision and not proposed here).

## Bounded cost summary

| Item | Standing cost |
| --- | --- |
| 1 | 2 gauges per unit |
| 2 | 3 counters per unit |
| 3 | one label; series multiply by world count |
| 4 | 1 counter per unit, one event per episode |
| 5 | events only on change |
| 6 | one gauge per unit, events on bucket change |
| 7 | 7 classes x units counters |
| 8 | 3 small fields per state; no new endpoint |
| 9 | 2 small fields; events on threshold crossing |
| 10 | no new data; one panel read per refresh |

## Tickets

Created under epic JDW-142, in rank order. Item 1 was implemented first.

| Rank | Ticket | Enrichment |
| --- | --- | --- |
| 1 | JDW-253 | Vitals gauges: health, hunger |
| 2 | JDW-254 | Fight telemetry passthrough |
| 3 | JDW-255 | World and dimension identity |
| 4 | JDW-256 | Movement corrections as a stuck cause |
| 5 | JDW-257 | Effect events and alerts |
| 6 | JDW-258 | Tool and armour wear |
| 7 | JDW-259 | Gather yield by item class |
| 8 | JDW-260 | Day phase, biome, rain |
| 9 | JDW-261 | Threat context |
| 10 | JDW-262 | Per-world comparison panel |
