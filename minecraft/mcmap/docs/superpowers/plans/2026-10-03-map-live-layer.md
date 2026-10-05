# Map Live Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Ticket:** JDWLABS-695 — phase 3 of JDWLABS-658 (live FWB world map).
**Prerequisites shipped:** JDWLABS-693 (mcmap 1.0.0, bridge 0.5.0 snapshot) and JDWLABS-694 (mcmap 1.1.0, agent 0.24.0, public route + in-game login) are both live at https://fwb.prd.jdwlabs.com.

**Goal:** Players and mobs in loaded chunks appear on the map and move within 3 s of moving in game, with no world experiments enabled.

**Architecture:** A stable-API `@minecraft/server` behaviour pack samples `world.getAllPlayers()` and `dimension.getEntities()` once a second and prints one-line JSON records to BDS stdout. The console bridge already reads every stdout line off the mc-server-runner websocket; it gains a *second*, separate bounded ring for those records and a long-poll endpoint over its existing bearer auth. mcmap polls that endpoint, assembles the latest complete generation per dimension in memory, and fans it out to browsers as Server-Sent Events on the existing session cookie. The page draws the markers on a canvas Leaflet layer.

```
BDS (+ behaviour pack)  --stdout-->  mc-server-runner ws
      │  [<ts> INFO] [Scripting] MCMAP1 {...}
      ▼
console bridge  ScriptLog (ring, notifier)
      │  GET /script?since=<id>&wait=2000   (bearer, long-poll)
      ▼
mcmap  internal/live  Store (latest complete gen per dimension) + Hub (fan-out)
      │  GET /api/live?dimension=overworld  (SSE, session cookie)
      ▼
browser  web/live.js  canvas marker layer, type filters, age readout
```

**Tech stack:** Go 1.27 stdlib `net/http` (SSE via `http.NewResponseController`), Prometheus `client_golang`, plain JavaScript `@minecraft/server` 2.10.0 script module (no bundler, no npm), Leaflet (already vendored), Helm chart `minecraft-fwb` in `jdw-deployments`.

**Spec:** `minecraft/agent/docs/superpowers/specs/2026-09-28-gameops-monorepo-design.md`, Part 1 (umbrella), "Probe evidence" and "Map architecture".

**Correction to the spec:** the umbrella diagram says the bridge's `GET /events` is an "SSE of stdout (exists)". It is not. `GET /events` is a polled JSON endpoint over a 2,000-entry ring of *parsed* events, and `parseLine` in `minecraft/bridge/events.go` recognises only connect, disconnect, crash and `^\[.*\]\s*\[ERROR\]`. A `[... INFO] [Scripting] ...` line matches none of them and is dropped. This plan adds a parallel path rather than widening `/events`; see Task 2 for why.

**Second correction to the spec:** the umbrella spec describes the pack as its own `minecraft/mcmap-pack` component (`kind: pack`, `release.artifacts: [github-asset]`). That is not what this plan builds. The pack is embedded in the mcmap image via `go:embed` and installed by `mcmap install-pack` (Decision D1), so there is no `mcmap-pack` component and no `github-asset` release path. Task 13 amends the spec for both errors.

---

## Global constraints

### What must NOT happen

These are the guardrails the epic and the ticket are built on. Each has a verification step in this plan.

