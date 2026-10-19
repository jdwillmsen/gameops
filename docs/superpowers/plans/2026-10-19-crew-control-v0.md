# crew-control v0: public multi-unit console — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `crew.prd.jdwlabs.com` opens one view-only console where an anonymous viewer switches between all crew units and four camera modes without reloading, and opens a wall of every unit (JDW-115).

**Architecture:** A new Go service, `minecraft/crew-control`, serves the console page and reverse-proxies an allow-list of each unit's camera and state routes under `/units/{id}/…`. It also polls every unit's `/v1/state` once a second and serves one SSE stream of unit summaries. The unit's existing camera page becomes the tile: it switches to relative URLs, gains `mode` and `hud` query parameters, and takes mode changes by `postMessage`. The console frames one tile per view. The HTTPRoute moves from crew-01 to crew-control.

**Tech Stack:** Go 1.27 (stdlib `net/http`, `httputil.ReverseProxy`), the shared `minecraft/agent/pkg/logging`, plain HTML/JS for the console, and three.js as the camera page uses it today. Helm chart `charts/crew-lab` in jdw-deployments.

**Spec:** `docs/specs/2026-10-19-crew-console-and-control-design.md` (step 1, "Console v0").

## Global Constraints

- Lab only. crew-control has no Minecraft connection. It never reaches FWB.
- No coordinates in logs, metrics, Jira or PR text. Positions travel only in proxied responses and the SSE body.
- Public and view-only. **No** path through crew-control may reach a unit's `/v1/tasks`, `/metrics`, `/healthz` or `/readyz`. Proxying is an allow-list, never a deny-list.
- crew-control never forwards `Cookie` or `Authorization` to a unit.
- CPU: crew-control limit **50m**, request 10m. Memory request 32Mi, limit 128Mi. The `tenant-quota` limits.cpu is now 12 cores with 6450m used. The FWB backup headroom rule still stands: don't spend headroom you didn't measure.
- Unit ids match `^crew-[0-9]{2,}$`. Unit names are 16 characters or fewer. The roster comes from the chart's `crew.units` and nowhere else.
- No LLM, no database, no sign-in in v0.
- Comments say why, never what. Conventional Commits. Trailers: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` plus `Assisted-by: Claude Code:<model-id>` for whichever model wrote the commit.

## Review Focus

1. **Path tricks against the allow-list:** `/units/crew-01/v1/camera/../../v1/tasks`, `%2e%2e`, `//`, `/units/crew-01/v1/statex` and `/units/crew-01/v1/camera` (no trailing segment) must all be 404 with no upstream request. Tested in Task 2.
2. **A unit that is down or restarting:** the proxy answers 502 quickly (dial timeout 2 s), and the SSE stream marks that unit `online:false` within 3 s while the other units keep updating. Tested in Tasks 2 and 3.
3. **WebSocket through the proxy:** the unit's same-origin check must pass. The proxy keeps the incoming `Host` header, so `Origin` and `Host` still match at the unit. A test dials the websocket through the proxy into an httptest unit that uses `websocket.Accept` with default options. Tested in Task 2.
4. **Viewer cap reached:** the unit answers 503 on `/v1/camera/ws` once it has `maxViewers` viewers. The tile must show "viewer limit reached" and retry with back-off. The console must not crash or spin. Tested by hand in Task 4 and the live check in Task 6.
5. **Unknown unit or mode in the console URL** (`?unit=crew-99&mode=fly`): fall back to the first unit and `first`, and rewrite the URL. Tested in Task 4 (JS unit test).

---

### Task 1: Camera page works under a base path, with modes and a bare tile

Repo gameops, worktree `~/worktrees/jdwillmsen/gameops/feat/JDW-115-crew-control-v0`, branch `feat/JDW-115-crew-control-v0`.

**Files:**
- Modify: `minecraft/crew/internal/camera/web/index.html`
- Modify: `minecraft/crew/internal/camera/web/entities.js` (the two `fetchSheet` URLs only)
- Test: `minecraft/crew/internal/camera/server_test.go` (or the existing camera test file that loads the page)

