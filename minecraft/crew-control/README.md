# crew-control

Public console for the Crew worker units, built as an operations console: a
roster, an adaptive wall that runs a limited number of live cameras, a fleet
event feed with toasts, a status bar, picture-in-picture, a command palette
(Ctrl-K or `:`, `?` lists the keys) and, for operators, a radar and bulk stop.
Anyone can watch: it reverse-proxies
an allow-list of each unit's camera routes and never forwards anything else.
Operators who sign in with a passkey can also queue tasks, stop a unit or send
it to a point, and see each unit's queue and history. Shift-J opens their Jobs
panel, with a Places tab; plain `j` and `k` step through units. Units pull that work from
crew-control; it never calls them to command them.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `CREW_UNITS` | required | JSON list `[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`, 1 to 26 units; an optional `"player"` per unit names its id in the world save when the unit has not reported its own (see [Unit detail](#unit-detail)) |
| `LISTEN_ADDR` | `0.0.0.0:8080` | Listen address |
| `LOG_LEVEL` | `info` | Log level |
| `PUBLIC_ORIGIN` | unset | `https://host`, lower case, no port or path; its host is the WebAuthn RP ID. Unset, or no operators, turns sign-in off |
| `CREW_OPERATORS` | unset | JSON list `[{"id":"owner","name":"Owner"}]`, at most 8; ids are slugs `^[a-z][a-z0-9-]{1,15}$` |
| `CREW_INVITES_DIR` | unset | Mounted Secret directory with one file per operator id holding that operator's one-time invite code; missing or empty means nobody can enrol |
| `DATA_DIR` | `/data` | Persistent volume for `session.key`, `passkeys.json` and `invites-used.json` |
| `WORK_TOKEN` | unset | Shared bearer token for the unit work routes; unset leaves them out entirely (404) |
| `PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USERNAME`, `PG_PASSWORD` | unset, `5432` | Postgres holding the queue (`minecraft.crew_work`, `minecraft.crew_work_event`), the world ledger (`minecraft.crew_place`, `minecraft.crew_stock`), jobs (`minecraft.crew_job`, `minecraft.crew_job_step`) and the unit event history (`minecraft.crew_event`), over `sslmode=require` |
| `WORK_STORE` | unset | `memory` keeps the queue, ledger and jobs in memory when `PG_HOST` is unset; development only, it is lost on restart |
| `CREW_PLACES` | unset | JSON list of named chests and areas, `[{"id","name","kind","box":{"min":{x,y,z},"max":{x,y,z}},"items":[]}]`; seeded at start, replacing earlier seeded places but never ones added by hand. `[]` removes every seeded place; unset or blank seeds nothing |
| `CREW_WORLD_DB` | unset | The lab world's `db` directory (the LevelDB inside the world folder), read-only behind `GET /units/{id}/detail`. Unset, the route still answers and says the save is `off` |
| `CREW_WORLD_WORK` | `/tmp/crew-worldview` | Where a read of the world builds its view; writable. On the world's own volume a read links its files, elsewhere (or if the world is mounted read-only) it copies them |
| `CREW_WORLD_EVERY` | `5s` | Least time between reads of the world save; the detail answer is cached to match |

## Routes

- `GET /`: the console, one focused unit, a wall of every unit or the fleet overview (`?view=fleet`); `GET /static/{file}` its assets; `GET /camera` redirects to `/`
- `GET /v1/units/stream`: server-sent `units` events from a once-a-second poll of each unit's `/v1/state`, sent on connect, on change and every 15 s; at most 64 clients, then 503. Also an `events` backlog on connect and a `log` event per new fleet event (death, offline, low health or hunger, task started, done, failed, ended with an unknown outcome, stuck); the `events` backlog replaces the console feed on each connect; the last 200 are kept in memory. Signed-in streams also get `positions`, every 2 s when something moved, and a `jobs` event with the newest 20 jobs whenever one changes
- `GET /units/{id}/camera`, `/units/{id}/v1/state`
- `GET /units/{id}/v1/camera/{file}` for `ws`, `mesher.js`, `entities.{js,png,json}`, `atlas.{png,json}`, `items.{png,json}`, `stats`
- `GET /units/{id}/detail`: the unit's inventory, armour, experience and all 27 ender chest slots from the world save, merged with its live polled state. Signed-in operators only, `Cache-Control: private`; see [Unit detail](#unit-detail)
- `GET /healthz`
- `GET /signin`: passkey sign-in; `/signin#operator=<id>&invite=<code>` enrols a passkey. The invite rides in the fragment so it never reaches a log
- `POST /auth/register/options`, `/auth/register/finish`, `/auth/login/options`, `/auth/login/finish`, `/auth/logout`, `/auth/report`; `GET /auth/me`. Only with sign-in on
- `GET /v1/work/{unit}`: the unit's last 50 items, newest first
- `POST /v1/work/{unit}/tasks` (a `gather_and_deposit`, `haul` or `goto` template, as in the crew Task API), `/goto` (`{"x","y","z"}`, x and z −500..499, y −64..320), `/stop`, `/items/{id}/cancel`
- `POST /v1/jobs`: `{"kind":"plan","template":"parallel-demo"}` or `{"kind":"stock","place":"<id>","want":{"cobblestone":16}}`, 202 with the job, 422 when it cannot be planned; `GET /v1/jobs` (the last 50), `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` (409 while the scheduler holds it)
- `GET /v1/places`: every place with its stock; `POST /v1/places` (`{"id","name","kind","box"}`, as in `CREW_PLACES`) adds or edits one, `POST /v1/places/delete` (`{"id"}`) removes one; seeded places answer 409. Chests units report get `chest-x-y-z` ids, which a request may only rename, never move or create. Moving a place or changing its kind, by request or by `CREW_PLACES`, drops its stock; a rename keeps it
- `GET /v1/events?unit=&kind=&severity=&since=&until=&limit=&sort=asc|desc&cursor=<next>`: signed-in; the unit event history, newest first by default, every filter optional and exact, times RFC3339. Pages are keyset (`next` carries an `(at, id)` cursor, never an offset), at most 200 events each. Severity is ok, info, warn or crit; kinds are listed under Event history. Rows carry each unit's position, which is why the route is signed-in only
- `POST /v1/events/prune` (`{"dry_run":true}` counts what would go and lists the oldest 100, `{}` deletes it): signed-in; the 30-day retention, answering `{"pruned","total","would"}`
- `GET /v1/units/{id}/work?wait=25s[&kinds=command]`, `POST /v1/units/{id}/events`: unit-facing, `Authorization: Bearer <WORK_TOKEN>`, cluster-only
- `GET /v1/squads` and `/v1/squads/{id}`: the squads, rebuilt from units' hellos, their members' last status reports and who leads by the chain of command (Vanguard, then Striker, then Ranger; a leader reported dead or silent for over 8 s is replaced), plus any battalions the squads nest in, data only; the console-safe shape the stream serves — no member positions and no operator id; anonymous and read-only
- `GET /v1/squads/stream`: server-sent `squads` snapshots for the console panel, sent on connect, on change and at least every 15 s. The snapshot carries members, the standing order and the last 12 callouts, but no positions and no operator id; anonymous and read-only
- `POST /v1/squads/{id}/hello` with `{"unit","squad_name"?,"role","battalion"?,"battalion_name"?}`, `/status` with `{"unit","hp","food","weapon","pos"}`, `/callout` with `{"unit","kind"}` from `low_health|need_heal|target_down|boss_phase`: unit-facing with the `WORK_TOKEN`, cluster-only; state is in memory only. Battalion, battalion name and weapon are at most 32 printable characters — they show on the public stream
- `GET /v1/squads/{id}/orders?since=<seq>&wait=25s[&rev=<rev>]`: the standing order, the leader-line revision (`rev`), deltas since the cursor (`focus|hold|advance|regroup|retreat`, call-outs with their sender) and the leader's position; a cursor past the squad's seq (a unit that outlived a restart) is served as a full resync, a `rev` older than the answer's is served at once; long-polls, 204 when nothing changed, same auth as the work routes
- `POST /v1/squads/{id}/order` with `{"kind":"focus|hold|advance|regroup|retreat","target"?}`: operator session, as the other command routes

Any other path, method, unknown unit or unclean path under `/units/` gets 404
without an upstream request. Only the `r` query (1 to 3 digits) and the `wire` query (1 or 2
digits, the camera format the page asks for) are forwarded, and only on `ws`.
Cookies, `Authorization` and client `X-Forwarded-*` headers are dropped.

## Unit detail

`GET /units/{id}/detail` answers one unit's operators with what the world
save holds of it — armour, experience, and the ender chest, none of which a
unit reports live — merged with what the once-a-second poll of its
`/v1/state` heard. The ender chest is private to its owner, so unlike the
camera and state routes above it this one is **not** on the public
allow-list: it answers 401 without an operator session and is marked
`Cache-Control: private`, so no share or cache may ever hold it. The public
gateway route passes everything under `/units` to crew-control, so that 401
is the guard, not an absence from the gateway.

A unit's record is found by the id the unit reports for its player in its
`/v1/state` (`unique_id`, a signed decimal: the id the world save files the
record under and the map's live feed names the unit by). A `player` field in
`CREW_UNITS` names the id by hand instead and wins; it is for a unit that has
not been online since crew-control started. With neither, the unit's `save`
is `none`, and the whole route says `off` when `CREW_WORLD_DB` is unset: the
live half still answers either way.

The body says where each half came from — `save` and `live`, each `known`,
`none`, `pending` or `off` with an `asOf` time (when the save was *read*;
the record carries no time of its own) — and then the merged view:

- `health`, `hunger` and `inventory` come from the live poll while the unit
  is online, since a poll is an observation of now and the record is as old
  as the server's last save; the live inventory keeps what the save knows
  about a stack it still holds in the same slot (durability, a custom name,
  enchantments, and what a shulker box carries). Offline, the save's own are
  shown.
- `effects` are live only — the save holds none of them.
- `xp`, `armor`, `mainhand`, `offhand`, `gameMode` and `enderChest` are the
  save's. `enderChest` is all 27 slots in slot order, `null` where a slot is
  empty; each item carries `name`, `count`, `slot` and, where it has them,
  `damage`, `customName` and `enchantments`. The answer stays under 50 KB:
  past the cap it drops what shulker boxes carry.
- The save is read at most once every `CREW_WORLD_EVERY`, a failed read
  included, and held between reads, so a detail view costs the world one
  read and not one a request. A read happens only when a request asks.

### Reaching the lab world

The lab world is the Bedrock StatefulSet's `/data/worlds/crew-lab/db`, on a
2 Gi `truenas-iscsi` ReadWriteOnce volume (`datadir-jdwillmsen-crew-lab-prd-minecraft-bedrock-0`)
the pod is pinned to one node for. crew-control's own volume is a small
`truenas-nfs` one for its keys, and units reach the lab over the network, so
none of them can see the save today. The lab has no console-bridge sidecar,
which is how the map gets the main world's snapshot. A unit cannot answer
for the chest either: the server sends the ender chest's contents only to a
client that opens it. The code therefore reads the save from a path behind
the `detail.Source` interface, and what is missing is deployment wiring:

1. crew-control mounts that Bedrock volume **read-only** and points
   `CREW_WORLD_DB` at `…/worlds/crew-lab/db`. A ReadWriteOnce volume may be
   mounted by several pods of one node, so crew-control must be scheduled
   onto the Bedrock pod's node (a `podAffinity` on its pod, or the same node
   affinity); it is not pinned today. The db files are owned by uid 1000 and
   world-readable, and crew-control runs as uid 1000.
2. `CREW_WORLD_WORK` needs a writable scratch, as the container's root is
   read-only: an `emptyDir`, not the NFS data volume, which keeps the many
   small writes of a read off NFS. A read-only mount cannot take hard links,
   so each read copies the db (26 MB at the time of writing) into the
   scratch and removes it again; `CREW_WORLD_EVERY` bounds how often.
3. Optionally `player` ids in `CREW_UNITS`; not needed once the units run a
   release that reports `unique_id`.

Mounting the volume read-only keeps the world exactly as the server wrote
it. A read is of the live files, not a save-held snapshot, so it can catch a
table the server is compacting away; such a read fails, is logged once, and
the last good records are served until the next. A record that does not
decode is skipped, as the map's reader skips it. The alternative is a
console-bridge sidecar on the lab pod, as the main world has; it gives a
consistent snapshot at the cost of a new component.

## Operators

A session is the `__Host-crew_session` cookie, signed with `session.key` and
valid for 12 hours. Every request re-checks that the operator is still in
`CREW_OPERATORS` and its passkey is still in `passkeys.json`, so removing
either signs it out. An operator holds at most 5 passkeys; remove one by
deleting its entry from `passkeys.json`. If that file stops parsing, everyone
is signed out and enrolment is refused until it parses again; it is never
overwritten. An invite works once: a new device needs a new code in the
invites Secret.

Sign-in logs one JSON line per step, never a code, challenge, credential id
or cookie: `ceremony_started` (`kind` `login` or `register`, plus `operator`
for a register) when a begin request is answered, then `operator_login` or
`operator_enrolled`, or a refusal (`login_refused` with a `reason`,
`invite_refused`, `enrol_failed`, `login_failed`). A failure that never
reaches the server, such as the browser refusing the passkey prompt, is sent
by the page to `POST /auth/report` and logged as `client_ceremony_failed`
with `kind`, `stage` (`begin`, `credential`, `finish`) and `error` (a
`DOMException` name, `network`, `refused` or `other`). The route needs no
session, takes only those words, and answers 429 past 20 reports a minute.

The `/v1/work/*`, `/v1/jobs` and `/v1/places` routes answer 401 without a session, as does
`GET /units/{id}/detail` (the ender chest it carries is private to its owner). Every `POST` needs a JSON
body and `X-Crew-Request: 1`. Stop cancels everything open for the unit and
reaches it before any queued task; cancelling an item the unit already has
sends it a `cancel_task` command and answers 202, leaving its other work alone. Signed-in
streams also get a `work` event per unit; anonymous streams never do. Coordinates follow the same rule: only signed-in
streams get `positions`, so the radar exists only for operators.

## Units and the queue

Each unit with `CONTROL_URL` and `CONTROL_TOKEN` set long-polls
`/v1/units/{id}/work` for one item at a time and posts its progress to
`/events`. An item silent for more than 30 s is handed out again, and units
treat a repeat as a duplicate. Those routes are refused on any request with
`X-Forwarded-For`, so only cluster traffic reaches them. Rows older than 30
days are pruned at start and daily. With Postgres unset or down, the work
routes answer 503 and the view-only console keeps working.

## Fleet overview

`?view=fleet` (the **Fleet** button, or `o`) is a table of every unit for the signed-out public view and for operators alike, fed only by the `/v1/units/stream` events the console already receives. Each row shows state, time in that state, health and hunger bars, the task, and a three-minute health trail; a strip of stat tiles counts online, working, idle, stuck, dead and offline units and the tasks done and failed in the live feed window (the last 200 events the stream holds, so those two are a window, not a total). Signed-in streams add a position column on wide screens. A state never rides on colour alone: each has its own shape and word.

- **States.** `working` and `idle` come from the unit's task; `dead` and `offline` from its status. `stuck` is a working unit whose current task the feed named stuck (no progress for 90 s); it clears when the task or its progress changes, or 10 minutes after the report (`STUCK_HOLD_MS`), because the feed carries no "unstuck" event and the unit list no stuck flag.
- **Time in state** is exact when the console saw the change or the feed backlog dates it; otherwise it is a floor, shown as `>=` the time since the page opened. Leaving `stuck` restarts the clock. The trail is a client-side window of one sample per 2 s, since the stream carries no history.
- **Sort and filter** by state, name, health, hunger, task or time in state (unknown values always last); filter by text, state, task kind and health or hunger at most 6, 10 or 15. Both live in the address (`sort`, `dir`, `q`, `st`, `task`, `hp`, `food`) so a view can be linked.
- **Keys.** `/` filters; arrows, `j`/`k`, Home, End and PageUp/Down move between rows; Enter or a click focuses the unit's camera; `a` jumps to the next unit that needs a look; Esc clears the filter.
- **Updates.** Rows are built once and patched in place every 2 s, with fixed column and row sizes so nothing shifts. A sort by a changing value re-orders rows, but not while the pointer is over the table. On a phone each unit is a two-line card and the camera modes, event feed and status bar are hidden to leave room.

Logic lives in `overview.js` (node-tested in `overview.test.mjs`), the DOM in `overview-ui.js`, styles in `overview.css`; the chart library supplies the stat tiles and sparklines.

## Chart library

`internal/console/web/charts/` holds the console's chart panels: plain ES modules and SVG, no bundler and no dependency, embedded and served from `/static/charts/{file}` like the rest of the console. `/static/charts/demo.html` is the gallery: every chart on seeded synthetic data, with theme (`?theme=light|dark`), texture (`?texture=1`) and a live 20-chart run (`?live=1`). It doubles as the visual regression fixture; `?eager=1` draws off-screen panels too, for full-page screenshots.

```html
<link rel="stylesheet" href="/static/charts/charts.css">
<script type="module">
import { LineChart, BarChart, StatTile, Heatmap, Gauge, createTimeRange, mountRangeFilter } from '/static/charts/index.js';
const range = createTimeRange();                 // one range scopes every chart handed it
range.setDomain(from, to);
mountRangeFilter(filterRow, range);              // the single filter row above the charts
const chart = new LineChart(host, { title: 'Latency', range, yFormat: v => `${v} ms` });
chart.update({ x: [ms, ...], series: [{ key: 'camera', name: 'Camera', values: [12, null, 14] }] });
</script>
```

| Component | Data | Notes |
| --- | --- | --- |
| `LineChart` | `{ x: [ms], series: [{ key, name, values }] }` | `area`, `stacked`, `zero`, `y: {min,max}`, `tz: 'UTC' \| 'local'`; crosshair and one tooltip for every series; drag to brush a range, double-click or Esc to reset; `Shift`+arrows then `Enter` brushes from the keyboard |
| `BarChart` | `{ categories: [name], series: [...] }` | one series, grouped or `stacked` (non-negative); `horizontal` (`height: 'auto'` sizes to the rows); hover lifts the mark and the tooltip lists every series |
| `Sparkline`, `StatTile` | `{ values, labels }`; tile: `{ label, value, delta: {value, label, goodWhenUp}, trend, status }` | trend line in neutral ink with the current period in the accent; delta carries an arrow and a sign; a sparkline also takes `height`, `pad`, `domain: [lo, hi]` to pin the range and `table: false` to skip its screen-reader table |
| `Heatmap` | `{ rows, cols, values: [[...]] }` | one-hue sequential, or `diverging: true` around zero; scale legend underneath |
| `Gauge` | `{ value }` | `min`, `max`, `warn`, `crit`, `invert`; fill goes accent, warning, critical, each with an icon and a word |

Every panel has a table view (the **Table** button) and, for two or more series, a legend that toggles series. Arrow keys move through points, bars and cells, and announce the reading in a live region. Series and category names reach the DOM only through `textContent`.

Colour comes from one token set in `charts.css` (`--cc-*` custom properties on `.cc-root`). Dark follows the OS unless an ancestor sets `data-theme="light"`; `data-theme="dark"` forces it, and `setTheme(el, 'auto' | 'light' | 'dark')` does either. `setTexture(el, true)`, print and forced-colours switch filled marks to a 45°/135° hatch so hue is not the only identity channel. The console itself is dark only, so put `data-theme="dark"` on its root when it adopts these panels.

- **Categorical order is fixed.** A series keeps the slot it was first seen in (`createRegistry()`), so filtering never repaints the survivors. Past eight, the tail folds into one grey "Other (n)" series (`otherCombine: 'sum' | 'mean' | 'max'`); a ninth hue is never generated. Pass a registry per dashboard.
- **The palette is validated, not eyeballed.** `charts/palette-validation.txt` has the dataviz validator's output for both surfaces; `palette.test.mjs` fails when `charts.css` and `palette.js` drift apart. Change a hue only after re-running the validator.
- **Refresh costs.** Panels redraw through one frame-budgeted queue and skip drawing while off screen. `update()` is cheap to call at 1 Hz per chart; `setLoading(true)` dims a panel while it refetches instead of blanking it.

`node charts/capture.mjs <demo url> <out dir> [--perf=30]` (Playwright; set `PLAYWRIGHT` to its entry module if it is not installed beside the script) writes light and dark screenshots of every section, hover, focus, brush and table states, and the frame-time run. `node --test web/charts/*.test.mjs` covers the scale, geometry, series folding and palette code; the Go test checks the files are embedded and served.

## Event history

`minecraft.crew_event` is the durable fleet event log: one typed row per event
with the unit, UTC timestamp (`at`), kind, severity, the task and job it names
when there is one, the unit's position when known (`x`, `y`, `z`, null when not)
and a JSON payload. Indexes `(unit, at, id)`, `(kind, at, id)`,
`(severity, at, id)` and `(at, id)` each serve one console query shape, and
the trailing `(at, id)` is the keyset pair, so paging and ordering are one
index walk in either direction with no sort. `kind` is not constrained in the
schema, so a new one is code in the producer and no migration. The SQL is
`migrations/V11__minecraft_crew_events.sql`; crew-control does not apply it
(see `migrations/README.md`). The history lives only in Postgres, nothing is
written under `DATA_DIR`, and with Postgres down events are dropped, not
queued: a gap is honest, a backlog that slows the live feed is not.

**Where events come from.** The poller derives them from each unit's status
poll every second, the same events as the live feed, and the writer stores them
in batches of up to 64 (one insert, at least once a second). Today that is
`online`, `offline`, `died`, `respawned`, `task_started`, `task_done`,
`task_failed`, `task_cancelled`, `task_ended` (outcome unknown), `stuck`,
`low_health`, `low_hunger`. A task event's payload has the feed's `text`, the
task's `template` and, once finished, `duration_ms`. Task ids that are not
queue UUIDs (units run their own rotation) go to `payload.task`, not `task_id`.

Not captured yet, because the data does not reach crew-control: a closed
failure reason code (a unit's reason is free text, in `text`), `kit_failed`,
`session_error` and other log-only events, and session start and end. Units do
not post events today, so there is no ingest route; units would have to post
them for those kinds, and that is follow-up work.

**Idle roll-up.** A hauling unit re-checks its chests every couple of seconds
and finishes in a fraction of one, which in one measured hour was 78% of all
task events. Storing each would make the table about 4.6 times larger and every
throughput chart a chart of no-ops, so they are collapsed per unit:

- A finished task is idle when its kind is `task_done`, its template is in the
  idle list (only `haul`) and its duration is known and at most 2 s. A failed,
  cancelled or unknown-outcome task, a slow haul, a haul of unknown duration
  and every other template keep their own rows.
- Idle polls from one unit and template become one `idle_rollup` row: `at` is
  the first poll, the payload has `template`, `count`, `last` and `text`. A
  roll-up closes on the unit's next real event (so it stays in order), when it
  is a minute old, or at shutdown. Only a crash loses an open count, at most a
  minute of it.
- A `task_started` of an idle-eligible template is held until its finish is
  known, then dropped with it if the poll was idle, or stored unchanged if not
  (also stored after 5 s, or when the unit's next task starts, if no finish
  came).

**Retention.** History is kept for 30 days, pruned at start and daily with the
work queue, in chunks of 5,000 rows so no statement holds the whole backlog.
`POST /v1/events/prune` with `{"dry_run":true}` counts the rows a pass would
delete and lists the oldest 100, without deleting anything.

The Postgres tests run when `CREW_PG_TEST_DSN` is set (they apply the migration
first and truncate `crew_event`, so point them at a scratch database) and skip
otherwise; everything else runs without a database.
