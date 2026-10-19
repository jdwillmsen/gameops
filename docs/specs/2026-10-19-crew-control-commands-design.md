# crew-control: sign-in, commands and the task queue

**Status:** draft for owner review, 2026-10-19
**Epic:** JDW-59. **Ticket:** JDW-116.
**Builds on:** `docs/specs/2026-10-19-crew-console-and-control-design.md` (step 2), and crew-control v0 (JDW-115, live).

## Why

The console at `crew.prd.jdwlabs.com` can only watch. Every command still comes from a script on the operator's machine that calls a unit's token-guarded `POST /v1/tasks` over a port-forward. The owner and Josh want to command units from the console.

## Done when

On `crew.prd.jdwlabs.com`:

- A signed-in operator queues a chop on Basalt, watches it finish, and stops Agate mid-task with one click.
- An anonymous viewer can do neither, and sees no command history.

## Owner decisions (2026-10-19)

- **Sign-in:** crew-control has its own sign-in, with passkeys on `crew.prd.jdwlabs.com`. It does not reuse the FWB map's sign-in, so the map is untouched. This replaces answer 1 in the console design.
- **Operators:** the owner and Josh.

## Design

### 1. Operators and enrolment (my calls)

- **Who.** The chart lists operators as `crew.control.operators: [{id: owner, name: …}, {id: josh, name: …}]`. The ids are stable short slugs, not gamertags or XUIDs. Crew has no Xbox identity to tie them to, because the lab runs offline.
- **Enrolment needs an invite**, because a passkey proves only that the same device came back, not who enrolled it.
  - The owner creates a Secret, `crew-control-invites`, with one key per operator id and a random value. The README gives the exact command. The value never passes through Git or an agent's shell.
  - The owner sends Josh his invite out of band.
  - Opening `/signin?invite=<code>` lets that operator register a passkey. The code is compared in constant time against the Secret's value for that id.
  - An invite is single-use: crew-control records a hash of each used code. A new device needs a new value in the Secret, which the owner rotates.
- **Limits.** Up to 5 passkeys per operator. The owner removes one by deleting it from the store file (README). There is no removal UI in this step.
- **Library and RP ID.** The library is `go-webauthn/webauthn`, already in `go.mod` for the map. The RP ID is `crew.prd.jdwlabs.com`.

### 2. Sessions

- **Cookie.** `__Host-crew_session`: HttpOnly, Secure, SameSite=Strict, Path=/. The value is the base64 of `{op, key, iat, exp}` plus an HMAC-SHA256. It lasts 12 hours, the same as the map's passkey session.
- **Revocation.** Removing an operator from the chart, or deleting their passkey, ends their sessions at the next request, because each request re-checks both.
- **Storage.** The signing key and the passkey store live on a small crew-control PVC (`/data`, truenas-nfs, 64Mi): `session.key`, `passkeys.json` and `invites-used.json`. This mirrors how the map stores its own. Postgres is not needed for sign-in.
- **CSRF.** Every state-changing request is a `POST` with a JSON body and needs the header `X-Crew-Request: 1`. SameSite=Strict plus that custom header blocks cross-site forms.

### 3. Work dispatch: units pull (design record D5)

- **Long poll.** Each unit long-polls `GET http://crew-control:8080/v1/units/{id}/work?wait=25s` with its own token.
- **Tokens.** One shared token covers all units: Secret `crew-work`, key `token`. It is per unit in name only, because the lab is a single trust domain; a per-unit Secret would mean one more Secret for every new unit. crew-control checks that the path's `{id}` is on the roster.
- **What a poll returns:** either nothing (204 after `wait`), or one item: `{"kind":"command","id":…,"command":{…}}` or `{"kind":"task","id":…,"template":{…}}`.
- **Delivery and retries.** crew-control marks an item delivered when it hands it out. If no event for that id arrives within 30 s, it hands the item out again. The unit ignores an id it already has, so retries are idempotent.
- **Events.** The unit posts `POST /v1/units/{id}/events` with `{"id":…,"state":"running|done|failed|cancelled","progress":n,"reason":"…"}`. These are the same states the runner already logs.
- **Unit side.**
  - A new `internal/control` package in crew runs the poll loop. It feeds tasks into `Runner.Submit` with the server's id, and maps runner transitions to events.
  - It is enabled only when `CONTROL_URL` and `CONTROL_TOKEN` are set. Without them, crew behaves exactly as today.
  - The local `POST /v1/tasks` stays for lab scripts.