**Interfaces:**
- Produces, for the console (Task 4):
  - Query parameters on `/camera`:
    - `mode` is one of `first|third|orbit|top` (default `first`).
    - `hud` is `0` or `1` (default `1`). `hud=0` hides the operator overlay and the debug text, and stops the `/v1/state` poll.
    - `r` is unchanged.
  - Message `window.postMessage({type: 'crew-camera', mode: '<mode>'})`. It is accepted only when `ev.origin === location.origin` and the mode is one of the four. It switches the mode in place, with no reconnect.
  - Default radius per mode when `r` is absent: `first` 4, `third` 4, `orbit` 6, `top` 6. An explicit `r` wins.

- [ ] **Step 1: Write the failing test.** Add `TestPageUsesRelativeURLs` in `minecraft/crew/internal/camera/server_test.go`. Serve `Mount(...)` with httptest, GET `/camera`, then GET `/v1/camera/entities.js`. Assert that neither body contains the substring `'/v1/` or `"/v1/` (a root-absolute URL), and that the page contains `new URL('v1/camera/ws'`.
- [ ] **Step 2: Run** `go test ./minecraft/crew/internal/camera/ -run TestPageUsesRelativeURLs`. Expected: FAIL.
- [ ] **Step 3: Switch every URL to a relative one.**
  - `fetch('v1/state')`, `'v1/camera/items.json'`, `import … from './v1/camera/entities.js'`, `new Worker('v1/camera/mesher.js')` and the atlas URLs.
  - The websocket URL is built as `const u = new URL('v1/camera/ws', location.href); u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'; u.search = …`.
  - A page at `/camera` still resolves to `/v1/…`. A page at `/units/crew-01/camera` resolves to `/units/crew-01/v1/…`.
- [ ] **Step 4: Run the test again.** Expected: PASS.
- [ ] **Step 5: Add camera modes in `frame()`.** Replace the fixed eye placement with `placeCamera(mode)`:
  - `first`: today's placement.
  - `third`: 4 blocks behind the eye along the yaw, 1.5 up, looking at the eye.
  - `orbit`: radius 8 around the eye. Mouse drag changes azimuth and elevation, clamped to 5°–85°. The wheel changes the radius within 3–24.
  - `top`: 40 above the eye, looking straight down, with "up" on screen along the unit's facing.

  In every mode except `first`, draw the unit itself through `ents.update({id: -1, type: 'player', x, y: me.y - 1.62, z, yaw: me.yaw, pitch: me.pitch, head: me.yaw})`. Remove it (`ents.remove(-1)`) on returning to `first`. Show the current mode in the debug text.
- [ ] **Step 6: Add `hud=0` and the `postMessage` listener** as set out in Interfaces. A `ws` close with HTTP 503 already retries. Make sure the visible status text reads `viewer limit reached` while it waits, including when `hud=0` (one small line, bottom-left).
- [ ] **Step 7: Run the whole crew test suite and lint.**
  - `go test -race ./minecraft/crew/...` must be green.
  - `test -z "$(gofmt -l minecraft/crew)" && go vet ./minecraft/crew/...` must be clean.
- [ ] **Step 8: Check the page by hand.** Run crew against the local compose lab (`minecraft/crew/lab`) and open `/camera?mode=orbit`, then `?mode=top&hud=0`. Confirm the unit's body shows, the drag works, and the page has no console errors.
- [ ] **Step 9: Commit:** `feat(crew): camera modes and base-path URLs for the console`.

### Task 2: crew-control service — roster, allow-listed proxy

**Files:**
- Create: `minecraft/crew-control/component.yaml`, a copy of crew's with these changes:
  - name `crew-control`;
  - `depends: [minecraft/agent/pkg/]`;
  - tasks scoped to `./minecraft/crew-control/...`;
  - release tag `crew-control`;
  - image `minecraft-crew-control`, with the description "Console and control plane for Crew worker units".