- **No Beta APIs.** The pack's `manifest.json` depends on `@minecraft/server` version `2.10.0` and nothing else. No `@minecraft/server-ui`, `-net`, `-admin`, `-gametest`, no `-beta` version strings, no `script_eval` capability, no `"experimental": true` on any module.
- **No experiments enabled, anywhere.** No change to `server.properties`, no `experiments` block, no chart value that toggles one. `level.dat` must still report `experiments_ever_used=0` and `saved_with_toggled_experiments=0` after the pack has run.
- **Achievements intact.** `cheatsEnabled=0` in `level.dat` and `allowCheats: false` in the chart stay as they are. The pack never calls `runCommand`, never mutates an entity, never writes a dynamic property — it is read-only by construction, so it cannot be what flips a world flag.
- **No resource pack.** Behaviour pack only. A resource pack would make clients download content and could engage `texturepack_required`.
- **The bridge's existing `/events` ring is not touched.** The agent's roster polls it (`minecraft/agent/internal/adapters/bridgeroster.go`); a 1 Hz record stream through that 2,000-entry ring would evict connect/disconnect events in ~33 minutes and inflate every roster poll.
- **Live data never leaves the session boundary.** `/api/live` is registered through `s.gated(...)`. `GET /api/config`, which is public, says nothing new. The internal listener stays off the HTTPRoute.
- **The init step must not be able to take the world offline.** An initContainer that exits non-zero blocks the game server. The 2026-09-06 incident (40 h outage from a sidecar's secret dependency) is the precedent. The installer exits 0 on anything it cannot do, logs why, and the missing live data is what alerts.
- **No ticket IDs in code or code comments.** Comments explain *why*, not *what*. Plan documents and their filenames may carry the ID.

### Contract: the record format

One line per record, under 3,500 bytes of JSON so the 4 KB ceiling that makes the *next* line start with a NUL byte is never reached (probe evidence, 2026-09-28).

```
[<ts> INFO] [Scripting] MCMAP1 {"gen":417,"tick":8340,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,"items":[{"i":"-42949672","n":"Dotablaze","x":120,"y":64,"z":-310,"r":-37}]}
```

- `MCMAP1` is a sentinel plus format version. mcmap ignores any `[Scripting]` line without it, so other packs' output and a future `MCMAP2` are both harmless.
- `gen` is a monotonic generation counter, one per sample. `tick` is `system.currentTick`.
- `kind` is `players`, `mobs` or `tick`. `kind:"tick"` is the heartbeat: one record per sample even when the server is empty, carrying counts, the sample's scan time and the current interval (both in ms) and no items, so a throttled pack is visible in production and not only in the rig log. Staleness therefore means the pipeline broke, never that nobody is online.
- `part`/`parts` split a list that does not fit one record. mcmap applies a generation only when every part has arrived.
- `more` is how many entities were dropped by the cap (1,000 per dimension by default, Decision D3), so the page can say "showing 1,000 of 1,612".
- Item keys are short on purpose (`i` id, `n` name or nameTag, `t` typeId without the `minecraft:` prefix, `x`/`y`/`z` rounded to one decimal, `r` yaw in degrees for players).
- `i` for a player is `player.id`, the per-session entity id. The stable API does not expose XUIDs, so the page matches "me" by gamertag against `GET /api/me`. Markers carry no XUID.

### Budgets and limits

| Thing | Value | Why |
|---|---|---|
| Sample interval | 1 s (`system.runInterval(..., 20)`) | The ticket's cadence |
| Record size cap | 3,500 bytes | Below the 4 KB NUL threshold |
| Mobs per dimension per sample | 1,000, default (Decision D3); a chart value, lowerable without a release | Bounds what is serialised, logged and drawn. It bounds the server's scan only if the `getEntities` query can limit the count (Task 5 confirms); otherwise the self-throttle is the only bound on scan cost |
| Pack self-throttle | if a sample's scan exceeds 20 ms, double the interval up to 5 s, then halve the effective mob cap to a floor of 100; step back after 30 consecutive samples under 10 ms; one record per state change | FWB already runs at 12–15 TPS (`tickRateAlert`) and the default cap is 2.5x what the first draft of this plan assumed; the pack must not be what pushes the tick rate lower |
| Bridge `ScriptLog` ring | 256 records | At the cap one sample is on the order of 50 records (about 17 parts of ~60-byte items per dimension, three dimensions), so ~5 samples of history, enough to survive one missed poll. The item size is an estimate; Task 5 measures parts per sample and this row is raised if it is wrong |
| Bridge long-poll wait | caller-supplied, capped at 25 s | Under the gateway's and the client's own timeouts |
| mcmap state TTL | 10 s | Older than this, markers disappear (ticket DoD) |
| SSE stream lifetime | `min(5 min, session expiry)` | Forces a re-verify of session and revocation on reconnect |
| SSE heartbeat | comment line every 15 s | The tightest idle limit on the path is the load balancer in front of the gateway: HAProxy in TCP mode with `timeout client 30s` / `timeout server 30s`. The gateway's own nginx `proxy_read_timeout` is the 60 s default. 15 s is half the tighter one; it must stay under 30 s (see V1) |

### Latency budget for the <3 s metric

| Hop | Expected | Measured by |
|---|---|---|
| Sample age at emit | ≤1.0 s | the 1 Hz interval; `mcmap_live_frame_interval_seconds` |
| BDS stdout → bridge ring | <50 ms | `mcmap_live_log_lag_seconds` (cross-check against the BDS line stamp) |
| Bridge ring → mcmap | <50 ms long-poll, ≤1 s if it degrades to polling | `mcmap_live_ingest_lag_seconds` |
| mcmap → browser frame write | <50 ms | `mcmap_live_fanout_seconds` |
| Browser paint | <100 ms | the page's own age readout, in the recording |

Worst case ~1.3 s, with ~1.7 s of headroom. If the long-poll has to be replaced by a fixed poll, add up to the poll interval.

---

## In-flight work and merge order

Three agents are in these repos now and two more tickets land before this one starts. Nothing in this plan should be merged into a file another ticket is holding without rebasing onto it first.

| Ticket | Repo/area | Files it owns | Status |
|---|---|---|---|
| JDWLABS-727 | `minecraft/mcmap` + chart | chunk-count gauges on the snapshot cycle: `internal/worker/worker.go`, likely a new chunk-counting package, `cmd/mcmap/main.go`, `README.md` metrics table; chart `templates/map.yaml` (PrometheusRule), a Loki rule, a Grafana dashboard, `values.yaml`, `tools/tests/test-map.sh` | In Progress |
| JDWLABS-729 | `minecraft/mcmap` + chart | two snapshot generations with an atomic switch: `internal/mirror/mirror.go`, `internal/worker/worker.go`, `cmd/mcmap/main.go`, `internal/config/config.go`, `README.md`, chart PVC size | Backlog, blocked by 727 |
| JDWLABS-730 | `minecraft/agent` | console `say` command parsing | In Progress |
| JDWLABS-728 | `minecraft/agent` | join-time damage notice; polls mcmap's `/internal/v1/...` | Backlog, blocked by 727 |

**`minecraft/bridge` is uncontended.** None of the four touches it. Tasks 1–3 can start and merge immediately.

**`minecraft/agent` is not touched by this plan at all.** 695 needs no agent change: live positions travel bridge → mcmap → browser, and the login the SSE stream authenticates against already exists. Any agent-side idea (an in-game opt-out, a `!live` command) is a follow-up ticket filed after 728 lands, not part of this one.

**mcmap merge order.** Everything new goes in a new package (`internal/live`) and new web files, which collide with nothing. Four files are shared: `cmd/mcmap/main.go`, `internal/config/config.go`, `README.md`, and `web/embed.go`. Land the mcmap wiring (Task 7) **after 729 has merged**, and rebase, don't merge, onto its `main.go`/`config.go`. If 729 slips, Task 7 can land on top of 727 alone and accept one rebase later — but do not land it between 727 and 729, which is where a three-way conflict in `main.go` becomes likely.

**Chart merge order.** `charts/minecraft-fwb/templates/map.yaml`, `values.yaml` and `tools/tests/test-map.sh` are all held by 727 (alerts, dashboard, promtool tests). Land Task 10 **after 727's chart PR has merged**, and add a new alert to the existing `minecraft-fwb-map` group rather than restructuring it.

Per-task collisions are called out in each task below under **Collision**.

---

## File structure

```
gameops/
  minecraft/bridge/
    script.go                  NEW  ScriptLog ring + notifier + line recognition
    script_test.go             NEW
    console.go                 EDIT one call in ingestEvents to feed ScriptLog   [uncontended]
    http.go                    EDIT GET /script route + handler                  [uncontended]
    config.go                  EDIT nothing expected; see Task 3                 [uncontended]
    README.md                  EDIT endpoint table, metrics                      [uncontended]
  minecraft/mcmap/
    pack/manifest.json         NEW  behaviour pack manifest, stable API only
    pack/scripts/main.js       NEW  the sampler
    pack/README.md             NEW  what it emits and why it is read-only
    internal/pack/pack.go      NEW  go:embed of pack/, install + register logic
    internal/pack/pack_test.go NEW
    internal/live/record.go    NEW  parse one line (NUL-tolerant), types
    internal/live/store.go     NEW  latest complete generation per dimension, TTL
    internal/live/source.go    NEW  bridge long-poll client
    internal/live/hub.go       NEW  SSE fan-out
    internal/live/metrics.go   NEW
    internal/live/*_test.go    NEW
    internal/server/live.go    NEW  the SSE handler
    internal/server/server.go  EDIT one route + one field                        [low risk]
    internal/config/config.go  EDIT new env vars                                 [727/729]
    cmd/mcmap/main.go          EDIT wiring, install-pack subcommand              [727/729]
    web/live.js                NEW  marker layer, filters, age readout
    web/index.html             EDIT filter chips, live readout, script tag
    web/style.css              EDIT marker and chip styles
    web/embed.go               EDIT add live.js to the embed list                [low risk]
    README.md                  EDIT live section, env, endpoints, metrics        [727/729]
    Dockerfile                 EDIT nothing expected (COPY . . already covers pack/)
jdw-deployments/
  charts/minecraft-fwb/
    templates/map.yaml         EDIT env vars + one alert                         [727]
    values.yaml                EDIT map.live block, bedrock initContainers       [727]
    README.md                  EDIT the live layer and the kill switch           [727]
  tools/tests/test-map.sh      EDIT live exposure + alert tests                  [727]
```

---

## Review focus

The five things most likely to be wrong, and the test that proves each.

1. **A record over 4 KB puts a NUL on the front of the next line.** The pack must not emit one; the parser must survive one anyway. Tests: `TestEmitSplitsAtTheSizeCap` (Task 5, against a fixture of 1,200 entities, above the cap) and `TestParseStripsNulBytes` (Task 6).
2. **A partial generation must never be drawn.** If part 1 of 3 arrives and parts 2 and 3 are lost, the previous complete generation stays on screen until the TTL expires it. Test: `TestStoreKeepsLastCompleteGeneration` (Task 6).
3. **A revoked or expired session must stop streaming.** The stream is bounded and re-verified on reconnect; it does not outlive the cookie it was opened with. Tests: `TestLiveStreamEndsAtSessionExpiry`, `TestLiveStreamRequiresSession` (Task 8).
4. **The pack must not cost the server its tick rate.** FWB already averages 12–15 TPS. With the default cap raised to 1,000 per dimension this is the highest-risk item in the plan. Measured before/after on the local rig at the cap (Task 5, Task 11) and in production from `mc_agent_server_tps` against a rollback threshold fixed before rollout (Task 12, open question Q2), with the self-throttle proven by a fixture of 5,000 entities and shown to recover when the load is removed (Task 5).
5. **The init step must never block the game server.** Every failure path exits 0 with a log line; this property is the basis of Decision D2 and is not optional. Tests: `TestInstallExitsZeroWhenTheWorldIsMissing`, `TestInstallExitsZeroOnAnUnwritableDataDir`, `TestInstallIsIdempotent` (Task 4), plus a rig run that starts the server with a deliberately broken installer (Task 11).

---

## Task 1: Re-confirm the probe facts on a local rig

- [ ] Stand up BDS 1.26.52.x in Docker with `CONTENT_LOG_CONSOLE_OUTPUT_ENABLED=true` and a **copy** of the FWB world from the latest nightly archive. Nothing in this task touches production.
- [ ] Write a throwaway pack that prints one record of each size class (100 B, 3.5 KB, 5 KB) and confirm: the `[<ts> INFO] [Scripting] <message>` shape, the trailing blank line, and that only the >4 KB record puts a NUL on the next line.
- [ ] Confirm how the itzg image installs a behaviour pack. Check whether `PACKS_DIR` (and `/data/packs`) exists in the image's entrypoint scripts for the Bedrock image, and whether it rewrites the world's `world_behavior_packs.json`. **Read the entrypoint, do not assume.** Record the answer: it decides whether Task 4 writes `behavior_packs/` + `worlds/<level>/world_behavior_packs.json` itself, or just drops a directory under `packs/` and lets the image do it.
- [ ] Record the uid/gid the server container writes world files as, with the chart's `podSecurityContext` (`runAsUser: 1000`, `runAsGroup: 3000`, `fsGroup: 2000`). This decides the init step's `securityContext`.
- [ ] Record `experiments_ever_used`, `saved_with_toggled_experiments` and `cheatsEnabled` from `level.dat` **before** anything is installed.

**Collision:** none. No repo file changes.

**Evidence:** a comment on the ticket with the pasted line shapes, the NUL threshold, the pack-install mechanism with the file path in the image that implements it, and the pre-install `level.dat` flags.

---

## Task 2: Bridge — a separate ring for script records

- [ ] Add `minecraft/bridge/script.go`: a `ScriptLog` with a 256-entry ring, monotonic IDs within one process, and a `sync.Cond`-or-channel notifier so a waiter wakes the moment a record lands.
- [ ] Recognise a line as a script record by the `[Scripting]` tag followed by the `MCMAP1 ` sentinel. Store the JSON payload after the sentinel, plus the bridge's receive time, plus the raw line. Nothing else is stored; a `[Scripting]` line from any other pack is ignored.
- [ ] Do **not** extend `parseLine` or `EventLog`. Add one call in `Console.ingestEvents`, beside `c.Events.Ingest(line, now)`, so both rings see the same line. Comment why they are separate: the event ring is the agent's roster feed and a 1 Hz stream would evict the joins out of it.
- [ ] Metrics: `mc_console_bridge_script_records_total{result}` with results `ok`, `nul_stripped`, `oversize`, `unparseable`; `mc_console_bridge_script_last_record_timestamp_seconds`.

**Collision:** none. `minecraft/bridge` is owned by nothing in flight. `console.go` gets a one-line addition inside `ingestEvents`.

**Test:** `go test -race ./minecraft/bridge/...`. New: `TestScriptLogRecognisesOnlyOurSentinel`, `TestScriptLogRingDropsOldest`, `TestScriptLogWakesAWaiter`, `TestIngestFeedsBothRings` (one `[Scripting] MCMAP1` line reaches `ScriptLog` and is absent from `Events.Since(0)`; one `Player connected:` line does the reverse).

---

## Task 3: Bridge — `GET /script` long-poll

- [ ] Add `GET /script?since=<id>&wait=<ms>` to `newMux`, behind the same `s.authed(...)` bearer every other endpoint uses. Returns `{"records":[{"id","at","data"}...]}`, oldest first.
- [ ] Return immediately if anything is newer than `since`. Otherwise block until a record arrives, `wait` elapses (capped at 25 s), or the request context ends — then return whatever there is, including an empty list.
- [ ] Extend the per-request write deadline with `http.NewResponseController(w).SetWriteDeadline`, tolerating `http.ErrNotSupported`, exactly as `handleSnapshot` does. The server-wide `WriteTimeout` is `max(10s, CommandTimeout+800ms+5s)` ≈ 10 s and would otherwise cut a long wait short.
- [ ] A `since` far behind the ring returns everything retained, with a `"gap":true` flag so mcmap can log that it fell behind rather than silently mis-sequencing.
- [ ] README: add the row to the endpoint table, the metrics, and a paragraph on why this is not `/events`.

**Collision:** none.

**Test:** `TestScriptEndpointRequiresBearer`, `TestScriptEndpointReturnsImmediatelyWhenNewRecordsExist`, `TestScriptEndpointBlocksThenReturnsOnArrival`, `TestScriptEndpointReturnsEmptyAtTheWaitCap`, `TestScriptEndpointCapsTheWait`, `TestScriptEndpointFlagsAGap`, `TestScriptEndpointUnblocksOnClientDisconnect`.

**Evidence:** a bridge release (`bridge-v0.6.0`) with green CI, plus a rig run where `curl -N` against the endpoint prints a record a second from the throwaway pack of Task 1.

---

## Task 4: The install step

- [ ] Add `minecraft/mcmap/internal/pack`: `//go:embed` the `pack/` tree, a `Version()` derived from a content hash of the embedded files, and `Install(dataDir, level string, logger) error` that is idempotent — a no-op when the installed content hash already matches.
- [ ] Install by whichever mechanism Task 1 established. If writing directly: `<dataDir>/behavior_packs/mcmap-live/{manifest.json,scripts/main.js}`, then merge an entry into `<dataDir>/worlds/<level>/world_behavior_packs.json` by `pack_id`, preserving every entry already there, written atomically (temp file in the same directory, then rename).
- [ ] Add an `install-pack` subcommand to `cmd/mcmap/main.go`: `mcmap install-pack` reads `DATA_DIR`, `LEVEL_NAME` and an optional `PACK_MOB_CAP` (default 1,000) and nothing else — no bridge, no token, no renderer. The cap reaches the pack as a generated `scripts/config.js` (`export const MOB_CAP = ...`) because a script module cannot read the container's environment; the content hash behind `Version()` covers that file, so changing the cap reinstalls. An unparseable or non-positive `PACK_MOB_CAP` logs and falls back to the default, like every other failure here. A changed cap takes effect on the next server restart, since the initContainer only runs on pod recreation. `os.Args[1]` dispatch before `config.Load`, so the installer does not need the service's config.
- [ ] **Every failure exits 0 with a logged reason. This is required, not a nicety: Decision D2 accepts an initContainer on the game server pod only on this condition.** A missing world directory (a world BDS has not created yet), an unwritable `/data`, a `world_behavior_packs.json` that will not parse: all log and exit 0. The only non-zero exit is a half-written install it could not roll back, which is the one state where starting the server is worse than not starting it. Comment that reasoning in the code. Weakening this requirement reopens D2.
- [ ] Keep the pack's two UUIDs (header and module) fixed, generated once and written into `manifest.json`. Record them in `pack/README.md`; a changed UUID orphans the pack in the world's registration.

**Collision:** `cmd/mcmap/main.go` is shared with 727 and 729. The subcommand dispatch is a small, isolated addition at the top of `main()`. Land with Task 7, after 729.

**Test:** `TestInstallWritesPackAndRegistersIt`, `TestInstallIsIdempotent`, `TestInstallPreservesOtherPacks`, `TestInstallExitsZeroWhenTheWorldIsMissing`, `TestInstallExitsZeroOnAnUnwritableDataDir`, `TestInstallExitsZeroOnUnparseableWorldPacks`, `TestInstallIsAtomicUnderAFailedWrite`, `TestInstallFallsBackOnABadMobCap`, `TestInstallRewritesConfigWhenTheCapChanges`. The fail-open tests (`...WhenTheWorldIsMissing`, `...OnAnUnwritableDataDir`, `...OnUnparseableWorldPacks`) are release-blocking: mcmap does not ship without them green.

---

## Task 5: The script pack

- [ ] `pack/manifest.json`: `format_version: 2`, `modules: [{type: "script", language: "javascript", entry: "scripts/main.js", uuid, version}]`, `dependencies: [{module_name: "@minecraft/server", version: "2.10.0"}]`. No `capabilities`, no `-beta`, no second dependency, no resource pack.
- [ ] `pack/scripts/main.js`: `system.runInterval` at 20 ticks. Per sample, for each of the three dimensions: collect players (`world.getAllPlayers()` filtered by dimension, with `getRotation().y` for heading) and mobs (`dimension.getEntities()`), capped at the per-dimension limit, and emit records with `console.log` — building each record incrementally and flushing when the serialised length approaches 3,500 bytes.
- [ ] Always emit the `kind:"tick"` heartbeat, even with zero players and zero mobs, carrying the sample's scan time and current interval.
- [ ] **Self-throttle (required).** With the default cap at 1,000 mobs per dimension this is the control that protects the tick rate, not a nicety. Time each whole sample (all three dimensions) against a 20 ms budget. Over budget: double the interval up to 5 s; if still over budget at 5 s, halve the effective mob cap down to a floor of 100. Recover with hysteresis: after 30 consecutive samples under 10 ms, step back one level, otherwise one transient spike leaves the layer degraded for good. Emit one record per state change and never one per sample. When the two conflict the <3 s metric yields to the tick rate, never the reverse.
- [ ] Take the mob cap from `scripts/config.js` (written by the installer from `PACK_MOB_CAP`), with 1,000 as the in-code default. It is a chart value so it can be lowered without a release, at the cost of a server restart.
- [ ] Confirm on the rig whether the `getEntities` query can bound the scan itself (for example a `closest` count) or only the amount serialised afterwards. If it cannot, the cap bounds log volume and browser load but not scan cost, and the self-throttle is the only bound on the latter; state that in `pack/README.md`.
- [ ] Measure the `MCMAP1` stdout volume at the cap (lines/s and bytes/s). All three dimensions at 1,000 mobs is on the order of 180 KB/s (~16 GB/day) before the self-throttle, and every line is also a container log line; check what log collection and retention do with it and record the answer.
- [ ] Wrap the whole sample in try/catch and emit a record naming the error rather than letting an exception kill the interval. A pack whose interval died silently is indistinguishable from a broken pipeline.
- [ ] Read-only by construction: no `runCommand`, no property writes, no entity mutation, no event subscriptions that can cancel anything. Comment that this is a guardrail, not a style choice.
- [ ] `pack/README.md`: the record format, the caps, the UUIDs, and the list of things the pack must never do.

**Collision:** none. All new files.

**Test:** no Go test harness runs JavaScript here, so this is a rig task. On the Task 1 rig with the FWB world copy: confirm one `MCMAP1` record set per second; confirm the longest record stays under 3,500 bytes across a full day's worth of samples (`awk` the captured log for max line length); confirm a synthetic 5,000-entity load (spawn a mob farm's worth of entities in a creative test world, not the FWB copy) trips the self-throttle instead of stalling the tick loop; capture `mc-monitor`/TPS before and after. Then a second run with exactly the cap, 1,000 mobs in each of the three dimensions (3,000 in total): record scan ms per sample, whether the throttle engages, and TPS before/after; remove the load and confirm the throttle steps back to 1 s. Each TPS comparison is over at least 30 minutes and is repeated with the pack absent, so the baseline is measured on the same rig rather than assumed.

