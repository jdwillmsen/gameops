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
| `XBOX_LOGIN_ENABLED` | `false` | `true` offers Sign in with Xbox beside the passkey; needs the four rows below. Off, the Xbox routes are 404 and `/auth/methods` says so |
| `XBOX_CLIENT_ID` | unset | Application (client) ID, a GUID, of the Entra app registration |
| `XBOX_CLIENT_SECRET_FILE` | unset | Path of the mounted Secret file holding the app's client secret (`XBOX_CLIENT_SECRET` holds it directly for a local run; not both). At least 16 characters, no whitespace |
| `CREW_XBOX_ACCOUNTS` | unset | JSON list `[{"operator":"owner","xuid":"2535..."}]`, at most 16: which Xbox account (by XUID) is which operator. Every operator id must be in `CREW_OPERATORS`; an empty list with Xbox on stops the start |
| `DATA_DIR` | `/data` | Persistent volume for `session.key`, `passkeys.json` and `invites-used.json` |
| `WORK_TOKEN` | unset | Shared bearer token for the unit work routes; unset leaves them out entirely (404) |
| `PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USERNAME`, `PG_PASSWORD` | unset, `5432` | Postgres holding the queue (`minecraft.crew_work`, `minecraft.crew_work_event`), the world ledger (`minecraft.crew_place`, `minecraft.crew_stock`), jobs (`minecraft.crew_job`, `minecraft.crew_job_step`) and the unit event history (`minecraft.crew_event`), over `sslmode=require` |
| `WORK_STORE` | unset | `memory` keeps the queue, ledger and jobs in memory when `PG_HOST` is unset; development only, it is lost on restart |
| `CREW_PLACES` | unset | JSON list of named chests and areas, `[{"id","name","kind","box":{"min":{x,y,z},"max":{x,y,z}},"items":[]}]`; seeded at start, replacing earlier seeded places but never ones added by hand. `[]` removes every seeded place; unset or blank seeds nothing |
| `CREW_WORLD_DB` | unset | The lab world's `db` directory (the LevelDB inside the world folder), read-only behind `GET /units/{id}/detail`. Unset, the route still answers and says the save is `off` |
| `CREW_WORLD_WORK` | `/tmp/crew-worldview` | Where a read of the world builds its view; writable. On the world's own volume a read links its files, elsewhere (or if the world is mounted read-only) it copies them |
| `CREW_ALERTS` | unset | JSON object of alert thresholds, see [Alerts](#alerts); unset keeps the defaults, an unknown key or bad value stops the start |
| `HEAT_SAMPLE` | `2s` | How often each unit's position is sampled into the heatmap; under `1s` or unparseable falls back to the default |
| `CREW_WORLD_EVERY` | `5s` | Least time between reads of the world save; the detail answer is cached to match |

## Routes

- `GET /`: the console, one focused unit, a wall of every unit, the fleet overview (`?view=fleet`), a unit's inventory and ender chest (`?unit=<id>&view=detail`) the job graph (`?view=jobs&job=<id>`) the world ledger (`?view=ledger`) or the timeline (`?view=timeline`, with `&job=<id>` for one job); `GET /static/{file}` its assets, `/static/charts/{file}` the chart library and `/static/graph/{file}` the job graph; `GET /camera` redirects to `/`
- `GET /v1/units/stream`: server-sent `units` events from a once-a-second poll of each unit's `/v1/state`, sent on connect, on change and every 15 s; at most 64 clients, then 503. Also an `events` backlog on connect and a `log` event per new fleet event (death, offline, low health or hunger, task started, done, failed, ended with an unknown outcome, stuck); the `events` backlog replaces the console feed on each connect; the last 200 are kept in memory. Signed-in streams also get `positions`, every 2 s when something moved, and a `jobs` event with the newest 20 jobs whenever one changes
- `GET /v1/alerts`, `GET /v1/alerts/stream`: the alerts, see [Alerts](#alerts); anonymous and read-only, no coordinates and no operator id. `POST /v1/alerts/{id}/ack` and `/mute` (`{"for":"30m"}`, `0s` unmutes) need an operator session
- `GET /units/{id}/camera`, `/units/{id}/v1/state`
- `GET /units/{id}/v1/camera/{file}` for `ws`, `mesher.js`, `entities.{js,png,json}`, `atlas.{png,json}`, `items.{png,json}`, `stats`
- `GET /units/{id}/detail`: the unit's inventory, armour, experience and all 27 ender chest slots from the world save, merged with its live polled state. Signed-in operators only, `Cache-Control: no-store`; see [Unit detail](#unit-detail)
- `GET /v1/heatmap?layer=position|mining|travel&window=15m|1h|6h[&unit=<id>]`: signed-in, `Cache-Control: no-store`, 401 otherwise; see [Heatmaps](#heatmaps)
- `GET /healthz`
- `GET /metrics`: Prometheus text of the fleet series, see [Fleet metrics](#fleet-metrics). Cluster-only: the chart's public route lists paths and does not list this one
- `GET /signin`: passkey sign-in; the **I have an invite** section takes an operator and invite code and enrols a passkey. `/signin#operator=<id>&invite=<code>` still works and prefills that form. The invite rides in the fragment so it never reaches a log, and is removed from the address bar on load
- `GET /auth/methods`: `{"passkey":true,"xbox":<bool>}`, what the sign-in page offers. `GET /auth/xbox/start` and `GET /auth/xbox/callback`: the Xbox sign-in, only with it on; see [Signing in with Xbox](#signing-in-with-xbox)
- `POST /auth/register/options`, `/auth/register/finish`, `/auth/login/options`, `/auth/login/finish`, `/auth/logout`, `/auth/report`; `GET /auth/me`. Only with sign-in on
- `GET /v1/work/{unit}`: the unit's last 50 items, newest first
- `POST /v1/work/{unit}/tasks` (a `gather_and_deposit`, `haul` or `goto` template, as in the crew Task API), `/goto` (`{"x","y","z"}`, x and z −500..499, y −64..320), `/stop`, `/items/{id}/cancel`
- `POST /v1/jobs`: `{"kind":"plan","template":"parallel-demo"}` or `{"kind":"stock","place":"<id>","want":{"cobblestone":16}}`, 202 with the job, 422 when it cannot be planned; `GET /v1/jobs` (the last 50), `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` (409 while the scheduler holds it)
- `GET /v1/places`: every place with its stock; `POST /v1/places` (`{"id","name","kind","box"}`, as in `CREW_PLACES`) adds or edits one, `POST /v1/places/delete` (`{"id"}`) removes one; seeded places answer 409. Chests units report get `chest-x-y-z` ids, which a request may only rename, never move or create. Moving a place or changing its kind, by request or by `CREW_PLACES`, drops its stock; a rename keeps it
- `GET /v1/events?unit=&kind=&severity=&since=&until=&limit=&sort=asc|desc&cursor=<next>`: signed-in; the unit event history, newest first by default, every filter optional and exact, times RFC3339. Pages are keyset (`next` carries an `(at, id)` cursor, never an offset), at most 200 events each. Severity is ok, info, warn or crit; kinds are listed under Event history. Rows carry each unit's position, which is why the route is signed-in only
- `POST /v1/events/prune` (`{"dry_run":true}` counts what would go and lists the oldest 100, `{}` deletes it): signed-in; the 30-day retention, answering `{"pruned","total","would"}`
- `GET /v1/explorer/events`, `GET /v1/explorer/tail`, `GET /v1/explorer/views`, `POST /v1/explorer/views`, `POST /v1/explorer/views/delete`: signed-in; the event explorer (see [Event explorer](#event-explorer))
- `GET /v1/insights`: the rule insights, firing first, each with rule id, unit (or `fleet`), severity, state, first-seen time and a sentence; signed-in sessions also get the evidence event ids. Held in memory, so a restart starts them afresh. See [Insights](#insights-explanations-and-capacity)
- `GET /v1/units/{id}/explain?from=&to=`: rule-derived causes of the unit's failed tasks in the window (RFC3339, default the last hour, at most 24 h); event ids and sentences only, no positions, so it is not signed-in only
- `GET /v1/capacity?window=`: Little's-law load per task type over a trailing window (`1m` to `24h`, default `1h`); no positions
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

## Alerts

Rules run in crew-control on every poll and raise one alert per (rule, unit),
so a condition that stays true is one alert, not one per poll. An alert is
`firing` until its condition clears, then `resolved`, and a resolved alert is
dropped from the list after 10 minutes. A condition that comes back fires a
new alert, which needs a new acknowledgement. Alerts are in memory only: a
restart forgets acknowledgements and mutes, and whatever is still wrong fires
again on the first poll.

| Rule | Severity | Fires when | Clears when |
| --- | --- | --- | --- |
| `unit_died` | crit | the unit is dead | it respawns |
| `unit_stuck` | warn | the unit has been in the stuck state for `stuck_for` (the poller marks a task stuck after 90 s without progress or movement) | the unit makes progress, finishes or drops the task |
| `task_failures` | crit | `failures` tasks failed in a row | a task finishes done |
| `low_food` | warn | hunger is known and at or under `hunger_below` | hunger is above it, or unknown |
| `unit_silent` | warn | the unit has been unreachable for `silent_for` | the unit answers a poll |

`CREW_ALERTS` sets the thresholds; every key is optional:

| Key | Default | Meaning |
| --- | --- | --- |
| `stuck_for` | `0s` | extra time in the stuck state before alerting; `0s` alerts on the poll that flags it |
| `failures` | `3` | consecutive failed tasks, at least 1 |
| `hunger_below` | `6` | hunger value, 0 to 20 |
| `silent_for` | `2m` | unreachable time, at least `1s` |
| `mute_max` | `24h` | longest single mute, at least `1m` |

Death, a low-food reading and a stuck flag alert on the poll that sees them,
one second after the unit reports it. A unit that goes quiet is detected by
failed polls, not by missing events: an idle unit legitimately emits none.

The console shows them behind the **Alerts** bar button (Shift-A) with an
unread count for firing alerts that are neither acknowledged nor muted, and a
toast when a new one fires. The panel lists severity, unit, rule, and age,
and filters by firing/resolved and severity. Anyone can read it; **Ack** and
**Mute** (15 minutes, 1 hour, 8 hours, or Unmute) need a sign-in. A muted
alert stays listed but is neither counted nor toasted, and the mute holds if
the alert resolves and fires again before it ends.

An outbound webhook is deliberately not built. The extension point is the
engine's `Subscribe` signal plus `List`: a pusher watches for alerts whose
`since` it has not sent and posts them, so it needs no change to the rules.

## Heatmaps

The **Heat** bar button (or `h`) opens a top-down map of the lab with a
density layer over it, with a layer, a time window (15 m, 1 h, 6 h) and a
unit (or the whole fleet) to choose. Where units stand is operator-only: the
view and `GET /v1/heatmap` answer 401 without a session, and signed out the
page shows a sign-in state and never requests a grid.

Every `HEAT_SAMPLE` the service reads each online, living unit in the
overworld and folds it into per-minute counters on a 4x4 block grid, kept
for 6 hours. Memory is bounded by that retention and a cap of 256 cells per
unit, layer and minute (overflow is counted in `dropped`), not by uptime.
Nothing is written to disk, so a restart empties the maps.

- **Position**: samples per cell.
- **Mining**: samples taken while the unit's task is a gathering one. Units
  do not report individual block breaks, so this is where they spend time
  gathering, not a count of broken blocks.
- **Travel**: cells crossed between two consecutive samples under 10 s
  apart and under 64 blocks apart; teleports, respawns and gaps draw
  nothing.

The answer is `{cell, layer, window, unit, since, until, samples, max,
dropped, cells: [[cellX, cellZ, count]...]}` with cell coordinates in cell
units (block / 4, floored). The map shades cells on one hue in seven
square-root steps with a legend, outlines the places from the ledger, and
lists the busiest cells as text.

## Unit detail

`GET /units/{id}/detail` answers one unit's operators with what the world
save holds of it — armour, experience, and the ender chest, none of which a
unit reports live — merged with what the once-a-second poll of its
`/v1/state` heard. The ender chest is private to its owner, so unlike the
camera and state routes above it this one is **not** on the public
allow-list: it answers 401 without an operator session and is marked
`Cache-Control: no-store`, so neither a share nor the browser's own
cache may ever hold it (a signed-out tab must not repaint it). The public
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

On the console the answer is its own view: the **Detail** bar button, `d`
while a unit is selected, the `detail` button on any roster row, or a deep
link such as `?unit=crew-03&view=detail`. Signed out it is a state, never an
error: *Sign in to see a unit's inventory*, with the link to `/signin`.

The view answers about what it knows, not what it guesses. A small line
under the unit's name says where the numbers came from — *live* while the
poll is fresh, *from the save, N min ago* when the answer is stored, and
otherwise exactly why not (*reading the world save*, *world save not
connected*, or *this unit's record is not saved yet*), echoed per section:
the ender chest only ever appears from a save, so without one it says so
instead of showing a blank grid. The health, hunger and air gauges flag a
value of 6 or less. An effect shows its name and, above level I, a Roman
level; an effect the answer could not name shows as `effect <id>`. What the unit
carries is drawn as fixed slot grids — carry inventory and hotbar in server
slot order, empty slots visibly empty — and every slot names itself to a
screen reader and a focus line under the grid: item name with its own
custom name in quotes, a stack count beyond one, enchantments (numbered
when the answer does not name them), what is left of durability, and what a
shulker box carries. The grids take one tab stop: arrow keys rove slot by
slot and row by row, and Enter, Space or a click opens a shulker box's
contents in that same line. The armour strip shows all four worn slots and
the two held, so a missing piece is an empty cell with its place named in a
caption beneath (head, chest, legs, feet, main, off), not a grid that quietly
grows shorter. Item faces come from the
unit's own camera item sheet when it serves one; anything else is a small
placeholder tile, and counts, enchant and durability marks are text and
bars rather than colour alone.

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
invites Secret. A refused enrolment answers 403 `invite_used` for the
operator's real code once spent, and `invite_refused` for everything else
(wrong, expired or replaced code, another operator's code, unknown
operator), so a guess learns nothing. The page words them as *already been
used* and *not valid or has expired*. Invites carry no clock of their own: a
code lapses when its file is replaced or removed.

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

### Signing in with Xbox

A second way in for the operators, so a lost passkey does not lock anyone out.
Off unless `XBOX_LOGIN_ENABLED=true`.

1. The sign-in page asks `/auth/methods` and, if Xbox is on, shows **Sign in
   with Xbox**, a form that goes to `GET /auth/xbox/start`. The server keeps a
   state and a PKCE verifier in a signed `__Host-crew_xbox` cookie (10 minutes,
   single use) and redirects to Microsoft's `consumers` sign-in for the
   `XboxLive.signin` scope.
2. Microsoft returns the browser to `GET /auth/xbox/callback`. The server checks
   the cookie, the state and the code agree, redeems the code, and walks
   Xbox Live to the account's XUID. No refresh token is requested and no
   Microsoft token is returned, stored or logged.
3. The XUID must be in `CREW_XBOX_ACCOUNTS`; its operator gets the same
   `__Host-crew_session` cookie a passkey login does. Any other account is sent
   back to `/signin` with a sentence and no session. Gamertags are not used for
   the decision because a player can change theirs.

Removing an account from `CREW_XBOX_ACCOUNTS`, or its operator from
`CREW_OPERATORS`, ends its sessions at the next request. Refusals redirect to
`/signin?xbox=<word>` with one word of a closed list (`not_allowed`,
`cancelled`, `expired`, `state_mismatch`, `replayed`, `bad_request`,
`microsoft_error`, `exchange_failed`, `no_xbox_profile`, `child_account`,
`xbox_refused`, `xbox_unavailable`, `bad_identity`, `busy`, `fault`); the page
turns it into a sentence. Callbacks that reach Microsoft are limited to a burst
of 10 and one per 6 s for the whole server. The log has `operator_login` with
`method` `xbox` and the XUID, and `xbox_login_refused` with the `result`, within
a budget of 20 a minute.

**Setting it up (owner).** The sign-in is built and tested against a fake
Microsoft; going live needs an Entra app registration, which agents do not
create or hold secrets for.

- Reuse the map's registration or make a new one (personal Microsoft accounts
  only). Add the Web redirect URI `https://<PUBLIC_ORIGIN host>/auth/xbox/callback`,
  written exactly, to it.
- Put its client secret in a Secret in the crew-control namespace and mount it;
  set `XBOX_CLIENT_ID`, `XBOX_CLIENT_SECRET_FILE`, `XBOX_LOGIN_ENABLED=true`.
- Set `CREW_XBOX_ACCOUNTS` in the chart to each operator's XUID.
- Add `/auth/methods`, `/auth/xbox/start` and `/auth/xbox/callback` to the
  public HTTPRoute's allow-list (jdw-deployments); the route lists paths.
- Sign in once with Xbox and confirm it.

As for the map, whether Microsoft consents a personal app to `XboxLive.signin`
without Xbox partner enrolment was not verified without a real sign-in. If it
is refused the sign-in fails closed and passkeys are untouched.

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

## Chest browser

Signed-in operators get a **Chests** tab in the Jobs panel (Shift-J). It lists
every chest in the world ledger from `GET /v1/places` with its position, the
items last seen inside and when a unit last observed it, refreshed every 10 s
while the tab is open. The ledger keeps an item total per chest, not slots,
so each kind of item is one cell with its count. A chest not observed for 10
minutes is dimmed and marked as possibly changed rather than shown as current;
a chest no unit has looked inside says so. The units that observed a chest are
buttons that open that unit. Item faces come from a unit's camera sheet and
fall back to text.

## Fleet overview

`?view=fleet` (the **Fleet** button, or `o`) is a table of every unit for the signed-out public view and for operators alike, fed only by the `/v1/units/stream` events the console already receives. Each row shows state, time in that state, health and hunger bars, the task, and a three-minute health trail; a strip of stat tiles counts online, working, idle, stuck, dead and offline units and the tasks done and failed in the live feed window (the events the console holds: the stream's backlog of up to 200, then every live event up to 300, so those two are a window, not a total). Signed-in streams add a position column on wide screens. A state never rides on colour alone: each has its own shape and word.

- **States.** `working` and `idle` come from the unit's task; `dead` and `offline` from its status. `stuck` is a working unit whose current task the feed named stuck (no progress for 90 s); it clears when the task or its progress changes, or 10 minutes after the report (`STUCK_HOLD_MS`), because the feed carries no "unstuck" event and the unit list no stuck flag.
- **Time in state** is exact when the console saw the change or the feed backlog dates it; otherwise it is a floor, shown as `>=` the time since the page opened. Leaving `stuck` restarts the clock. The trail is a client-side window of one sample per 2 s, since the stream carries no history. Each sample is labelled by the time it was taken, and nothing is sampled while the stream is silent, so an outage or a throttled tab shows as a break, not a flat line.
- **Sort and filter** by state, name, health, hunger, task or time in state (unknown values always last); filter by text, state, task kind and health or hunger at most 6, 10 or 15. Both live in the address (`sort`, `dir`, `q`, `st`, `task`, `hp`, `food`) so a view can be linked.
- **Keys.** `/` filters; arrows, `j`/`k`, Home, End and PageUp/Down move between rows; Enter or a click focuses the unit's camera; `a` jumps to the next unit that needs a look; Esc clears the filter. While the fleet is up the console's own `j`/`k`, `a`, digit and bracket keys are the table's, wherever focus sits, so they never leave it; click a row to open a unit.
- **Updates.** Rows are built once and patched in place every 2 s, with fixed column and row sizes so nothing shifts. A sort by a changing value re-orders rows, but not while the pointer is over the table. On a phone each unit is a two-line card and the camera modes, event feed and status bar are hidden to leave room.

Logic lives in `overview.js` (node-tested in `overview.test.mjs`), the DOM in `overview-ui.js`, styles in `overview.css`; the chart library supplies the stat tiles and sparklines.

## Job graph

`?view=jobs` (the **Graph** button, or `g`) draws one job as a layered DAG: a
node per step and an edge from every dependency to the step that waits on it.
The list beside it holds the newest 20 jobs, running ones first, and a deep
link to an older job fetches that one
(`GET /v1/jobs/{id}`): the view shows "loading that job…" meanwhile, "job not
found" on a 404, and retries with growing waits on a 503 or a dropped
connection. Jobs come from the signed-in `jobs`
event on `/v1/units/stream`; signed out the view says so and anonymous streams
never receive the job payload. `?view=jobs&job=<id>` opens a specific job, and
the address follows the list selection.

- **Layout.** `layout.js` is a pure, library-free layered layout: longest-path
  layering, barycentre crossing reduction, a critical path and Kahn's
  algorithm with strongly connected components to report a dependency cycle.
  The critical path is the longest chain of steps: every step counts one, as
  no step carries a duration. Only the steps on a cycle are named in the
  toolbar; they share one column, and steps that merely wait on the cycle are
  laid out after it like any others. A cycle never hangs the layout;
  60 nodes lay out well under the 50 ms budget. Layout runs only when the
  set of steps or edges changes, so a live `jobs` update patches state in
  place and never moves a node or the viewport.
- **State.** Every node carries the step's word, a signature shape and its
  tone, so state never rides on colour alone. The critical path is a thicker
  outline and edge, and "on critical path" is part of the node's accessible
  name; the selected node is marked and carries `aria-current`. The details panel names the step's unit, task, state, the
  job's timing, attempts, work item and the failure reason.
- **Interaction.** Drag or scroll to pan and zoom, **Fit** resets the view,
  click a node for details. Tab moves between nodes; Right and Left follow an
  edge to what the step unlocks or waits for, Up and Down step through the
  same layer, Enter opens the details and Esc closes them and returns focus to
  the node. A `jobs` update patches in place: it never moves focus, and the
  selected node, open details and pan and zoom survive it. Empty state says
  "no jobs yet"; signed out it says "Sign in to see jobs."

The graph component is generic (`graph.js` owns the DOM, `layout.js` the
algorithm); `jobs.js` maps the job/steps shape into it and `jobs-ui.js` owns
the jobs list, stream updates and deep link. Node tests cover the pure parts
(`layout.test.mjs`, `jobs.test.mjs`) and, against a small fake DOM
(`fake-dom.mjs`), the component and the view (`graph.test.mjs`,
`jobs-ui.test.mjs`). `demo.html` is the standalone component page; its script
is `demo.js`, as the console's CSP refuses inline script.

## World ledger graph

`?view=ledger` (the **Ledger** button, or `l`) draws the world ledger as a node
graph on the job graph's canvas. It reads `GET /v1/places` when it opens and
every 15 s while the tab is visible (**Refresh** asks at once), so it is
signed-in only, as the places route is: signed out it says "Sign in to see the
world ledger." and asks for nothing. A 503 says the ledger is unavailable and
keeps the last good picture on screen.

- **Nodes.** A place (a chest or an area), a resource (an item type found in
  stock or listed on an area) and a unit (the one that last read a chest).
- **Edges.** *Stored in* (solid) joins an item to each place that holds it,
  *supplied by* (dashed) joins an area to the items it lists, *used by*
  (dotted) joins a chest to the unit that last read it. Hover a line for its
  meaning; the dash pattern means no line rides on colour alone.
- **Staleness.** Stock is what a unit last saw, not a live inventory. Each
  place and resource carries a chip with a word, a shape and the age of its
  newest reading: *fresh* (up to 2 min), *aging* (up to 10 min), *stale*, or
  *never seen*. The details panel (click or Enter on a node) lists the
  last-known contents, when and by which unit each was read, and the place's
  position.
- **Layout.** *Auto layout* is the layered layout. *Map layout* places each
  place at its box centre in world coordinates (east right, south down, one
  scale on both axes) and nudges chests that would overlap down a row;
  resources and units, which have no position, stand in columns to the right.

The hand-off graph and resource-flow Sankey are not built: they need chest
deposit and withdraw events, which units do not post (see Event history).

## Timeline

Press `t` (or Timeline in the bar) for the operator-only timeline, built from the
[event history](#event-history) with `GET /v1/events`; signed out it shows a
sign-in note and asks for nothing, because event details carry positions.

- **Units**: one lane per unit over 1 h, 6 h, 24 h or 7 d ending now. A task is a
  bar from its start event to its finish, coloured by task template; a failed task
  has a red edge and an X, a cancelled task or one that ended with an unknown
  outcome is a dashed outline, idle poll roll-ups are thin grey bars. Deaths
  (diamond) and stuck warnings (triangle) are markers; offline time is hatched and
  dead time is dark red. Wheel or the buttons zoom, drag or the arrow keys pan
  inside the loaded window, and Earlier and Later load the neighbouring one.
  Hover for a tooltip; click a bar or marker to list its events in the side panel
  (Esc closes it).
- **Job**: the steps of one job (pick it, or open `?view=timeline&job=<id>`) in the
  order they ran, with unit, outcome, start, duration and a mini bar. Only events
  of the job's own tasks are used, so other work on the same units is left out.

Each unit is read with its own paged query, newest first and capped at 15 pages
of 200; when a busy unit hits the cap the oldest rows are dropped and the bar says
so. The window is never loaded all at once.

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

**Analytic fields.** Task events carry, when the unit sent them: `reason_code`
(the unit's closed code, see the crew README; absent from an older unit),
`duration_ms`, `outcome` (`ok`, `noop`, `failed`; `noop` is decided by the idle
rule below, so only roll-ups carry it), `session_id`, `attempt`, `crew_version`
and `cause_ref`. A failed task gets a `cause_ref` to the latest `kit_failed`,
`session_error` or `died` on the same unit in the 30 minutes before it, never
to a later event or another unit. The free `reason` text stays in `text` and is
never exported to a model; a unit-sent `reason_text` is cut to 120 characters.
None of these holds a coordinate. `GET /v1/events` also filters on
`reason_code`, `outcome`, `session_id` and `cause_ref`; a malformed value is a
400.

Status: the in-memory store and the routes implement all of this. The Postgres
table does not have the columns yet (that migration ships from the platform
repo), so the Postgres store writes the old columns only and answers a request
using the new filters with a 400 rather than ignoring them. The unit's own
`kit_failed`, `died`, `respawned` and `session_error` are validated by
`events.FromUnit` (closed kinds and codes, bounded text) but no route accepts
them yet: units post task reports only, and a unit-authenticated route is
follow-up work.

**Idle roll-up.** A hauling unit re-checks its chests every couple of seconds
and finishes in a fraction of one, which in one measured hour was 78% of all
task events. Storing each would make the table about 4.6 times larger and every
throughput chart a chart of no-ops, so they are collapsed per unit:

- A finished task is idle when its kind is `task_done`, its template is in the
  idle list (only `haul`) and its duration is known and at most 2 s. A failed,
  cancelled or unknown-outcome task, a slow haul, a haul of unknown duration
  and every other template keep their own rows.
- Idle polls from one unit and template become one `idle_rollup` row: `at` is
  the first poll, the payload has `template`, `count`, `total_ms`, `last` and `text`; the row's
  `duration_ms` is `total_ms` and its `outcome` is `noop`. A
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

## Event explorer

`?view=events` (the **Explorer** button, or `x`) filters, sorts and searches the unit event history, Grafana-Loki style.

- **Filters** are chips: units, kinds, severities (each with an icon and a word), a task id, free text and a time range (presets, or a custom from/to). Lists are OR within a field and AND across fields. Text is a case-insensitive substring over unit, kind, task id and the payload. Click a column header to sort by time, level, unit or kind; click again to flip. Click a row for its full payload.
- **Links.** The filter lives in the address as `f.unit`, `f.kind`, `f.severity`, `f.task`, `f.q`, `f.range` (`15m`, `1h`, `6h`, `24h`, `168h`), `f.since`, `f.until`, `f.sort`, `f.dir`, so a copied link opens the same view.
- **Live tail.** New events appear within about a second, at the top when sorted newest-first. Pause holds them back and counts them; Resume reloads. Under any other sort they are counted, not inserted.
- **Saved views** are named filter sets, per signed-in operator, up to 50, 60-character names. They are kept in memory, so a restart forgets them. Signed-out viewers get the same filters over the public feed the page already holds (no coordinates, no task ids, no history beyond the feed's last 300) but cannot save views or tail history.
- `GET /v1/explorer/events?unit=&kind=&severity=&task=&q=&since=&until=&range=&sort=at|unit|kind|severity&dir=asc|desc&limit=&cursor=`: lists take commas. Answers `{events,total,next,head,truncated}`; paging is keyset on the sort key, then `(at, id)`, never an offset. Positions are in the rows, so it is signed-in only.
- `GET /v1/explorer/tail?<same filters>&after=<head>`: server-sent `events` carrying `{events,head}` for rows stored after `after`; at most 16 clients.
- The routes read the history through a `Source` interface (`internal/explorer`). The in-memory source scans a snapshot of the store; a store that cannot snapshot is paged, newest first, up to 20,000 events, and the answer says `truncated`. A database-backed source would push the filter into its query.
- **Performance.** Filtering with six units, three severities and a text search, then sorting 10,000 events takes about 4 ms (`go test -run 10k -v ./internal/explorer`, budget 200 ms); `go test -bench . ./internal/explorer` repeats it.

## Dashboard

`b` (or the Dashboard button, or `?view=dash`) opens a grid of panels over the
live fleet. A filter row above the grid holds the one time window (5 m, 15 m,
1 h, or everything the page has seen) and the unit filter; both apply to every
panel. Panels read the units stream and the event feed, so they work for
anyone and show nothing the public console does not already show (no
coordinates). The browser keeps the history, not the server: a panel's window
is what this tab has watched since it opened, and the event backlog on
connect.

Starter panels: fleet health (gauge), task throughput, success rate, units
with alerts, units by state over time, outcomes per unit, idle time, hunger,
task durations, unit status, and recent alerts. Every chart has a table view.

**Edit layout** turns on per-panel buttons (move earlier or later, narrower or
wider on the 12-column grid, shorter or taller, remove) and drag-and-drop;
**Add panel** inserts any type. The working layout saves itself in this
browser's `localStorage`; **Save as** names a layout, the layout menu loads
one, **Reset** restores the default. Layouts are per browser, not per
operator, and are not synced.

### Panel contract

A panel type in `dash-panels.js` is `{ title, about, size, query, create }`.
`query({ data, units, filter, now })` is a pure function returning a
JSON-safe model; `create(host, { height })` builds the chart and returns
`{ update(model), destroy() }`. The dashboard calls `update` only when the
model changed. Add a type to `PANELS` and it appears in the Add menu; layouts
naming a type that no longer exists drop it on load.

## Fleet metrics

`GET /metrics` answers in Prometheus text on the same port as everything else,
and is cluster-only because the public HTTPRoute allow-lists paths and
`/metrics` is not one (`tools/tests/test-crew-route.sh` in jdw-deployments
fails if that changes). Under `/units/` the proxy's allow-list answers 404 for
any metrics path. The registry is the fleet's own, so there are no Go or
process series, only the ones below.

**These are samples.** Everything comes from the once-a-second poll of each
unit's `/v1/state`, so a task that starts and ends between two polls is never
seen. In one measured hour the poller saw a haul about one time in five. A task
of several seconds, and every failure of one, is counted reliably; quick tasks
are undercounted. Time-in-state is exact to about one poll per change of state;
the state gauges are exact as of the last poll. The exact per-task record is
the units' own.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `crew_fleet_tasks_started_total` | counter | `unit`, `template` | Task starts the poller saw. Includes starts that turned out to be idle polls |
| `crew_fleet_tasks_finished_total` | counter | `unit`, `template`, `outcome` | Task ends. `done` excludes idle polls, so `rate(done)` is throughput |
| `crew_fleet_task_failures_total` | counter | `template`, `reason` | Failed tasks by normalised reason |
| `crew_fleet_task_duration_seconds` | histogram | `template`, `outcome` | How long tasks ran, from the unit's own times; `done` and `failed` only, idle polls left out |
| `crew_fleet_unit_last_done_timestamp_seconds` | gauge | `unit` | When the unit last finished real work; `time()` minus it is its time without work. Absent until the first |
| `crew_fleet_unit_deaths_total` | counter | `unit` | Deaths a poll saw |
| `crew_fleet_unit_stuck_total` | counter | `unit` | Stalls (no progress for 90 s on a running task), once each |
| `crew_fleet_units` | gauge | `state` | Units per state at the last poll; the five states sum to the roster |
| `crew_fleet_units_online` | gauge | none | Everything but `offline` |
| `crew_fleet_unit_state` | gauge | `unit`, `state` | 1 for each unit's current state, 0 otherwise |
| `crew_fleet_unit_state_seconds_total` | counter | `unit`, `state` | Time each unit has spent in each state |

Label values are closed sets, never a raw string a unit sent:

- `unit`: the roster. Events from any other id are dropped.
- `state`: `working`, `idle`, `stuck`, `dead`, `offline`. Offline wins, then dead, then stuck, then working, else idle.
- `outcome`: `done`, `failed`, `cancelled`, `unknown` (the unit's recent list no longer held the verdict), `idle` (a successful finish of an idle-eligible template within 2 s, the rule the history's idle roll-up uses; it is the idle-poll count).
- `template`: the ten crew templates (`gather_and_deposit`, `haul`, `withdraw`, `deposit`, `mine_shaft`, `goto`, `inspect`, `observe_blocks`, `place_block`, `craft`), else `other`.
- `reason`: below. A unit's failure text is free text, so it is matched, case-insensitively, against phrases; the first row to match wins, and no match is `other`. A new failure shows up under `other` until a row is added in `internal/metrics/reason.go`.

| `reason` | Matches (phrases) | Seen as |
| --- | --- | --- |
| `session` | `session ended` | |
| `died` | `unit died` | |
| `no_path` | `no path`, `no walking path`, `no standing spot`, `cannot reach`, `no way down`, `trapped` | `no path`, `getting to the chest: walking to the goal: no path` |
| `missing_item` | ` in inventory`, `left to deposit`, `, need `, `out of blocks`, `nothing to haul` | `no axe in inventory` |
| `no_pickup` | `no pickup` | |
| `no_progress` | `no progress after` | |
| `container` | `chest`, `crafting table` | `the chest's chunk is not loaded`, `chest did not open within 300 ticks` |
| `server_rejected` | `server rejected`, `server did not`, `did not confirm` | |
| `timeout` | `took too long`, `timed out`, `ticks` | `visit took too long` |
| `action_failed` | `did not break`, `did not land`, `never settled`, `drifted`, `did not come down`, `is blocked at its first step` | |
| `invalid_task` | `invalid task`, `outside the allowed`, `outside the area`, `unknown template` | |
| `other` | anything else | |

Series stay bounded by the label sets: with 20 units the worst case is about two
thousand, and the usual count is a few hundred because a series appears only
once its event has happened. Per-unit state, death and stall series exist from
start so that every unit shows up before it does anything.

Not exported, because crew-control cannot see them: kit failures, session
errors and travel distance. They are in the units' logs only, and need the units
to emit them (a closed reason code from the unit would also replace the text
matching above).

## Performance budget

The console has to stay above 30 fps with 20 units: every tile live, the roster
and the event dock open, and the fleet dashboard. `perf/budget.json` holds the
limits (frame rate, p95 frame time, time to interactive, JS heap, stream bytes
per second per unit, longest main-thread task); `perf/budget.mjs` enforces
them and exits 1 when one is broken.

```sh
PLAYWRIGHT=/path/to/playwright/index.js node minecraft/crew-control/perf/budget.mjs
# flags: --scenario=wall-all|fleet  --seconds=15  --json=out.json  --cpu-throttle=4
```

It builds and starts crew-control against 20 simulated units whose state
changes every poll (about 20 fleet events a second, the worst steady rate),
opens it in headless Chromium and measures after a 3 s warm-up. The camera
pages are canvas stand-ins: real cameras are WebGL and depend on the GPU, so
the run measures what the console itself adds around 20 live frames, not
camera cost. `--url` points it at a running console instead.

Measured on the devbox (headless Chromium, 15 s window; "before" is the harness on the unfixed event dock):

| | wall, 20 live | fleet dashboard |
| --- | --- | --- |
| before: fps / p95 frame / longest task | 53.4 / - / 231 ms | 54.4 / - / 174 ms |
| after: fps / p95 frame / longest task | 59.8 / 16.8 ms / none over 50 ms | 60 / 16.7 ms / none over 50 ms |
| heap, time to interactive, stream bytes per unit | 9.8 MB, 125 ms, 383 B/s | 2.3 MB, 97 ms, 384 B/s |

Two fixes came out of it. The event dock rebuilt and re-formatted all 120 rows
on every event; rows now persist while their event is shown and renders are
coalesced to one per frame. And when the wall cannot hold 24 fps for 4 s the
console steps its live-camera budget down (20 to 12 to 9 to 6 to 4) and says
so in the wall bar; the viewer's own choice is not saved over and returns on
the next load. Tighten the limits when a fix lands, never loosen them to pass.

## Insights, explanations and capacity

All of this is fixed rules over the unit event stream: no model is called, the
same input always gives the same answer, and every finding names the events it
rests on. The rules read these optional payload keys of a stored event:
`reason_code`, `cause_ref` (an event id), `template`, `duration_ms`, `idle` and
`anchor` (a coordinate-free fingerprint that is equal while a unit has not
moved). Until units emit them the rules that need them stay quiet or report
failures as unexplained; they never guess.

### Rule catalogue

The engine evaluates every 10 s over the last 45 minutes. One firing insight
exists per rule and unit; it resolves when the rule stops holding. Thresholds
are in `insight.DefaultConfig`.

| Rule | Fires when | Why it exists |
| --- | --- | --- |
| R1 zero success | 3 or more failures in a row with one reason code and no completed task for 5 minutes | A unit that never succeeds has a zero baseline, so no baseline rule can see it. In the motivating hour four units failed all hour unnoticed |
| R2 throughput drop | An EWMA chart (lambda 0.2, k 3) on useful completions per minute falls below the unit's own median of the 20 minutes before the last 5. Sigma is 1.4826 x MAD, at least 0.5; fewer than 10 minutes of history gives no verdict | Catches a working unit slowing against itself. Baselines are per unit: a fleet-wide baseline flagged the haulers for being fast |
| R3 long task | A running task has outlasted its template's `median + 3.5 x sigma`, sigma being MAD / 0.6745 floored at 5% of the median and at 1 s; needs 10 finished runs | The floor matters: a 0.2 s haul with zero MAD would otherwise flag a third of hauls |
| R4 common mode | At least half the working units raised R2 within 2 minutes and at least 5 `session_error` events fell in that window | A server restart slows everyone at once. It raises one fleet insight and suppresses the per-unit R2s it explains |

Only completed, non-idle tasks count as throughput. Persisting insights in
Postgres and a thresholds-from-chart-config path are not built yet.

### Why panel and cause classes

The unit detail page and each unit insight row have a Why panel with the
sentence, the event-id chain and a "Rule-derived" badge showing the rule id.
Failures no rule covers are listed as unexplained with equal prominence.

| Class | Rule id | Chain |
| --- | --- | --- |
| `missing_tool` | `missing_tool.kit_failed`, `missing_tool.died` | the `kit_failed` or `died` event (named by `cause_ref`, else the latest within 30 min), then the failure |
| `session_lost` | `session_lost.session_error` | a `session_error` named by `cause_ref` or within 60 s before |
| `geometry_trap` | `no_path.same_anchor` | 3 or more `no_path` failures from one unchanged anchor with no success between; the cause is stated as unknown |
| `low_hunger`, `low_health` | `vitals.low_hunger`, `vitals.low_health` | a low-vitals event within 2 minutes before the failure |

To add a rule, write a function in `internal/explain` that takes the failure
and the events before it and returns a verdict with the chain, call it from
`classify`, and add table-test rows for a match, a near miss and a missing
predecessor.

### Capacity

`/v1/capacity` reports, per task type, arrivals per minute (completed plus
failed), mean duration, and `L = lambda x W`, the mean number of busy units.
Utilization is the sum of L over the units that completed anything in the
window. Time in failed tasks is shown apart, and idle roll-ups are listed but
never counted. Little's law needs a steady arrival rate: a type whose first and
second half of the window differ more than 2x is flagged `non_stationary`, as
is the report, and the console says the numbers are rough. Tasks with no
duration are counted in `unknown_durations` and pull the mean toward the
known ones, so a high share makes the view unreliable.

### Critical path

`jobs.CriticalPath(steps)` returns the longest chain by duration over
`depends_on`, each step's slack, and flags steps with no known duration (never
critical). A cycle or unknown dependency is an error; equal chains go to the
smaller step key. Steps do not store durations yet, so the job API does not
return `critical` or `slack_s`.