- Create: `minecraft/crew-control/Dockerfile`, the same pattern as crew's: a distroless nonroot runtime with no volume, `EXPOSE 8080`, and a binary at `/crew-control`.
- Create: `minecraft/crew-control/internal/roster/roster.go` and `roster_test.go`.
- Create: `minecraft/crew-control/internal/proxy/proxy.go` and `proxy_test.go`.
- Create: `minecraft/crew-control/cmd/crew-control/main.go`.
- Create: `minecraft/crew-control/README.md`, short: what it is, the env vars, and the routes.

**Interfaces:**
- `package roster`:
  - `type Unit struct { ID, Name string; URL *url.URL }`
  - `type Roster struct{ … }` with `Units() []Unit` (in the given order) and `Get(id string) (Unit, bool)`.
  - `func Parse(raw string) (Roster, error)`. It reads env `CREW_UNITS` as JSON `[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`.
  - `Parse` errors on:
    - invalid JSON or an empty list;
    - more than 26 units;
    - an id that does not match `^crew-[0-9]{2,}$`;
    - a duplicate id;
    - a name that is empty or longer than 16 runes;
    - a URL that is not `http`, has a path other than empty or `/`, or has userinfo or a query.
- `package proxy`: `func Handler(r roster.Roster, log *logging.Logger) http.Handler`. It serves only these routes:
  - `GET /units/{id}/camera` goes to upstream `/camera`.
  - `GET /units/{id}/v1/state` goes to upstream `/v1/state`.
  - `GET /units/{id}/v1/camera/{file}` goes to upstream `/v1/camera/{file}`, where `file` ∈ {`ws`, `mesher.js`, `entities.js`, `entities.png`, `entities.json`, `atlas.png`, `atlas.json`, `items.png`, `items.json`, `stats`}. This is an exact set, not a prefix.
  - Anything else gets 404 and makes no upstream request.
  - Built on `httputil.ReverseProxy`:
    - `Rewrite` sets the URL to the unit's and keeps `Out.Host = In.Host` (the reason for this goes in a comment).
    - It deletes `Cookie`, `Authorization` and the `X-Forwarded-*` the client sent, then calls `SetXForwarded`.
    - Transport dial timeout 2 s, `ResponseHeaderTimeout` 5 s (the websocket upgrade is answered immediately).
    - `ErrorHandler` answers 502 `unit unavailable` and logs `proxy_upstream_failed` with `unit` and `error`, but no path query.
- `main.go`:
  - Env: `CREW_UNITS` (required), `LISTEN_ADDR` (default `0.0.0.0:8080`), `LOG_LEVEL`.
  - Mux:
    - `/units/` goes to the proxy.
    - `/healthz` returns 200 `ok`.
    - Task 3 adds `/v1/units` and `/v1/units/stream`.
    - Task 4 adds `/` and `/static/`.
  - `http.Server` with `ReadHeaderTimeout` 5 s and no `WriteTimeout` (it would cut websockets and SSE).
  - Graceful shutdown on SIGTERM, 10 s.

- [ ] **Step 1: Write the failing roster tests.**
  - `TestParseAcceptsChartShape` covers three units, in order.
  - `TestParseRejects` is table-driven, one case per error above.
- [ ] **Step 2: Run** `go test ./minecraft/crew-control/...`. Expected: FAIL (does not compile).
- [ ] **Step 3: Implement `roster.Parse`.**
- [ ] **Step 4: Write the failing proxy tests** against an httptest "unit" that records every request it receives.
  - `TestProxyAllowList`: each allowed path returns the unit's body, and the upstream saw the stripped path.
  - `TestProxyRefuses`: each of these returns 404, and the upstream saw zero requests. The paths are `/units/crew-01/v1/tasks`, `/units/crew-01/metrics`, `/units/crew-01/healthz`, `/units/crew-01/v1/camera`, `/units/crew-01/v1/camera/ws/x`, `/units/crew-01/v1/statex`, `/units/crew-01/v1/camera/../../v1/tasks`, `/units/crew-01/v1/camera/%2e%2e/tasks`, `/units/crew-99/camera`, `/units/crew-01/camera` with method POST, and `/units//camera`.
  - `TestProxyStripsCredentials`: the `Cookie` and `Authorization` sent never reach the upstream.
  - `TestProxyUnitDown`: the unit URL points at a closed port, and the response is 502 in under 3 s.
  - `TestProxyWebSocketSameOrigin`: the upstream handler is `websocket.Accept(w, r, nil)` (default origin check), followed by an echo. Dial `ws://<proxy>/units/crew-01/v1/camera/ws` with an `Origin: http://<proxy host>` header. Assert that the dial succeeds and one message round-trips.
