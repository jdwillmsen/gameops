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
| `CREW_UNITS` | required | JSON list `[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`, 1 to 26 units |
| `LISTEN_ADDR` | `0.0.0.0:8080` | Listen address |
| `LOG_LEVEL` | `info` | Log level |
| `PUBLIC_ORIGIN` | unset | `https://host`, lower case, no port or path; its host is the WebAuthn RP ID. Unset, or no operators, turns sign-in off |
| `CREW_OPERATORS` | unset | JSON list `[{"id":"owner","name":"Owner"}]`, at most 8; ids are slugs `^[a-z][a-z0-9-]{1,15}$` |
| `CREW_INVITES_DIR` | unset | Mounted Secret directory with one file per operator id holding that operator's one-time invite code; missing or empty means nobody can enrol |
| `DATA_DIR` | `/data` | Persistent volume for `session.key`, `passkeys.json` and `invites-used.json` |
| `WORK_TOKEN` | unset | Shared bearer token for the unit work routes; unset leaves them out entirely (404) |
| `PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USERNAME`, `PG_PASSWORD` | unset, `5432` | Postgres holding the queue (`minecraft.crew_work`, `minecraft.crew_work_event`), the world ledger (`minecraft.crew_place`, `minecraft.crew_stock`) and jobs (`minecraft.crew_job`, `minecraft.crew_job_step`), over `sslmode=require` |
| `WORK_STORE` | unset | `memory` keeps the queue, ledger and jobs in memory when `PG_HOST` is unset; development only, it is lost on restart |
| `CREW_PLACES` | unset | JSON list of named chests and areas, `[{"id","name","kind","box":{"min":{x,y,z},"max":{x,y,z}},"items":[]}]`; seeded at start, replacing earlier seeded places but never ones added by hand. `[]` removes every seeded place; unset or blank seeds nothing |

## Routes

- `GET /`: the console, one focused unit or a wall of every unit; `GET /static/{file}` its assets; `GET /camera` redirects to `/`
- `GET /v1/units/stream`: server-sent `units` events from a once-a-second poll of each unit's `/v1/state`, sent on connect, on change and every 15 s; at most 64 clients, then 503. Also an `events` backlog on connect and a `log` event per new fleet event (death, offline, low health or hunger, task started, done, failed, ended with an unknown outcome, stuck); the `events` backlog replaces the console feed on each connect; the last 200 are kept in memory. Signed-in streams also get `positions`, every 2 s when something moved, and a `jobs` event with the newest 20 jobs whenever one changes
- `GET /units/{id}/camera`, `/units/{id}/v1/state`
- `GET /units/{id}/v1/camera/{file}` for `ws`, `mesher.js`, `entities.{js,png,json}`, `atlas.{png,json}`, `items.{png,json}`, `stats`
- `GET /healthz`
- `GET /signin`: passkey sign-in; `/signin#operator=<id>&invite=<code>` enrols a passkey. The invite rides in the fragment so it never reaches a log
- `POST /auth/register/options`, `/auth/register/finish`, `/auth/login/options`, `/auth/login/finish`, `/auth/logout`, `/auth/report`; `GET /auth/me`. Only with sign-in on
- `GET /v1/work/{unit}`: the unit's last 50 items, newest first
- `POST /v1/work/{unit}/tasks` (a `gather_and_deposit`, `haul` or `goto` template, as in the crew Task API), `/goto` (`{"x","y","z"}`, x and z −500..499, y −64..320), `/stop`, `/items/{id}/cancel`
- `POST /v1/jobs`: `{"kind":"plan","template":"parallel-demo"}` or `{"kind":"stock","place":"<id>","want":{"cobblestone":16}}`, 202 with the job, 422 when it cannot be planned; `GET /v1/jobs` (the last 50), `GET /v1/jobs/{id}`, `POST /v1/jobs/{id}/cancel` (409 while the scheduler holds it)
- `GET /v1/places`: every place with its stock; `POST /v1/places` (`{"id","name","kind","box"}`, as in `CREW_PLACES`) adds or edits one, `POST /v1/places/delete` (`{"id"}`) removes one; seeded places answer 409. Chests units report get `chest-x-y-z` ids, which a request may only rename, never move or create. Moving a place or changing its kind, by request or by `CREW_PLACES`, drops its stock; a rename keeps it
- `GET /v1/units/{id}/work?wait=25s[&kinds=command]`, `POST /v1/units/{id}/events`: unit-facing, `Authorization: Bearer <WORK_TOKEN>`, cluster-only

Any other path, method, unknown unit or unclean path under `/units/` gets 404
without an upstream request. Only the `r` query (1 to 3 digits) is forwarded,
and only on `ws`.
Cookies, `Authorization` and client `X-Forwarded-*` headers are dropped.

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

The `/v1/work/*`, `/v1/jobs` and `/v1/places` routes answer 401 without a session. Every `POST` needs a JSON
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