**Evidence:** on the ticket — the max record size observed, the TPS before/after over 30 minutes at the FWB copy's own load and at the 3,000-mob cap load, the stdout volume, the self-throttle record from the overload run, and the recovery record after it.

---

## Task 6: mcmap — `internal/live` parse and store

- [ ] `record.go`: parse one bridge record into a typed frame. Strip **all** `\x00` bytes before `json.Unmarshal` (the >4 KB record that caused one is the previous line's problem, but the NUL lands on ours). Reject anything without the `MCMAP1` sentinel, an unknown `kind`, a dimension not in `render.Dimensions`, coordinates that are not finite, or a payload over a hard byte cap.
- [ ] `store.go`: per `(dimension, kind)`, buffer parts of the in-flight generation; apply atomically when `parts` of them have arrived; keep the last applied generation and its wall-clock time. `Snapshot(dimension, now)` returns the applied generation, or nothing at all once it is older than the TTL — that is what makes stale positions disappear.
- [ ] A generation number that goes backwards means the server (and so the pack) restarted: reset the buffers rather than waiting for a generation that will never come.
- [ ] `metrics.go`: `mcmap_live_frames_total{result}` (`applied|incomplete|dropped|unparseable|nul_stripped`), `mcmap_live_last_frame_timestamp_seconds`, `mcmap_live_entities{dimension,kind}`, `mcmap_live_frame_interval_seconds` (histogram), `mcmap_live_log_lag_seconds`, `mcmap_live_ingest_lag_seconds`, and from the heartbeat `mcmap_live_pack_scan_seconds` and `mcmap_live_pack_interval_seconds`, so production shows the pack's own cost and whether it is throttled.

**Collision:** none. New package.

**Test:** `TestParseStripsNulBytes`, `TestParseRejectsForeignScriptingLines`, `TestParseRejectsNonFiniteCoordinates`, `TestStoreAppliesOnlyCompleteGenerations`, `TestStoreKeepsLastCompleteGeneration`, `TestStoreExpiresAtTheTTL`, `TestStoreResetsOnAGenerationGoingBackwards`, `TestStoreIsSafeConcurrently` (`-race`).

---

## Task 7: mcmap — the source loop, the hub, and wiring

- [ ] `source.go`: a loop that long-polls the bridge's `GET /script` with the existing `BRIDGE_TOKEN`, feeding every record into the store. Backoff on error (1 s doubling to the refresh interval, like `worker.nextDelay`), `CheckRedirect` refusing redirects so the token never follows one, and a client timeout above the long-poll cap. On a `"gap":true` response, log that it fell behind and carry on.
- [ ] `hub.go`: per-dimension subscriber sets. On each applied generation, serialise that dimension's frame once and non-blocking-send to its subscribers; a full channel drops the frame for that subscriber and increments `mcmap_live_fanout_dropped_total`. A dropped frame is self-healing: the next one is a full state.
- [ ] `config.go`: `LIVE_ENABLED` (default `true`), `LIVE_POLL_WAIT` (default `2s`), `LIVE_TTL` (default `10s`), `LIVE_MAX_ENTITIES` (the fan-out's own cap per dimension and kind, default 1,000 to match the pack; whatever it drops is added to `more`. Lowering it cuts browser load after an mcmap restart alone, but cannot reduce the server's scan cost). Validate like every other value in that file: a present-but-invalid value is an error, not a silent fallback.
- [ ] `cmd/mcmap/main.go`: start the source loop in the same `wg`/`ctx` pattern the worker uses; hand the store and hub to the server. With `LIVE_ENABLED=false` nothing starts and `/api/live` is not registered.

**Collision:** `cmd/mcmap/main.go` and `internal/config/config.go` are both shared with 727 and 729. **Rebase onto 729 before opening this PR.** The source loop is a third goroutine alongside the worker, so the structural change is additive; the conflict is textual.

**Test:** `TestSourceFeedsTheStore` (httptest bridge), `TestSourceBacksOffAndRecovers`, `TestSourceRefusesRedirects`, `TestHubFansOutPerDimension`, `TestHubDropsForASlowSubscriberWithoutBlocking`, `TestLiveDisabledStartsNothing`. Config: a table case per new variable, valid and invalid.

---

## Task 8: mcmap — the SSE endpoint

- [ ] `internal/server/live.go`: `GET /api/live?dimension=<id>`, registered as `mux.Handle("GET /api/live", s.gated(s.handleLive))` so the session check is the same one `/api/map` and `/tiles/...` get. An unknown dimension is a 400.
- [ ] Headers: `Content-Type: text/event-stream`, `Cache-Control: no-store`, `Connection: keep-alive`, and **`X-Accel-Buffering: no`**. Measured on the gateway's own image and generated config (V1), small flushed events pass through at their original cadence with or without that header, so it is kept as a statement of intent that survives a future buffering policy, not as the thing that makes streaming work. The existing CSP (`default-src 'self'`) already permits a same-origin `EventSource`; no CSP change.
- [ ] Extend the write deadline before every frame via `http.NewResponseController` and `Flush()` after each. The public `http.Server` has `WriteTimeout: 30s` and `IdleTimeout: 60s`; without this the stream dies at 30 s.
- [ ] Send the current state immediately on connect, so a reload does not wait up to a second for the first frame.
- [ ] Heartbeat a `: keepalive` comment every 15 s when no frame has gone out, so the gateway's read timeout never reaps a quiet stream.
- [ ] Bound the stream at `min(5 min, time until this session expires)` and end it cleanly. `EventSource` reconnects by itself, and the reconnect re-runs `Sessions.Verify`, which consults `Revoked`. That is what makes `!map logout` stop a live stream rather than only the next page load.
- [ ] Each frame carries `at` (RFC3339 with milliseconds, the sample's bridge receive time), `serverNow`, `players`, `mobs`, `more`, and `stale`. `serverNow` lets the browser estimate clock offset for its age readout.
- [ ] `mcmap_live_subscribers` gauge, `mcmap_live_streams_total{reason}` for how streams ended, `mcmap_live_fanout_seconds` histogram.

**Collision:** `internal/server/server.go` gains one route and the `Server` struct two fields. Neither 727 nor 729 has a reason to touch that file; if one does, it is a two-line rebase.

**Test:** `TestLiveStreamRequiresSession` (401 without the cookie), `TestLiveStreamSendsCurrentStateImmediately`, `TestLiveStreamSendsFrames`, `TestLiveStreamHeartbeats`, `TestLiveStreamEndsAtSessionExpiry`, `TestLiveStreamRejectsAnUnknownDimension`, `TestLiveStreamSetsNoBufferingHeader`, `TestConfigStillSaysNothingAboutTheWorld` (the public `/api/config` response is unchanged).

---

## Task 9: The page

- [ ] `web/live.js`: open an `EventSource` on `api/live?dimension=<current>`; close and reopen on a dimension switch. Reconnect is the browser's job; on a 401 frame-less failure, fall back to `startLogin()` the way `load()` does.
- [ ] Draw on a dedicated `L.canvas()` renderer, not the default SVG path — at 1 Hz. The design target is up to ~3,000 markers across the three dimensions (1,000 mobs per dimension by default, Decision D3, plus players). A page subscribes to one dimension at a time, so a single view draws about a third of that, but the layer must hold the full figure without dropping below a smooth pan and zoom. Measure paint cost with the three-dimension fixture, not a handful of markers. Keep marker objects in a `Map` keyed by entity id and `setLatLng` them; create and remove only on arrival and departure. Do not switch the whole map to `preferCanvas`, which would change how the grid and future layers draw.
- [ ] Players: a distinct marker with the gamertag and a heading indicator from `r`. The visitor's own marker, matched by gamertag against `GET /api/me`, is highlighted.
- [ ] Mobs: small markers coloured by category, with a category table in `live.js` (hostile, passive, villager, other) defaulting to "other" for an unknown `typeId`, and a tooltip naming the type and any `nameTag`.
- [ ] Filter chips in `index.html`: a master "Live" toggle plus one per category, persisted in `localStorage`. Mob categories are on by default for every visitor (Decision D3). When `more > 0`, the footer says "showing 1,000 of 1,612 mobs".
- [ ] The footer carries the age readout — `live · 1.2 s` — computed from the frame's `at` against the browser clock corrected by the offset estimated from `serverNow` at stream open. This is both the feature and the instrument the <3 s recording reads.
- [ ] `web/embed.go`: add `live.js` to the `//go:embed` list.

**Collision:** `web/*` is untouched by 727 and 729. `embed.go` is a one-line change.

**Test:** `web/embed_test.go` extended so the new file is proven to be served. Manual, against a local build with a fake source: markers appear, move, survive a dimension switch, disappear at the TTL, and the filters hide and show categories. Repeat with a fake source at the design target (1,000 mobs in each dimension) and record frame time while panning and zooming. A browser check that the page still loads and the map still works with `LIVE_ENABLED=false`.

---

## Task 10: The chart

- [ ] `values.yaml`, under `map`: a `live` block (`enabled`, `pollWait`, `ttl`, `maxEntities: 1000`) and a `pack` block (`enabled`, plus the mcmap image reference the init step uses).
- [ ] `values.yaml`, under `minecraft-bedrock`: an `initContainers` entry running `["/mcmap","install-pack"]` from the pinned `minecraft-map` tag, with `DATA_DIR=/data` and `LEVEL_NAME=FWB`, a `volumeMounts` entry for `datadir` at `/data` **read-write**, and a `securityContext` with the uid/gid Task 1 recorded, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities: drop: [ALL]`.
  - The pack's mob cap is a literal `PACK_MOB_CAP: "1000"` env entry in that same block (it cannot come from `map.pack` because the block is not templated). It is the chart value for the cap: lowering it is a values change plus a server restart, with no image release. `map.live.maxEntities` is the browser-side counterpart and lowers with an mcmap restart only. Comment both.
  - `initContainers` is rendered through plain `toYaml`, **not** `tpl` (confirmed in the vendored subchart's `deployment.yaml`). So: no conditionals inside it, and a literal image reference, for the same reason the console-bridge sidecar's image is a literal. Comment that, and comment that nothing with a `secretKeyRef` may ever go in there — the 2026-09-06 incident is what that rule is made of.
  - Turning the pack off is "delete this block and restart", which is the kill switch. Document it in the chart README.
- [ ] `templates/map.yaml`: the new `LIVE_*` env vars, and one alert in the **existing** `minecraft-fwb-map` rule group:
  - `JdwillmsenMinecraftMapLiveStale`, warning, `(time() - mcmap_live_last_frame_timestamp_seconds{...}) > 120` for 5m — no player-count gate is needed, because the heartbeat makes the gauge fresh on an empty server.
  - `JdwillmsenMinecraftMapLiveNeverStarted`, warning, `absent(...)` with a `for`, mirroring the `NeverRendered` alert — a gauge that was never set is absent, not old, and the staleness expression would stay quiet through exactly the case of a live layer that never worked.
- [ ] `tools/tests/test-map.sh`: extend the exposure check so a `LIVE_*` variable cannot be the thing that publishes the internal port, assert the initContainer mounts only `datadir` and carries no `secretKeyRef`, assert the init image tag equals `map.image.tag`, and add promtool fire/no-fire cases for both new alerts.
- [ ] Chart README: the live layer, the latency budget, the kill switch, and the runbook line for a stale live layer (check `mcmap_live_frames_total` by result, then the bridge's `mc_console_bridge_script_records_total`, then the server log for `[Scripting]`).

**Collision:** heavy, and all with 727. `templates/map.yaml`, `values.yaml`, `README.md` and `tools/tests/test-map.sh` are all in 727's deliverables (chunk gauges, Loki rule, dashboard, promtool tests). **Do not open this PR until 727's chart PR is merged.** Add to the existing rule group; do not restructure it. If 727 also adds a Grafana dashboard, add live panels to it rather than shipping a second dashboard.

**Test:** `bash tools/tests/test-map.sh` green with `promtool` on `PATH`; `helm template` with all three production value files; `helm lint`.

---

## Task 11: Guardrail run against a copy of the FWB world

This is the ticket's own guardrail and it gates production.

- [ ] On the Task 1 rig, with a fresh copy of the FWB world from the latest nightly archive: run `mcmap install-pack`, start the server, let it run 30 minutes with a client connected and moving.
- [ ] Stop the server cleanly and read `level.dat`: `experiments_ever_used`, `saved_with_toggled_experiments`, `cheatsEnabled`. **All three must match what Task 1 recorded.**
- [ ] Record TPS for the 30 minutes before the pack is installed and the 30 minutes after, at the FWB copy's own load, and repeat the Task 5 cap-load run (1,000 mobs in each of three dimensions) against the installed pack with the real installer. Rig TPS is indicative only; the production comparison is Task 12. A result that already shows a drop past the threshold from open question Q2 stops the rollout here.
- [ ] Confirm the server log reports the pack in its pack stack (today it says "Pack Stack - None") and reports no content errors.
- [ ] Start the server a second time with a deliberately broken installer (an unwritable `/data`): the server must still come up, and the installer must log and exit 0.
- [ ] Run the full chain on the rig: bridge 0.6.0 → mcmap with `LIVE_ENABLED=true` → a browser on a port-forward. Walk in game; watch the marker move.

**Collision:** none.

**Evidence:** on the ticket — the before/after `level.dat` flag values, the before/after TPS at both loads, the pack-stack log line, the broken-installer run, and a screen recording of the rig end-to-end.

---

## Task 12: Production rollout and the <3 s measurement

- [ ] Release the bridge (Task 3) and mcmap (Tasks 4, 6, 7, 8, 9) and roll the chart (Task 10). Order: bridge first and on its own, so the new endpoint exists before anything asks for it; then mcmap; then the chart change that adds the initContainer, which restarts the game server.
- [ ] Note the server restart in the rollout: the initContainer only runs on a pod recreation, so the pack does not take effect until the server is restarted. Announce it the way the chart's deploy-announce hook already does.
- [ ] Watch `mc_agent_server_tps` for 24 h against the same hours of the week before (TPS follows the player count, so a day-level average hides the comparison), and read the pack's own `mcmap_live_pack_scan_seconds` and `mcmap_live_pack_interval_seconds` alongside it. The cap is 1,000 mobs per dimension, 2.5x the first draft, on a server already at 12–15 TPS, so check hourly for the first 6 h rather than once at the end. A drop past the threshold fixed in open question Q2, which must be settled before the rollout, is a rollback, not a thing to tune in place.
- [ ] Record the latency, three ways:
  1. **Objective hops, from metrics:** `histogram_quantile(0.95, mcmap_live_log_lag_seconds)`, `..._ingest_lag_seconds`, `..._fanout_seconds`, `..._frame_interval_seconds` over 24 h.
  2. **The recorded end-to-end move:** one screen capture showing the Minecraft client, the map page with its age readout, and `kubectl logs -f` on the mcmap pod. 20 discrete moves; report the distribution and the p95. This is the "recorded" artefact the DoD asks for.
  3. **Stale positions disappear:** stop the pack (scale the server pod down, or `LIVE_ENABLED=false` on the map) and record the markers vanishing within the TTL.
- [ ] Confirm the exposure boundary on the public hostname, the way 694's comment did: `GET /api/live` without a cookie returns 401; `GET /api/config` still returns only `{"login":true}`; the internal port is still not routed.
- [ ] Confirm `level.dat` in production after rollout: flags unchanged.

**Collision:** the restart is a shared resource. Coordinate with whoever is mid-rollout for 727/728/730 so the pack's restart and theirs are not two restarts of the world in an hour.

**Evidence:** a closing comment on JDWLABS-695 with the three latency measurements, the TPS comparison (hour by hour, with the pack's scan and interval series), the production `level.dat` flags, and the exposure check output.

---

## Task 13: Documentation

- [ ] `minecraft/mcmap/README.md`: a "Live layer" section (where the records come from, the format, the caps, the TTL), the new `LIVE_*` rows in the environment table, `GET /api/live` in the endpoint table, the new metrics in the metrics table, and a note that the pack ships inside this image and is installed by `mcmap install-pack`.
- [ ] `minecraft/bridge/README.md`: `GET /script` and its metrics, and the paragraph on why it is not `/events`.
- [ ] `minecraft/mcmap/pack/README.md`: the record format, the UUIDs, and the must-nevers.
- [ ] Chart README: the live layer, the kill switch, the runbook entry.
- [ ] Amend the umbrella spec in two places. (1) Its "SSE of stdout (exists)" line, or a short note under it, so the next reader is not told the bridge already streams stdout. (2) Its `minecraft/mcmap-pack` component design (`kind: pack`, `release.artifacts: [github-asset]`): replace it with the embedded form, stating that the pack ships inside the mcmap image, is installed by `mcmap install-pack`, and has no component or release path of its own (Decision D1).

**Collision:** `minecraft/mcmap/README.md` is shared with 727 (metrics table) and 729 (env table, generations). Land last, after both.

---

## Decisions

Recorded from the human's answers; the task bodies above already reflect them.

- **D1. The pack is embedded in the mcmap image via `go:embed`; the umbrella spec is amended.** Why: no new component, no release glue (`release.yml` has no `github-asset` implementation), no new toolchain, and it matches the ticket's "installed into the world by an init step from the mcmap image". Task 13 amends both the spec's `minecraft/mcmap-pack` component design and its "SSE of stdout" line.
- **D2. The pack is installed by an initContainer on the game server pod, and the installer fails open.** Why: it is the only writer that can reach the world's PVC before BDS opens it, and the subchart's `initContainers` value makes it a one-block change. The decision rests on the installer never blocking the server (the 2026-09-06 outage came from exactly that class of coupling): every failure exits 0 with a logged reason and the staleness alert is the safety net. The fail-open behaviour and its tests (Task 4, Task 11) are release-blocking requirements, not optional hardening.
- **D3. The mob cap defaults to 1,000 per dimension, and mob markers are on by default for every visitor.** Why: the AFK bots' mob farm can hold far more than 400 mobs in loaded chunks, and the map is meant to show it rather than a truncated sample. The cap is a chart value (`PACK_MOB_CAP` for the pack, `map.live.maxEntities` for mcmap) so it can be lowered without a release. Cost: 2.5x the first draft's pack tick cost and browser marker count on a server already at 12–15 TPS, which is why the self-throttle (Task 5), the before/after TPS measurements (Tasks 5, 11, 12) and open question Q2 are load-bearing.
- **D4. Every logged-in FWB player sees every player's live position, with no opt-out.** Why: it is the epic's access model, "anyone who plays on FWB", and needs no agent change in this ticket. If revisited: an opt-out would need an in-game agent command and so would queue behind JDWLABS-728.

## Verified

- **V1. Server-Sent Events pass through the ingress path unbuffered; the only requirement is traffic at least every 30 s.** Measured 2026-10-05, nothing changed in production.
  - *Path:* browser → HAProxy VIP (TCP mode, TLS passed through) → `platform-gateway` (nginx-gateway-fabric 2.7.2, nginx 1.31.6, HTTP/2 to the client) → mcmap. The public hostname answers HTTP/2 from nginx with the wildcard certificate, which is what TCP passthrough looks like from outside.
  - *Gateway config, read from a live data-plane pod (`nginx -T`):* no `proxy_buffering`, `proxy_read_timeout`, `proxy_ignore_headers` or `gzip` directive anywhere, so nginx defaults apply, and no ProxySettingsPolicy, ClientSettingsPolicy, UpstreamSettingsPolicy or snippets object exists in the cluster.
  - *Buffering, measured on the same data-plane image with the map's generated location block and an upstream emitting one small event per second:* every event arrived within 10 ms of its send time, over HTTP/1.1 and HTTP/2, with and without `X-Accel-Buffering: no`, and for `text/plain` as well as `text/event-stream`. The header is not what makes it work here.
  - *Idle limits, measured:* nginx cut a silent stream at 60.06 s (`upstream timed out`). HAProxy with the load balancer's own defaults cut a silent stream at 30.0 s, carried a 1 Hz stream for its full 45 s, and carried a stream with nothing but a 15 s keepalive comment for its full 60 s.
  - *Consequences:* the plain-JSON polling fallback is not needed. The 15 s heartbeat is required, and the limit it answers to is HAProxy's 30 s, not nginx's 60 s. The heartbeat interval must never be raised to 30 s or beyond.
  - *Not covered:* the HAProxy test ran the current LTS image (3.4.6) against the template's defaults, not the version on the load balancer host, and nothing was streamed through the production path itself, since that would need a route that is not in git. The first end-to-end confirmation is Task 12's recorded move.

## Open questions needing a human decision

Q1. **Does the gateway pass SSE through unbuffered?** Resolved 2026-10-05 by measurement, no decision needed: it does, and no gateway policy or platform change is required. See V1. The number is kept so references to Q2–Q4 stay valid.

Q2. **What TPS drop triggers a rollback, decided before the rollout?** This is more load-bearing since D3 raised the default cap to 1,000 mobs per dimension: a 1 Hz `getEntities()` across three dimensions at 2.5x the first draft's volume is a real cost on a server that already averages 12–15 TPS with its own alert tuned around that, and the self-throttle is a mitigation, not a guarantee. Task 12 treats a drop past this threshold as a rollback and Task 11 uses it as a stop condition, so both are undefined until it is set. **Decision needed:** the threshold as a number and a window (for example, X TPS below the same hours of the prior week, sustained for Y minutes), who makes the call, and whether lowering the cap counts as a response short of rollback.

Q3. **Does the restart that activates the pack get its own window?** The pack only takes effect on a server restart, and 727, 728 and 730 all have rollouts of their own in the same period. **Decision needed:** whether 695's restart rides along with one of theirs, or gets announced separately.

Q4. **Does the bridge need a second long-poll slot?** `GET /script` is a long-lived request on a sidecar whose `http.Server` has no connection cap. Today only mcmap calls it. **Decision needed:** whether to bound concurrent `/script` waiters (and refuse past the bound), or accept that the bearer token is the only control, as it is for every other bridge endpoint.