- [ ] **Step 5: Run the tests.** Expected: FAIL.
- [ ] **Step 6: Implement `proxy.Handler` and `main.go`.**
  - Match with Go 1.22+ `ServeMux` patterns: `GET /units/{id}/camera`, `GET /units/{id}/v1/state`, `GET /units/{id}/v1/camera/{file}`.
  - `ServeMux` cleans `..` and redirects. Make sure a cleaned redirect cannot land on a proxied path that the original request could not reach. A test covers this.
- [ ] **Step 7: Run the checks.**
  - `go test -race ./minecraft/crew-control/...` must pass.
  - `CGO_ENABLED=0 go build ./minecraft/crew-control/...` must pass.
  - gofmt and vet must be clean.
  - `go run ./tools/components validate` must pass.
- [ ] **Step 8: Commit:** `feat(crew-control): allow-listed proxy to unit cameras`.

### Task 3: Telemetry poller and SSE stream

**Files:**
- Create: `minecraft/crew-control/internal/telemetry/poller.go`, `sse.go` and `telemetry_test.go`.
- Modify: `minecraft/crew-control/cmd/crew-control/main.go` to mount the routes and start the poller.

**Interfaces:**
- `type Status struct`, with JSON field names fixed because the console reads them:
  - `ID string \`json:"id"\``
  - `Name string \`json:"name"\``
  - `Online bool \`json:"online"\``
  - `Health *float32 \`json:"health"\``: null unless `health_known` is set.
  - `Dead bool \`json:"dead"\``
  - `Task *TaskSummary \`json:"task"\``: from the unit's `tasks.current`, otherwise null.
  - `Seen time.Time \`json:"seen"\``: the last successful poll.
- `type TaskSummary struct`: `Template`, `State`, `Item string` and `Qty`, `Progress int`, with JSON names `template, state, item, qty, progress`. Position is deliberately left out; the tile has it.
- `func NewPoller(r roster.Roster, client *http.Client, every time.Duration) *Poller`.
  - `Run(ctx)` polls every unit concurrently each tick, with a per-request timeout of `every*8/10`.
  - A unit goes `Online:false` after 2 consecutive failures.
- `(*Poller) Snapshot() []Status` returns statuses in roster order.
- `(*Poller) Subscribe() (<-chan []Status, func())` delivers a snapshot only when it changed. Its buffer is 1, and it drops stale values (latest wins).
- Routes:
  - `GET /v1/units` returns the JSON snapshot.
  - `GET /v1/units/stream` is SSE. It sends `event: units\ndata: <json>\n\n` immediately, then on every change, plus a `: ping` every 15 s.
  - The stream allows at most 64 concurrent clients, and answers 503 beyond that.
  - It sets the header `X-Accel-Buffering: no`.