- **Losing crew-control.** A unit that loses crew-control finishes its current task, then idles and keeps retrying with back-off up to 30 s.

### 4. Commands (the priority lane)

- **This step ships `stop` and `goto` only**, as the console design said.
- **`stop`.** It cancels the current task and every queued task for that unit. It goes out ahead of any queued task, on the next poll. On the unit it calls `Runner.Cancel` for each.
- **`goto {x, y, z}`.** It is a new task template, `goto`, run by a skill that wraps the existing `Navigate`. It reaches the block, or fails with `unreachable`. Being a template, it shows in the task list like any other.
- **Bounds.** Both are bounded to the lab's 1000×1000 flat square and y between −64 and 320. Anything outside is rejected with 400.

### 5. Queue and history: Postgres

- **Database.** The platform's shared CloudNativePG cluster, database `jdwillmsen_prd`, new schema `crew`. The migration is a Flyway file in `jdwlabs/platform`, beside the agent's.
- **Credentials.** The agent's pattern: an ExternalSecret mirroring the app-role Secret, read as `PG_*` env from `secretKeyRef`.
- **Tables:**
  - `crew.work(id uuid pk, unit text, kind text, body jsonb, state text, progress int, reason text, operator text, created_at, updated_at, delivered_at)`
  - an index on `(unit, state)`
  - `crew.work_event(id bigserial, work_id uuid, state text, progress int, at timestamptz)`
- **History.** The console shows the last 50 items per unit. Rows older than 30 days are deleted by crew-control on start and then daily.
- **No coordinates in logs.** A `goto` target lives only in `body` and is never logged.
- **Postgres down.** If Postgres is unreachable, crew-control still serves the view-only console and answers command endpoints with 503. Viewing never depends on the database.

### 6. Console

- **Signed out.** A "Sign in" button appears in the bar. Nothing else changes for anonymous viewers.
- **Signed in.** A side panel for the focused unit shows:
  - a Stop button;
  - "Go to" with x, y and z fields;
  - "Queue task", with a template picker (gather cobblestone, chop oak_log, haul) and its fields, shaped like `lab/parallel-demo.sh`;
  - the queue, with cancel on each item;
  - history (last 50).
- **Live updates** arrive on the existing SSE stream as a second event, `work`. It is sent only to a stream opened with a valid session cookie. Anonymous streams never get it.
- **Reorder** is deferred. The design asked for it, but cancel plus re-queue covers this step. It comes with jobs (JDW-117).

### 7. Public surface

- **New routes.** The route allow-list gains `/signin`, `/auth/*` and `/v1/work/*` for the browser.
- **Unit-facing routes stay cluster-only.** `/v1/units/{id}/work` and `/events` are blocked on the public route. crew-control also refuses them on any request carrying the gateway's `X-Forwarded-For`. The real guard is the work token; the chart test asserts they are unrouted.
- **CPU.** crew-control stays at 50m: the long polls are idle connections, and SSE is unchanged.

## Out of scope

- Follow a player, come to me, and return home (new skills).
- Jobs (JDW-117).
- Builds (JDW-118).
- Xbox sign-in.
- A passkey-management UI.

## Delivery

| PR | Repo | Contents |
|---|---|---|
| 1 | jdwlabs/platform | Flyway migration for schema `crew` and grants |
| 2 | gameops | crew `internal/control` and the `goto` template; crew-control auth, work API, Postgres store and console panel |
| 3 | jdw-deployments | PVC, Secrets references, ExternalSecret, env, route allow-list and tests |

The owner creates `crew-control-invites` and `crew-work` with the README commands. Their values are never printed by an agent.
