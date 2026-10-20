# crew-control: sign-in, commands and the task queue — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Signed-in operators (the owner and Josh) can command crew units from `crew.prd.jdwlabs.com`: queue tasks, stop a unit and send it to a point, and see the queue and history. Anonymous viewers can do none of it (JDW-116).

**Architecture:**
- **Sign-in.** crew-control gets its own passkey sign-in, enrolled by invite codes, with sessions in an HMAC cookie. All of it lives on a small PVC.
- **Work storage.** Work items live in Postgres (`minecraft.crew_work`).
- **Delivery.** Units long-poll crew-control for work with a shared work token and post task events back.
- **Unit side.** A new `internal/control` package bridges the poll loop to the existing task `Runner`.
- **Console.** The console gains a signed-in command panel. It gets live updates through the existing SSE stream.

**Tech Stack:**
- Go 1.27: `go-webauthn/webauthn` (already in `go.mod`) and `jackc/pgx/v5` (already in `go.mod`).
- Plain JS console.
- Flyway SQL in `jdwlabs/platform`.
- Helm in jdw-deployments.

**Spec:** `docs/specs/2026-10-19-crew-control-commands-design.md`

## Global Constraints

- Lab only. Nothing connects to FWB or reads FWB data. No coordinates in logs, metrics, Jira or PR text. A `goto` target lives only in request and DB bodies.
- **Anonymous viewers** get exactly today's console. They see no command UI and no history, and no `work` SSE event reaches a stream without a valid session.
- **Every state-changing browser request** is a `POST` with a JSON body. It needs the header `X-Crew-Request: 1` and a valid `__Host-crew_session` cookie (HttpOnly, Secure, SameSite=Strict, Path=/, 12 h). If either is missing the answer is 403 or 401.
- **Operators come only from env `CREW_OPERATORS`** (JSON `[{"id":"owner","name":"…"}]`; ids match `^[a-z][a-z0-9-]{1,15}$`; at most 8). An operator removed from the list loses every session at the next request.
- **Invites come from env `CREW_INVITES_DIR`**, a mounted Secret directory with one file per operator id whose content is the code. They are single-use: used-code SHA-256 hashes go in `invites-used.json`. Codes are compared in constant time. An operator can hold at most 5 passkeys.
- **RP ID** comes from env `PUBLIC_ORIGIN` (`https://crew.prd.jdwlabs.com`). Use the host for the RP ID and the origin for WebAuthn's allowed origins.
- **State dir** is env `DATA_DIR` (default `/data`). It holds `session.key` (32 random bytes, created on first start, mode 0600), `passkeys.json` and `invites-used.json`. Writes go to a temp file and are then renamed.
- **The unit work API** is `GET /v1/units/{id}/work?wait=<1..25>s` (204 on timeout) and `POST /v1/units/{id}/events`.
  - Bearer token from env `WORK_TOKEN`, compared by SHA-256 plus constant-time compare, as in `minecraft/crew/internal/task/api.go`.
  - With `WORK_TOKEN` empty, the routes are not registered.
  - A request carrying `X-Forwarded-For` gets 404 (the public gateway always sets it).
  - An unknown unit id gets 404.
- **Redelivery.** A delivered item with no event within 30 s is handed out again. The unit ignores ids it already holds.
- **Commands.**
  - `stop` cancels the unit's running task and its queued ones, and is delivered before any queued task.
  - `goto` is a task template: `{"template":"goto","to":{"x":…,"y":…,"z":…}}`, with x and z in −500..499 and y in −64..320. Anything outside gets 400.
- **History.** The last 50 items per unit. Rows older than 30 days are deleted on start and every 24 h.
- **Postgres** is configured by env `PG_HOST`, `PG_PORT`, `PG_DATABASE`, `PG_USERNAME`, `PG_PASSWORD`, as in the agent (`minecraft/agent/internal/authcache/postgres.go`). If Postgres is unset or down, the view-only console still works and the command and work endpoints answer 503.
- **CPU.** crew-control stays at 50m. Long polls are idle connections, and no per-poll goroutine leaks.
- **Code style.** Comments say why, never what. Conventional Commits. Trailers: `Co-Authored-By: Claude <Model> <noreply@anthropic.com>` for the model that wrote the commit, plus `Assisted-by: Claude Code:<model-id>`.