- [ ] **Step 1: Write the failing tests.**
  - `TestPollerMapsState` uses a fake unit returning real `/v1/state` JSON (copy a sample from crew's `statusapi` test). It covers health known and unknown, and a current task present and absent.
  - `TestPollerMarksOffline`: after the unit returns 500 twice, `Online` is false.
  - `TestStreamSendsOnChange` uses httptest plus a reader on the SSE body. The first event arrives at once; a change in the fake unit produces a second event; no change means no event within 2 ticks.
  - `TestStreamLimit`: with the 65th client, the answer is 503.
- [ ] **Step 2: Run the tests.** Expected: FAIL.
- [ ] **Step 3: Implement the poller and SSE, and wire them in main.** The tick is 1 s.
- [ ] **Step 4: Run** `go test -race ./minecraft/crew-control/...`. Expected: PASS.
- [ ] **Step 5: Commit:** `feat(crew-control): unit status stream for the console`.

### Task 4: The console page

**Files:**
- Create: `minecraft/crew-control/internal/console/console.go`. It provides `func Mount(mux *http.ServeMux)`, which serves `GET /{$}` (index.html) and `GET /static/{file}` from `embed.FS`. It sets `X-Content-Type-Options: nosniff`, sets `Cache-Control: no-cache`, and sets a CSP: `default-src 'self'; frame-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'`.
- Create: `minecraft/crew-control/internal/console/web/index.html`, `web/console.js` and `web/console.css`.
- Create: `minecraft/crew-control/internal/console/web/state.js`, holding the pure URL and state logic so it can be tested.
- Test: `minecraft/crew-control/internal/console/console_test.go` covers the served files and CSP. `web/state.test.mjs` runs with `node --test`. Append it to `component.yaml`'s test task (`go test -race ./minecraft/crew-control/... && node --test minecraft/crew-control/internal/console/web/`). CI's component job only runs `setup-node` for node-language components, but the `ubuntu-latest` image already has Node, so this works. Confirm it in the PR's CI log.

**Interfaces:**
- Consumes:
  - From Task 1: the tile URL `/units/{id}/camera?mode=&hud=&r=`, and `postMessage({type:'crew-camera', mode})`.
  - From Task 3: `GET /v1/units/stream` with event `units` carrying `Status[]`.
- `state.js` exports:
  - `parse(search, units)` returns `{unit, mode, view}`. `view` is `focus` or `wall`. An unknown unit falls back to the first unit; an unknown mode falls back to `first`; an unknown view falls back to `focus`.
  - `format({unit, mode, view})` returns a search string.
- Behaviour:
  - **Unit picker.** A top bar shows one button per unit (name, then a health and task badge from SSE). Keys `1`–`9` pick a unit.
  - **Camera mode.** Four mode buttons (First, Third, Orbit, Top). Key `c` cycles through them. A mode change sends a `postMessage` to the focused iframe and does not reload it.
  - **Switching units** changes the single focus iframe's `src` to `/units/{id}/camera?mode=<mode>`.
  - **Wall.** `w` toggles the wall: a grid with one iframe per unit at `?mode=first&hud=0&r=2`, each with an overlay label (name, health, task, or offline). Clicking a tile switches to focus on that unit. In focus view the wall iframes are removed, not hidden, so their sockets close.
  - **The URL** is kept in sync with `history.replaceState(null, '', '?' + format(state))`. It is never reloaded.
  - **Lost SSE.** If the stream drops, the bar shows "status offline" and `EventSource` reconnects on its own.
  - **Old bookmarks.** The proxy mux also serves `GET /camera` as a 302 to `/`.

- [ ] **Step 1: Write the failing tests.**
  - `state.test.mjs` round-trips `parse(format(s)) == s` for every unit × mode × view, and checks the fallbacks (Review Focus 5).
  - `console_test.go` checks that `/` returns HTML with the CSP header, that `/static/console.js` has a JS content type, and that `/static/../go.mod` returns 404.
- [ ] **Step 2: Run the tests.** Expected: FAIL.
- [ ] **Step 3: Implement the console files, and mount them in main.**
- [ ] **Step 4: Run** `go test -race ./minecraft/crew-control/...` and `node --test minecraft/crew-control/internal/console/web/`. Expected: PASS.
- [ ] **Step 5: Run it locally end to end.**
  - Start compose crew-01..03 (`minecraft/crew/lab`) and run crew-control with `CREW_UNITS` pointing at `localhost:8081-8083`.
  - In Playwright:
    - switch units with `2` and `3`;
    - cycle the modes;
    - open the wall;
    - click a tile;
    - confirm with `performance.getEntriesByType('navigation').length === 1` that there was no reload;
    - confirm the console has no errors.
  - Save a screenshot of the wall for the PR.
- [ ] **Step 6: Commit:** `feat(crew-control): console with unit picker, camera modes and wall`.

### Task 5: Ship gameops, then the chart

1. **gameops.** Push the branch and open the PR with `/no-mistakes`. Title: `feat(crew-control): public multi-unit console (v0)`.
   - Body per the PR rule, plus a *Needs attention* note on `proxy.go` (allow-list and Host preservation).
   - Merge on green with every review thread resolved, using rebase merge.
   - Confirm that semantic-release tagged `crew-v<next>` and `crew-control-v1.0.0`, and that both images are published.
   - Confirm `ghcr.io/jdwillmsen/minecraft-crew-control` is pullable anonymously: `docker manifest inspect` with no login. If it is private, make it public to match the other images, and say so.
2. **jdw-deployments.** Use a new worktree, branch `feat/JDW-115-crew-control-chart`.
   - Bump `crew.image.tag` to the new crew release.
   - Add `crew.control`:
     - `enabled: true`;
     - image `ghcr.io/jdwillmsen/minecraft-crew-control` at `1.0.0`;
     - port 8080;
     - resources as in Global Constraints.
   - New templates:
     - `crew-control-deployment.yaml`: replicas 1, RollingUpdate, the same pod and container securityContext as crew, `readOnlyRootFilesystem`, liveness and readiness on `/healthz`. `CREW_UNITS` is rendered with `toJson` from `crew.units`, mapping each to `{id, name, url: "http://<id>:<statusPort>"}`, and **skipping units with `replicas: 0`**.
     - `crew-control-service.yaml`: a ClusterIP Service named `crew-control`.
   - `crew-route.yaml`: the backend becomes `crew-control`.
     - Allow-list rules: Exact `/`, Exact `/camera` (redirected by the app), PathPrefix `/units`, PathPrefix `/v1/units`, PathPrefix `/static`.
     - Drop the `/` → `/camera` redirect.
     - Drop `crew.route.unit` and its `fail` check.
   - Update `tools/tests/test-crew-route.sh`:
     - The backend must be `crew-control` on every rule.
     - Routed: `/`, `/units/crew-01/camera`, `/v1/units/stream`, `/static/console.js`.
     - Not routed: `/v1/tasks`, `/metrics`, `/healthz`, `/readyz`, `/v1/state` (the unit's top-level state route no longer exists publicly).
   - New `tools/tests/test-crew-control.sh`. It renders the chart and asserts:
     - `CREW_UNITS` parses as JSON with three entries in chart order;
     - a unit given `replicas: 0` via `--set` is omitted;
     - crew-control's CPU limit is 50m or less.
   - README "Joining the lab" and units section: the console URL, keys `1`–`9`, `c` and `w`.
   - Open the PR (check the base branch's approval gate per the PR rule), get it green, review it, and merge with a rebase merge.

### Task 6: Prove it live and measure

- [ ] Argo `jdwillmsen-crew-lab-prd` is Synced and Healthy, crew-control is Ready, and the three units are on the new crew tag.
- [ ] With Playwright against `https://crew.prd.jdwlabs.com` (from the devbox use `--resolve crew.prd.jdwlabs.com:443:192.168.1.199`, or Playwright's `--host-resolver-rules`):
  - switch Agate, Basalt and Cobble;
  - cycle all four modes;
  - open the wall and click a tile;
  - confirm one navigation entry, i.e. no reload.
- [ ] Confirm that `curl -s -o /dev/null -w '%{http_code}' https://crew.prd.jdwlabs.com/units/crew-01/v1/tasks` returns 404, and that `/metrics` is also 404.
- [ ] **Measure.** Hold the wall plus one focused orbit view open for 5 minutes and record:
  - each unit's CPU every 15 s (`kubectl top pod`);
  - each unit's `container_cpu_cfs_throttled_periods_total` delta from cAdvisor, either through `kubectl get --raw /api/v1/nodes/<node>/proxy/metrics/cadvisor` or the platform's Prometheus if that is easier;
  - crew-control's CPU.

  The pass condition is no throttled periods on any unit, and crew-control under 50m. Put the table in the jdw-deployments PR (or the gameops PR, if it is still open) and on JDW-115.
- [ ] Post the result on JDW-115 for the owner and Josh (@mention Josh Sorenson, accountId `712020:d6193b5a-6dec-476b-b165-921248cccda6`), written for a reader with no context. Then move JDW-115 to Done and update the memory note `crew-smart-bots-plan.md`.