## Review Focus

1. **A replayed or cross-site request.** A POST without `X-Crew-Request`, with a stale cookie, or with a cookie for an operator removed from `CREW_OPERATORS` → 401/403 and no work item created. Tested in Task 6.
2. **A unit restart mid-task.** The unit comes back with an empty runner while crew-control still shows the item running. The 30 s redelivery hands it out again. The unit runs it, or reports `failed` if it can't, and no item stays `running` forever. Tested in Task 4 (store) and Task 3 (unit).
3. **`stop` while the unit has queued items it received but crew-control thinks are queued.** Every non-terminal item for that unit ends `cancelled` on both sides. Tested in Tasks 3 and 4.
4. **A reused or wrong invite**, or a sixth passkey → refused with no state change. Tested in Task 5.
5. **The public route reaching the unit work API.** `/v1/units/{id}/work` and `/events` stay unrouted (chart test), and `X-Forwarded-For` → 404 in the app (Task 4).

---

### Task 1: Flyway V9 — the crew work tables (repo `jdwlabs/platform`)

**Files:**
- Modify: `tenants/jdwillmsen/services/jdwillmsen-schemas/postInstall/migrations-configmap.yaml` (append `V9__minecraft_crew_work.sql`)

- [ ] **Step 1:** Work in a worktree of `~/projects/jdwlabs/platform` on branch `feat/JDW-116-crew-work-tables` from `origin/main`. Read the repo's `CLAUDE.md`/`AGENTS.md` and its tests (`tests/`, `tools/tests`) for migration checks.
- [ ] **Step 2:** Append V9, in the same comment style as V8.

  `minecraft.crew_work`:
  - `id UUID PRIMARY KEY`
  - `unit TEXT NOT NULL`
  - `kind TEXT NOT NULL CHECK (kind IN ('task','command'))`
  - `body JSONB NOT NULL`
  - `state TEXT NOT NULL CHECK (state IN ('queued','delivered','running','done','failed','cancelled'))`
  - `progress INT NOT NULL DEFAULT 0`
  - `reason TEXT NOT NULL DEFAULT ''`
  - `operator TEXT NOT NULL`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `delivered_at TIMESTAMPTZ`
  - Index on `(unit, state, created_at)`.

  `minecraft.crew_work_event`:
  - `id BIGSERIAL PRIMARY KEY`
  - `work_id UUID NOT NULL REFERENCES minecraft.crew_work(id) ON DELETE CASCADE`
  - `state TEXT NOT NULL`
  - `progress INT NOT NULL`
  - `reason TEXT NOT NULL DEFAULT ''`
  - `at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - Index on `(work_id, at)`.

  A comment notes that both tables fall under V2's default privileges for `app`.
- [ ] **Step 3:** Run the repo's lint and tests (yamllint, plus any migration test). Expected: pass.
- [ ] **Step 4:** Commit `feat(jdwillmsen-schemas): add the crew work tables`. Push, open the PR, follow the repo's PR rules (check for an approval gate first), merge when green, and confirm the Flyway job ran: `kubectl get jobs -A | grep -i flyway` and its logs show V9 applied.

### Task 2: crew — the `goto` template and runner hooks for an external controller

**Files:**
- Modify: `minecraft/crew/internal/task/template.go` and `template_test.go` (`goto`)
- Modify: `minecraft/crew/internal/task/runner.go` and `runner_test.go` (`SubmitAs`, observer)
- Modify: `minecraft/crew/internal/task/recipes.go` or the skill factory in `cmd/crew/wiring.go`, wherever templates map to skills (`goto` gives one `skill.NewNavigate(world, self, goal, 1.5)`)

**Interfaces (produces):**
- `Template.To *Chest` is reused for `goto` (`{"template":"goto","to":{x,y,z}}`). `ParseTemplate` rejects `goto` without `to` and anything out of bounds (x,z −500..499, y −64..320). It also rejects other fields on a `goto`.
- `func (r *Runner) SubmitAs(id string, t Template) (Task, error)`:
  - Same as `Submit`, but with the caller's id.
  - Returns `ErrDuplicate` (new) if the id is already held.
  - `id` must match `^[0-9a-f-]{8,64}$`.
- `func (r *Runner) Observe(f func(Task))`. It sets one observer, called (outside `r.mu`) after every state or progress change, with the task copy. Wire it from the same places `logEvent` is called.

- [ ] **Steps:**
  - TDD each piece.
  - Template tests: a valid `goto`, a missing `to`, out-of-bounds values on each axis, and an extra field.
  - Runner tests: `SubmitAs` duplicate, a bad id, and the observer seeing queued, running and done for a task, plus cancelled on `Cancel`.
  - Run `go test -race ./minecraft/crew/...`, gofmt and vet.
  - Commit `feat(crew): goto template and hooks for an external controller`.

### Task 3: crew — `internal/control`, the work poll loop

**Files:**
- Create: `minecraft/crew/internal/control/client.go` and `client_test.go`
- Modify: `minecraft/crew/internal/config/config.go` (env `CONTROL_URL`, `CONTROL_TOKEN`; both or neither, else a config error)
- Modify: `minecraft/crew/cmd/crew/main.go` and `wiring.go` (start the client when configured)

**Interfaces:**
- Consumes `Runner.SubmitAs`, `Runner.Cancel`, `Runner.List` and `Runner.Observe` from Task 2.
- Wire format, fixed. crew-control (Task 4) implements the other side.
  - `GET {CONTROL_URL}/v1/units/{CREW_ID}/work?wait=25s` with `Authorization: Bearer <token>`. The answer is 204, or 200 with one of:
    - `{"id":"<uuid>","kind":"task","template":{…}}`
    - `{"id":"<uuid>","kind":"command","command":{"name":"stop"}}`
  - `POST {CONTROL_URL}/v1/units/{CREW_ID}/events` with `{"id":"<uuid>","state":"running|done|failed|cancelled","progress":n,"reason":"…"}`. The answer is 204.
- `func New(base *url.URL, unit, token string, r Runner, log Logger) *Client`, where `Runner` is a small interface over the four methods. `func (c *Client) Run(ctx context.Context)`.
- Behaviour:
  - **Task item.** `SubmitAs(id, template)`.
    - `ErrDuplicate`: ignore (redelivery).
    - Parse or submit error: post `failed` with the reason.
  - **`stop` command.** `Cancel` every queued or running task, then post `done` for the command id. Each cancelled task reports `cancelled` through the observer.
  - **Observer.** Posts an event for each change of a task whose id came from control. Tasks submitted locally through `/v1/tasks` are not reported.
  - **Event post failure.** Keep the latest per id and retry on the next loop. Never block the runner.
  - **Poll errors and non-2xx.** Back off 1 s, doubling to 30 s, and reset on success. Log `control_unreachable` once per outage, not every try.

- [ ] **Steps:**
  - TDD against an httptest fake crew-control:
    - a task delivered then done;
    - a redelivered id ignored;
    - a stop cancelling a queued and a running task, with events in order;
    - a bad template posting `failed`;
    - back-off on 500 (inject the clock or sleep func);
    - no events for local tasks.
  - Config test for the both-or-neither rule.
  - Run `go test -race ./minecraft/crew/...`.
  - Commit `feat(crew): pull work from crew-control`.

### Task 4: crew-control — work store and the unit work API

**Files:**
- Create: `minecraft/crew-control/internal/work/store.go` (interface + Postgres), `memstore.go` (in-memory implementation used by tests and when Postgres is unset), `store_test.go` (runs the same suite against memstore; against Postgres only if `CREW_PG_TEST_DSN` is set)
- Create: `minecraft/crew-control/internal/work/api.go` and `api_test.go` (unit-facing routes)
- Modify: `minecraft/crew-control/cmd/crew-control/main.go` (Postgres config, store, routes)

**Interfaces (produces):**

```go
type Item struct {
    ID uuid; Unit, Kind string; Body json.RawMessage; State string
    Progress int; Reason, Operator string; Created, Updated time.Time
}
type Store interface {
    Enqueue(ctx, unit, kind string, body json.RawMessage, operator string) (Item, error)
    // Next returns the oldest command, else the oldest task, that is queued
    // or delivered more than redeliverAfter ago, and marks it delivered.
    Next(ctx, unit string, now time.Time) (Item, bool, error)
    Apply(ctx, unit string, id uuid, state string, progress int, reason string) (Item, error) // ErrNotFound, ErrWrongUnit
    CancelOpen(ctx, unit string) ([]Item, error) // marks every non-terminal item of unit cancelled
    Recent(ctx, unit string, n int) ([]Item, error)
    Prune(ctx, before time.Time) (int, error)
}
```

- Use `github.com/google/uuid` if it is already in `go.mod`. Otherwise use a 16-byte `crypto/rand` hex in UUID format, with no new dependency.
- Terminal states (`done`, `failed`, `cancelled`) never change again: `Apply` on a terminal item is a no-op that returns it.
- `api.go` provides `func Routes(mux *http.ServeMux, s Store, units roster.Roster, token string, notify func(unit string))`.
  - `GET /v1/units/{id}/work`: loops on `Next`, waiting on a per-unit wake channel or a timeout up to `wait`. It returns the item as the Task 3 wire format: `task` items carry `template`, and `command` items carry `command`.
  - `POST /v1/units/{id}/events`: decodes strictly, takes a body of at most 1 KiB, and calls `Apply`, then `notify`.
  - Both routes 404 on `X-Forwarded-For` or an unknown unit, and 401 on a bad token.
- `func (a *API) Wake(unit string)` wakes a waiting poll when an operator enqueues work.

- [ ] **Steps:**
  - TDD.
  - Store suite (memstore):
    - commands before tasks;
    - FIFO;
    - redelivery after 30 s with an injected `now`;
    - terminal states sticky;
    - `CancelOpen`;
    - `Recent` order and limit;
    - `Prune`.
  - API tests:
    - a long poll returns at once when woken;
    - 204 on timeout (use `wait=1s`);
    - an event updates the item;
    - token, XFF and unknown-unit refusals;
    - a body over 1 KiB gets 413.
  - Postgres store:
    - implement it with `pgxpool`;
    - `Next` uses `SELECT … FOR UPDATE SKIP LOCKED` in a transaction;
    - write each event row to `crew_work_event` in the same transaction as the `crew_work` update.
  - Run `go test -race ./minecraft/crew-control/...`.
  - Commit `feat(crew-control): work queue and the unit work API`.

### Task 5: crew-control — operators, invites, passkeys, sessions

**Files:**
- Create: `minecraft/crew-control/internal/auth/` with these files and tests:
  - `operators.go`: `CREW_OPERATORS` parse.
  - `invites.go`: directory read, constant-time check, used-hash file.
  - `passkeys.go`: JSON store, at most 5 per operator, counter rules.
  - `sessions.go`: cookie issue and verify, key file.
  - `ceremony.go`: the WebAuthn begin and finish handlers for register and login. Ceremony state is held in memory, keyed by a random id in a short-lived `__Host-crew_ceremony` cookie, with a 5 min TTL.
- Read `minecraft/mcmap/internal/auth/{passkeys.go,ceremony.go,auth.go}` first and follow their patterns: counter handling, user handle, atomic writes. Do not import mcmap packages. Copy what fits.

**Interfaces (produces):**
- **Register:**
  - `POST /auth/register/options` `{"invite":"…","operator":"owner"}` → creation options.
  - `POST /auth/register/finish` → sets the session cookie, marks the invite used.
- **Login:**
  - `POST /auth/login/options` → discoverable-credential request options.
  - `POST /auth/login/finish` → session cookie.
- **Session:**
  - `POST /auth/logout` clears it.
  - `GET /auth/me` → `{"operator":{"id","name"}}` or 401.
- `func (a *Auth) Operator(r *http.Request) (Operator, bool)`: verifies the cookie, that the operator is still in `CREW_OPERATORS`, and that the passkey named in the session still exists.
- `func (a *Auth) Require(h http.Handler) http.Handler`: checks the method is POST, `X-Crew-Request: 1`, and the session. It answers 401 or 403 otherwise.
- `GET /signin` is a static page in the console embed (Task 7 builds it). This task serves only the JSON endpoints.

- [ ] **Steps:**
  - TDD. Use go-webauthn's test helpers, or a software authenticator like mcmap's `softkey_test.go`.
  - Tests:
    - register with a valid invite;
    - a reused invite;
    - a wrong operator for the invite;
    - a 6th passkey refused;
    - login;
    - a tampered or expired cookie;
    - an operator removed from the list → `Operator` false;
    - a deleted passkey → session invalid;
    - `Require` without the header → 403;
    - files written 0600 via rename.
  - Run `go test -race`.
  - Commit `feat(crew-control): operator sign-in with invited passkeys`.

### Task 6: crew-control — operator work API and live updates

**Files:**
- Create: `minecraft/crew-control/internal/work/operator.go` and `operator_test.go`
- Modify: `minecraft/crew-control/internal/telemetry/sse.go` (optional `work` event for authenticated streams)
- Modify: `main.go`

**Interfaces:**
- All routes are behind `Auth.Require`, or behind `Auth.Operator` for the GET:
  - `POST /v1/work/{unit}/tasks` takes a template body. It is validated by a crew-control copy of the bounds rules. A full template check is the unit's job, so a bad template ends `failed` with the unit's reason. It answers 202 with the Item. Then it calls `Wake`.
  - `POST /v1/work/{unit}/stop`: `CancelOpen` for the queued, not-yet-delivered items, then enqueues a `stop` command. Answers 202.
  - `POST /v1/work/{unit}/goto` takes `{"x","y","z"}` and enqueues a `goto` task.
  - `POST /v1/work/{unit}/items/{id}/cancel`: if the item is queued, it is cancelled in the store. If it is delivered or running, a `stop` is enqueued. A per-item cancel command is out of scope; reply with the resulting item.
  - `GET /v1/work/{unit}` → `Recent(50)`, operator only. Anonymous gets 401.
- Implementation update: stop now uses the atomic `StopUnit` store operation; full units poll with `kinds=command` so stop commands remain deliverable.
- SSE: `StreamHandler` gets `func(r *http.Request) bool` (authenticated?). On every store change for any unit (`notify`), authenticated streams receive `event: work` with `data: {"unit":"crew-02","items":[…Recent(50)]}`.
- Store unavailable → 503 `{"error":"queue unavailable"}`.

- [ ] **Steps:**
  - TDD.
  - Each route:
    - anonymous → 401;
    - without the header → 403;
    - a happy path;
    - bounds → 400;
    - an unknown unit → 404.
  - SSE: an anonymous stream never gets `work`; an authenticated one does after an enqueue.
  - Store down → 503 while `/` and `/v1/units/stream` still work.
  - Commit `feat(crew-control): operator commands, queue and history`.

### Task 7: Console — sign-in page and command panel

**Files:**
- Create: `minecraft/crew-control/internal/console/web/signin.html` and `signin.js`. These run the WebAuthn browser calls: `navigator.credentials.create` and `.get` with base64url helpers. An invite arrives as `#operator=…&invite=…` (a fragment, so it never reaches a server or proxy log) and is removed from the URL with `history.replaceState` before anything else runs.
- Modify: `console.go` (serve `/signin`), `web/index.html`, `web/console.js` and `web/console.css` (panel)
- Modify: `web/state.js` and its tests (pure helpers for the panel's form → template body)

**Behaviour:**
- **Signed out:** a "Sign in" link only.
- **Signed in:** the bar shows the operator's name and a "Sign out" button. A side panel for the focused unit holds:
  - Stop (one click, no confirm);
  - Go to (x, y, z number inputs, prefilled from the unit's current position in `/units/{id}/v1/state`);
  - Queue: a template select with gather cobblestone, chop oak_log, and haul (from/to chests), with qty and chest inputs;
  - the queue and history list from `work` SSE events, each open item with Cancel.
- **Keyboard:** the panel's inputs must not trigger the console's 1–9, c and w shortcuts while focused.
- **CSP:** unchanged. No inline script.

- [ ] **Steps:**
  - `node --test` for the state.js helpers (template bodies, bounds).
  - Go tests: `/signin` is served with the CSP.
  - A Playwright run against a local crew-control with memstore, a fake unit, and a virtual authenticator (`cdp WebAuthn.addVirtualAuthenticator`):
    - register by invite;
    - see the panel;
    - queue a chop;
    - the fake unit polls and reports done;
    - history shows done;
    - Stop works;
    - sign out hides the panel.
  - Commit `feat(crew-control): sign-in page and command panel`.

### Task 8: Ship gameops

- [ ] Final whole-branch review (Opus), then the fix wave.
- [ ] `/no-mistakes`, PR with the body rule, merge once green.
- [ ] Confirm the releases `crew-v…` and `crew-control-v1.1.0` (or what semantic-release picks) and that both images pull anonymously.

### Task 9: Chart (jdw-deployments) and the Secrets

**Files:** `charts/crew-lab/values.yaml`, `templates/crew-control-deployment.yaml`, a new `templates/crew-control-pvc.yaml`, a new `templates/crew-control-db-externalsecret.yaml` (copy the agent's `agent-db-externalsecret.yaml`), `templates/crew-deployment.yaml` (`CONTROL_URL` `http://crew-control:8080`, `CONTROL_TOKEN` from Secret `crew-work` key `token`, `optional: true`), `templates/crew-route.yaml`, `tools/tests/test-crew-control.sh`, `test-crew-route.sh`, and the README.

- **crew-control env:**
  - `PUBLIC_ORIGIN=https://<route.host>`
  - `CREW_OPERATORS` from `crew.control.operators` (owner and josh; display names "Jake" and "Josh")
  - `CREW_INVITES_DIR=/invites`, mounted from Secret `crew-control-invites` (`optional: true`)
  - `WORK_TOKEN` from Secret `crew-work` key `token` (`optional: true`)
  - `PG_*` from the ExternalSecret, with host, port and db as in the agent's values
  - `DATA_DIR=/data` on a PVC (truenas-nfs, 64Mi)
- **Strategy:** Recreate, because the RWO PVC rules out RollingUpdate. Comment why.
- **Route allow-list:** add Exact `/signin`, PathPrefix `/auth` and PathPrefix `/v1/work`. The test asserts `/v1/units/crew-01/work` and `/v1/units/crew-01/events` stay unrouted, even though `/v1/units` is a prefix.
  - Gateway PathPrefix `/v1/units` would match them. Change the rule to Exact `/v1/units/stream` and re-run the test.
- **README:** how to rotate an invite or the work token with `kubectl create secret … --dry-run=client -o yaml | kubectl apply -f -`, generating values with `openssl rand -hex 24` inside the command so nothing is echoed. Also how to read an invite to send it: `kubectl get secret … -o jsonpath | base64 -d` is the owner's command, not an agent's.
- [ ] **Secrets (owner-approved).** Before merging the chart, create `crew-work` (key `token`) and `crew-control-invites` (keys `owner` and `josh`) in `jdwillmsen-prd`. Generate each value inside the command, never echo or print it, and verify only that the keys exist (`kubectl get secret -o jsonpath='{.data}' | jq 'keys'`).
- [ ] Open the PR, merge when green, wait for Argo Synced and Healthy, and confirm crew-control and the units are Ready and the units log no `control_unreachable`.

### Task 10: Prove it live

- [ ] Ask the owner to enrol: give them the one command that prints the owner's invite, plus the URL `https://crew.prd.jdwlabs.com/signin#operator=owner&invite=<code>`. Agents never read invite values.
- [ ] Without any credential, prove everything an agent can on production:
  - anonymous gets 401 on `GET /v1/work/crew-01` and 401 or 403 on each POST;
  - an anonymous SSE stream carries no `work` event;
  - `/v1/units/crew-01/work` is 404 publicly;
  - every unit logs `control_connected` and no `control_unreachable`;
  - crew-control's Postgres store is reachable: `/healthz` stays 200, and the log line `work_store_ready` is present (Task 4 adds it).
- [ ] The signed-in part of the done condition (queue a chop on Basalt, watch it finish, stop Agate mid-task) needs a real passkey, which agents never mint. Hand it to the owner as a 3-step checklist: enrol, queue the chop, press Stop on Agate. Watch the store and units while they do it, and record the outcome. The Task 7 Playwright run with a virtual authenticator is the automated evidence for the same flow.
- [ ] Post the result on JDW-116 for the owner and Josh (@mention Josh Sorenson `712020:d6193b5a-6dec-476b-b165-921248cccda6`), with the invite instructions for Josh, which the owner sends.
- [ ] Move JDW-116 to Done, and update the memory `crew-smart-bots-plan.md`.
