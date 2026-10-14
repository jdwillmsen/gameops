# Admin access to the production map

Status: proposed 2026-10-14. Nothing is built. Each decision below is
numbered so that it can be approved or rejected on its own.

## What is wanted

The owner, in his words: "a secret way to login for admin wise or like an
override, maybe through the Dex login API or something? So that like, if I
need to debug, you need to debug, we can log in and see the production
instance." And that it be designed, built, reviewed and shipped without
him.

That is two people with different needs:

1. **The owner.** To open the production map logged in when something is
   wrong, ideally from a phone or a laptop with no game running, and to be
   seen by the map as its admin.
2. **AI agents.** To find out what the production map is doing when a bug
   is reported.

## Limits this works inside

- **Dex needs the owner.** Dex is bundled with Argo CD at
  `argocd.jdwlabs.com/api/dex`, with GitHub and Google connectors and a
  static admin user; Headlamp is already a client of it (platform ADR
  0003). A new client is a change under `tenants/platform/`, which
  CODEOWNERS makes a required code-owner approval with no bypass. So Dex
  cannot be the first phase.
- **No new secret from an agent or a person.** Agents cannot write to
  Vault, and nothing that mints, prints or exchanges a credential runs in
  an agent's shell. Secrets the cluster generates and nobody sees (the
  External Secrets `Password` generator, as `map-token-externalsecret.yaml`
  and `map-checks-externalsecret.yaml` already use) are allowed.
- **The namespace is not enforced.** `tenants/jdwillmsen/tenant.yaml` sets
  `networkPolicy: default` with no `enforce: true`, so the envelope
  renders allow-all policies and every pod in the cluster can reach every
  port of the map pod, the internal listener included.
- **No identity in git.** No gamertag, XUID, name tag, real coordinate or
  seed in code, tests, fixtures, docs, commits or chart values. An owner's
  XUID in a chart value is not acceptable.
- **GitOps only.** No `kubectl` changes to production. `gameops` and
  `jdw-deployments` both require 0 approvals on `main` (checked
  2026-10-14 through the rulesets API), so map and chart changes need
  nothing from the owner.
- **The standing rule on agent runs.** Each run of the browser checks
  from an agent's machine against production needs the owner's approval
  that time (README, "Who has to approve what"). This design does not
  change that rule. Decision D9 asks whether he wants to, later.

## What was found by reading the code

- The map already holds `BRIDGE_URL` and `BRIDGE_TOKEN`
  (`internal/config/config.go`), for snapshots and the live layer.
- The console bridge already serves `GET /permissions` behind that token
  (`minecraft/bridge/http.go`), reading the server's own `permissions.json`
  off the game's data volume, which it mounts read-only. It answers a map
  of XUID to `operator`, `member` or `visitor`.
- The agent already decides who may run operator commands from exactly
  that answer, cached for 10 seconds, failing closed to `visitor` on any
  error (`agent/internal/adapters/permissions.go`).
- `permissions.json` is not in git and not rendered by the chart. The
  nightly backup copies it beside the world.
- The bridge's command allowlist has no `op` or `permission` command, so
  the bridge token cannot change who is an operator.
- A session is a signed cookie carrying `Identity{XUID, Gamertag}`, issued
  for 7 days by default (`SESSION_TTL`). Revocation is per XUID, "every
  session issued up to this moment", on the data volume
  (`internal/auth/revoke.go`).
- Every route goes through `route` with a gate (`gatePublic`,
  `gatePlayer`, `gateViewer`, `gateAgent`, `gateExchange`), and
  `service_test.go` fails on any route registered another way.
  `auth.FromContext` answers "no player" for a service session, so
  anything keyed by XUID is out of its reach by construction.
- `GET /api/diagnostics` is on the **public** listener behind
  `gateViewer`. Without a session it cannot be read. The internal listener
  has only `/healthz`, `/metrics`, the agent's routes and the checks'
  exchange.
- The map already logs every login with XUID and gamertag
  (`map login issued`, `internal/server/auth.go`).
- `worker.Status.Problem` is only a stage name, `snapshot` or `render`;
  the detail goes to the log.
- The world runs with `allowCheats: false` (chart values), so what an
  operator can do in chat is narrower than on a cheats-on server.
  Unverified: which commands an operator can still run there, and whether
  an operator can `/op` another player with cheats off.
- **The kube credential agents use on the devbox is `system:masters`**
  (`kubectl auth whoami`, 2026-10-14). An agent can technically read every
  Secret in the namespace, exec into the map and read `session.key`. So
  "agents never hold a credential" is a rule of conduct, not something the
  cluster enforces. This design never requires an agent to touch a
  credential; it cannot prevent one from doing so.

## Decisions

### D1. An admin is an operator in the game's own `permissions.json`

A logged-in player is the map's admin while the bridge's `GET /permissions`
says `operator` for their XUID. Nothing else makes an admin. The map asks
on every admin request, through a 10-second cache, as the agent does. Any
error, a missing file, an absent XUID, `member`, `visitor` or an unknown
word all mean "not an admin".

Why:

- **No identity in git.** The truth is the game's own file on the game's
  own volume, already backed up nightly, which the owner already curates.
- **No new secret and no new wiring.** The map already has the bridge
  token; the bridge already serves the file; the agent already trusts it.
- **One list for the game and the map.** Whoever the owner makes an
  operator for the game is an operator for the map, and taking it away in
  one takes it away in both, within 10 seconds.
- **Spoofing.** The XUID comes from the session, which comes from the code
  flow (the agent reports the XUID the server stamped on the chat packet;
  the gamertag is never trusted) or from a passkey bound to that XUID (D3).
  The answer comes from the bridge over the cluster network. Faking it
  takes a write to the Service or the pod, which is cluster-admin, and
  cluster-admin can rewrite `permissions.json` itself.

What this does not settle: every operator is an admin. If the owner has
made friends operators, they see what D5 shows too. D5 is chosen so that
this is harmless (nothing in it is about the world or another player), but
the owner-only views on the roadmap (underground views) need something
narrower. See open question Q1.

Rejected for who is admin:

- **A chart value naming the owner.** An XUID in git, which is not
  allowed. A hash of one is no better: XUIDs are short numbers, and the
  hash is found by trying them.
- **The first player to claim "owner" in the map.** Whoever gets there
  first wins, including someone holding the internal token, and nothing
  in the map can tell the real owner from them.
- **A value in an existing Secret.** Someone has to write it, which is a
  person with Vault access or `kubectl`, and it is a second list of
  operators to keep in step with the game's.
- **Bedrock's `default-player-permission-level`.** Read by nobody here,
  and only matters for XUIDs absent from the file; absent is "not admin".

### D2. Admin is a gate, not a kind of cookie

A new gate, `gateAdmin`, is a player's session (`Require`) plus D1's check
on `auth.FromContext`'s XUID. The cookie says nothing about being an
admin: a cookie issued while someone was an operator stops opening admin
routes the moment the file says otherwise. A service session has no XUID
and so can never be an admin, by the same construction that keeps it from
waypoints. `service_test.go` learns the new gate and asserts that every
`gateAdmin` route answers 403 to a service session and 403 to a player the
fake bridge calls `member`.

`/api/me` gains `admin: true` for an operator, so the page can show what
only an admin has. Nothing else in a viewer's answers changes for an
admin.

### D3. Phase 1: passkeys, registered while logged in through the game

The owner gets in without the game with a passkey (WebAuthn) that the map
itself checks. Nothing is configured, no client is registered anywhere,
and no secret exists that a person or an agent has to hold: the map
stores only public keys.

**Registering one.**

1. The owner logs in the normal way, typing `!map <code>` in the game.
2. If he is an operator, the page offers "Add a passkey" for the first 10
   minutes of that session and no later. The session has to have come from
   a game code: a session that came from a passkey cannot add another, so
   one stolen passkey session cannot plant more.
3. `POST /auth/passkey/register/options` (admin gate) returns creation
   options: a 32-byte random challenge, the RP ID (the map's host,
   `fwb.prd.jdwlabs.com` from `map.host`), the user handle (32 random bytes
   kept beside the XUID, never the XUID itself), `residentKey: required`,
   `userVerification: required`, `attestation: none`, ES256 and RS256.
   The challenge is held server-side, single use, for 2 minutes, bound to
   an `HttpOnly`, `SameSite=Strict`, `__Host-` cookie, in a bounded table
   like the login codes.
4. The browser creates the credential; `POST
   /auth/passkey/register/finish` (admin gate) verifies it as §7.1 of the
   WebAuthn spec says: the challenge matches (step 8), the origin is the
   map's exact origin (step 9), the RP ID hash matches (step 14), user
   presence (step 15) and user verification (step 16) are set, and the
   credential ID is not registered to anyone (step 26).
5. The map stores `{credential ID, public key, sign count, user handle,
   XUID, created at, last used at, a label the owner types}` in
   `DATA_DIR/auth/passkeys.json`, written whole as `revoked.json` is. At
   most 5 per XUID.

**Logging in with one.** The login screen gains "Log in with a passkey",
shown only when the map has passkeys switched on. It is a discoverable
credential, so there is nothing to type: `POST /auth/passkey/options`
returns a challenge with no allowed list, the browser asks the
authenticator, and `POST /auth/passkey/finish` verifies the assertion
(origin, RP ID hash, UP, UV, signature), finds the credential, and then
issues a session only if all of these hold:

- the credential's XUID is an operator now (D1);
- the credential was created after that XUID's last revocation (so
  `!map logout` ends passkeys as well as sessions, D7);
- the sign count, where the authenticator keeps one, has moved forward.
  A synced passkey reports 0 every time, which is allowed; a counter that
  goes backwards is refused and logged, as §6.1.1 suggests a clone
  (the spec leaves the response to the relying party).

The session is an ordinary player session with one more signed field,
`m: "passkey"` (absent means a game code, so old cookies read as they
always did), and lasts 12 hours, not 7 days. The method is what step 2
above reads.

**Why passkeys for phase 1.**

- Everything is inside the map: no Dex client, no Vault, no chart secret.
- The owner's phone or laptop already has a passkey provider with a
  screen lock, so `userVerification: required` costs him nothing.
- A passkey cannot be phished onto another site (the RP ID binds it to
  this host, glossary "Relying Party Identifier"), and the map holds
  nothing that would let anyone log in if the volume leaked.
- Bootstrapping uses the proof the map already trusts, being in the game,
  and recovery is the same: if every passkey is lost, log in through the
  game and add a new one.

**Limits, said plainly.**

- A port-forward is `localhost`, a different origin, so passkeys work only
  on the public address.
- The map's volume is not backed up. Losing it loses the passkeys; the
  owner adds them again through the game.
- **New dependency.** Verifying WebAuthn by hand (CBOR, COSE keys,
  attestation parsing) is where mistakes are made. Recommended:
  `github.com/go-webauthn/webauthn`, pinned, its version reviewed like
  any other. The alternative is a hand-written verifier for ES256 with
  `none` attestation only, about 400 lines plus tests. Open question Q2.

Rejected for phase 1:

- **Long-lived owner sessions** (a 90-day cookie for operators). Still
  needs the game to start, and turns one stolen cookie into three months
  of access with no second factor.
- **A recovery code or admin password.** A credential that someone has to
  create, see and keep, and a thing to guess at on the public listener.
- **A magic link sent out of band.** There is no mail or chat channel to
  send it on that is not itself a new credential.

### D4. Phase 2, optional and owner-gated: log in through Dex

Phase 2 registers the map with the platform's Dex as a **public client
using PKCE**, so that it, too, needs no secret: Dex documents
`public: true` clients with no secret, and PKCE has been in Dex since
2.26 (secondary source: the Dex issue that introduced it; check against
the running version before relying on it). The platform change is one
`staticClients` entry in `tenants/platform/services/argo-cd/values.yaml`:
id `mcmap`, `public: true`, redirect URI
`https://fwb.prd.jdwlabs.com/auth/oidc/callback`. That is the one thing
that needs the owner's approval.

The map does not trust an email. The Google connector accepts any Google
account on the internet (the platform's own comment says so), so who logs
in is decided by the map, fail closed: an operator, while logged in
through the game, presses "Link Dex login", goes through Dex once, and the
map stores the pair `(issuer, sub)` beside his XUID on the volume. A later
Dex login whose `(issuer, sub)` is linked is that XUID, still subject to
D1 and to revocation like a passkey. No email and no XUID go in git.

What it adds over phase 1:

- the owner's GitHub or Google account, with whatever second factor it
  already has, as a second way in;
- a way in when every passkey is lost and the game is not to hand;
- the same identity he uses for Argo CD and Headlamp.

Why it is optional: phase 1 already gets him in without the game, and
phase 2 adds a dependency on Argo CD's Dex being up and a platform change.
`github.com/coreos/go-oidc/v3`, already an indirect dependency of this
module, verifies the ID token.

### D5. What an admin can do in phase 1

Small, read-only, and nothing about the world that every player cannot
already see. The owner plays this world too: an admin view that showed
more of it than other players see would be the cheat the login exists to
prevent.

1. **See the map as any player does**, which an operator already can.
2. **The server's status in the page.** `GET /api/admin/status` (admin
   gate) returns the same payload as D8's internal status: the worker's
   stage and timings, generations and chunk-census counts, the live
   layer's freshness, bridge reachability, history capture state, the
   effective non-secret settings, and counts of sessions issued and
   refused. The debug panel shows it in an Admin section. It holds no
   gamertag, XUID, coordinate, seed or path.
3. **Their own passkeys:** list (label, created, last used), add (D3),
   remove. `GET`, `POST` and `DELETE` under `/api/admin/passkeys`, admin
   gate, each acting only on the caller's own XUID.
4. **Log out everywhere** for themselves: the same revocation `!map
   logout` makes, from the page. `POST /api/admin/logout-all`, admin gate,
   own XUID only.

Rejected for phase 1:

- **"View as" a player.** The only things that differ by player are their
  own waypoints, their standing in a village and which head is "me".
  Seeing another player's waypoints is reading their private notes; it
  helps with no bug that a fixture cannot reproduce, and it makes every
  admin request ambiguous in the log about whose data it touched. If a bug
  ever needs it, it should be its own design, with the player told in
  game.
- **Withheld structures, caves, seeds, other players' trails beyond what
  everyone sees.** Not admin powers in phase 1. The underground view on
  the roadmap is the owner's to decide (Q1).
- **Anything that writes to the world or the game**, including the chunk
  reset approvals on the roadmap. That design will add its own admin
  routes behind this gate.
- **A list of who logged in.** It is in the log already, and putting it
  in the page makes a list of players and their habits for every
  operator.

### D6. Audit and metrics

Every admin-relevant event is one log line, and a count. The lines carry
the XUID, as the existing login line already does, and **not** the
gamertag, a coordinate, the challenge, the credential's public key or the
cookie. A credential is named by the first 8 hex digits of the SHA-256 of
its ID. A user agent is cut as `said` already cuts it.

| Event | Log line | Metric |
|---|---|---|
| Passkey added | `map passkey registered` xuid, credential, label length, method of the session | `mcmap_admin_passkeys_registered_total{result}` |
| Passkey removed | `map passkey removed` xuid, credential | `mcmap_admin_passkeys_removed_total` |
| Passkey login | `map login issued` (the existing line) with `method=passkey`, credential | `mcmap_admin_logins_total{method,result}` with `result` one of `ok`, `bad_assertion`, `unknown_credential`, `not_operator`, `revoked`, `counter`, `limited` |
| Admin route used | `map admin request` xuid, route pattern, allowed | `mcmap_admin_requests_total{route,allowed}` |
| Operator lookup failed | `map operator lookup failed` error class | `mcmap_admin_operator_lookups_total{result}` |
| Passkeys held | none | `mcmap_admin_passkeys` gauge |

Alerts, in the chart beside the map's other rules:

- **A passkey was added.** Any increase of
  `mcmap_admin_passkeys_registered_total{result="ok"}`. It should happen a
  few times a year; each one is worth a look, and it is the signal for the
  internal-token abuse in the threat model.
- **Passkey logins refused**, more than a handful an hour.
- **Operator lookups failing** for 15 minutes (admins are locked out, and
  the bridge is probably down too).

### D7. Revocation and turning it off

| To end | Do | Takes effect |
|---|---|---|
| One person's admin | Remove them as operator in the game | Within 10 s, every session |
| One person's sessions and passkeys | `!map logout` in game, or Log out everywhere in the page | At once |
| One passkey | Remove it in the page | At once |
| Passkey logins for everyone | `map.admin.passkeys: false` in the chart | When the map restarts; stored passkeys are kept but not used |
| The admin role for everyone | `map.admin.enabled: false` in the chart | When the map restarts |
| Every login of every kind | Delete `session.key` (existing) | At once; needs the volume, so a person |

Both chart switches are a one-line revert, and `jdw-deployments` needs no
approval, so an agent can make it.

### D8. Agents: no production session in phase 1

Agents do not hold a production session in phase 1, and this design does
not create one for them. Holding one means holding a credential, the
namespace is not enforced, and the owner's rule is an approval per run.
What agents get instead needs no credential at all:

1. **An identity-free status on the internal listener.**
   `GET /internal/v1/status`, `gatePublic` on `INTERNAL_ADDR`, the same
   payload as D5's admin status. An agent reads it with the access it
   already uses for Prometheus, through the API server's service proxy:

   ```sh
   kubectl get --raw \
     "/api/v1/namespaces/jdwillmsen-prd/services/http:<map service>:internal/proxy/internal/v1/status"
   ```

   No secret is involved: the request is authorised by the agent's own
   kube identity, and the map needs none.
2. **Metrics** on the same listener, as today.
3. **Logs** through `kubectl logs`, as today. They hold XUIDs and
   gamertags (login lines), so an agent quotes counts and event names from
   them and never names in a ticket, commit or pull request.
4. **The owner's debug report.** The page's debug panel already builds a
   report that holds no name or place unless asked; the owner pastes it.
5. **The harness.** `e2e/local.sh` runs the real server over a made-up
   world, which is where page bugs are reproduced.

**What the status may hold, since every pod can read it.** The namespace
is not enforced, so every workload in the cluster can reach the internal
listener. The status is held to a closed set of fields, as the checks'
reporter is (`e2e/lib/fields.mjs`): numbers, timestamps, booleans and
words from fixed lists. It has no free-text field, so it has nowhere to
put a gamertag, a path, a coordinate or a seed. A Go test builds the
payload from a server full of fixture players and markers and fails if any
string in it is not from the lists. This is the same exposure `/metrics`
already has.

Why not give it a token: a token is a credential an agent would then hold,
and a pod that can reach the port could read the token's Secret no less
easily than the agent.

### D9. Later, for the owner to decide: agents seeing the map itself

When the platform enforces network policy in `jdwillmsen-prd` (an owner
approval under `tenants/*/tenant.yaml`), the scheduled browser checks can
be switched on as already designed, and their verdicts are something
agents can read without any session. Beyond that, the owner could
delegate one of these, and this spec recommends none of them until he
asks:

- **(a) Keep the rule.** Each agent run against production needs his
  approval that time. Recommended.
- **(b) Agents may start a run of the in-cluster checks through git**,
  for example a chart value that renders a one-off Job from the CronJob.
  The agent never sees the secret, the run is the same read-only
  10-minute `service:e2e` session, each run is a commit, and the existing
  `maxExchangesPerHour` alert bounds it. It changes the standing rule from
  "approval per run" to "approval once, audited per run", and the owner
  would say so in writing.

Not offered: an agent running the checks from its own machine without
approval. `run.mjs --secret-from-kube` reads the secret into the runner's
memory, which is the agent holding it.

## Threat model

| Threat | What stops it |
|---|---|
| Stolen session cookie | `HttpOnly`, `Secure`, `__Host-`, `SameSite=Lax` as today. A passkey session lasts 12 h. A stolen game-code session older than 10 minutes cannot add a passkey; a passkey session never can. The owner ends it with Log out everywhere or `!map logout` |
| CSRF on the new endpoints | All state changes are `POST` or `DELETE` behind `http.NewCrossOriginProtection` (as today). The WebAuthn challenge is bound to a `SameSite=Strict` cookie. The browser itself refuses a WebAuthn call for an RP ID that is not the page's host |
| Passkey registration hijack | Registration needs: an operator's session, from a game code, under 10 minutes old, the challenge cookie, and a user-verified authenticator. Every registration is logged, counted and alerted (D6), and listed in the page with its date |
| Replay of an assertion or attestation | Challenges are random, single use and 2 minutes long; the sign counter is checked where there is one |
| A normal player making themselves admin | They cannot write `permissions.json`; the bridge has no command to; a claim for a code can only be for the XUID the server stamped on their own chat |
| **The internal token as an escalation path** | Whoever holds `INTERNAL_TOKEN` can already claim a code as any XUID, an operator's included, and so can log in as the owner, become admin and add a passkey that outlives a token rotation. This exists today for logins; passkeys make it last. Mitigations: the registration alert, the passkey list in the page, `!map logout` ending passkeys, and the 5-per-XUID cap. Only the map and the agent pods mount the token. Enforcing network policy does not change who can read the Secret. Accepted, with the alert as the control |
| An agent or anyone with cluster-admin | Can read every Secret, exec into the map, read `session.key` and edit `permissions.json`. Nothing in the map can stop them. The design only makes sure no workflow needs it. See "What was found" |
| Logs leaking identities | New lines carry XUIDs as the existing login line does, never gamertags, coordinates, challenges, keys or cookies (D6) |
| Brute force | A passkey cannot be guessed. The challenge table is bounded like the codes table, oldest first. Refused assertions are counted and logged at most once a minute past a small budget, as the exchange does. The map sees only the gateway's address, so there is no per-client limit |
| A pod reading the internal status | Closed field set, tested (D8) |
| Lockout | Lost passkey: log in through the game and add another. Bridge down: no admin (fail closed) but the map still works for players. Lost volume: passkeys gone, the game login still works. Phase 2 adds Dex as a second way |
| `permissions.json` read wrongly | The map, like the agent, accepts only the literal `operator`. An unparsable file is an error, which is "not admin" |

## Rollout

Phase 1 is three changes, each its own pull request, none needing the
owner:

1. **gameops, map release N.** D1, D2, D5 and D8: the operator check, the
   admin gate, `/api/me`'s flag, the admin status, the internal status,
   Log out everywhere, and passkeys built but behind their own switch.
   New settings, both off unless set:
   - `ADMIN_ENABLED` (default `false`): recognise operators as admins.
   - `PASSKEYS_ENABLED` (default `false`): the passkey routes. Refuses to
     start without `ADMIN_ENABLED`, without the login, or without
     `PUBLIC_ORIGIN`.
   - `PUBLIC_ORIGIN`, the exact `https://` origin passkeys are bound to.

   The internal status is on whenever the map runs: it holds nothing a
   switch would protect.
2. **jdw-deployments: run release N, `map.admin.enabled: true`.** Sets
   `ADMIN_ENABLED` and `PUBLIC_ORIGIN` from `map.host`, and adds the
   alerts. `tools/tests/test-map.sh` checks the origin is `https://` plus
   `map.host`, and that `map.admin.passkeys` cannot be on without
   `map.admin.enabled`. Agents can then read the internal status.
3. **jdw-deployments: `map.admin.passkeys: true`**, after (2) has run a
   day with operator lookups succeeding. The owner can add a passkey the
   next time he logs in through the game.

Phase 2 (D4) needs the owner's approval of one platform change and is
built only if he wants it. Phase 3 (D9) needs network-policy enforcement
and his written choice.

## Tests

Go, in `internal/auth` and `internal/server`, all executed in CI:

- The operator check: `operator` is admin; `member`, `visitor`, absent,
  unknown words, a bridge error, a timeout and a malformed body are not.
  The cache expires at 10 s.
- `gateAdmin`: no session is 401; a service session is 403; a `member`
  is 403; an operator is let through; demoting the operator in the fake
  bridge refuses the same cookie within the cache's life.
- `service_test.go` knows `gateAdmin`, and the existing check that every
  route is registered through `route` covers the new ones.
- Passkey registration, against a software authenticator in the test
  (ES256, `none` attestation): refused without an operator, from a
  passkey session, after 10 minutes, with a reused or expired challenge,
  a wrong origin, a wrong RP ID hash, UP or UV clear, a credential ID
  already held, and past 5 per XUID. Accepted otherwise, and written
  whole to disk.
- Passkey login: refused for an unknown credential, a bad signature, a
  wrong origin, UV clear, a counter that went backwards, a credential
  created before the XUID's revocation, and a non-operator. Accepted
  otherwise; the cookie carries `m: "passkey"` and a 12-hour expiry; a
  cookie without `m` still verifies.
- Cookies: a `m` that is anything but absent or `passkey` is refused.
- The internal status: built from a fixture server full of made-up players,
  markers and a seed, every string in the payload is from the closed
  lists. No gamertag, number that is an XUID, path or seed appears.
- Logs: a test captures the new log lines and fails on a gamertag, a
  challenge, a public key or a cookie value in them.
- Config: each refusal named under Rollout.

Page, under `web/testdata/*.test.cjs`, run by the existing Go wrappers:

- The login screen offers a passkey only when the config says passkeys
  are on, and falls back to the code when the browser has no WebAuthn.
- The Admin section of the debug panel renders from a fixture status and
  is absent without `admin` in `/api/me`.
- "Add a passkey" is shown only to an admin, only in the first 10 minutes
  of a game-code session.

`e2e/`: the harness gains a fake bridge `GET /permissions` with one
made-up operator, and the run gains a check that a service session is
refused by every admin route. A full passkey ceremony in Chromium uses the
DevTools virtual authenticator; whether `playwright-core` exposes it
cleanly enough for the suite is unverified.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Dex first | Needs the owner's platform approval, so it cannot be phase 1. Kept as phase 2 |
| A static admin password or "override" token | A secret someone creates and keeps, and a guessable door on the public listener |
| An admin bypass on the internal listener | Every pod in the cluster can reach it while the namespace is not enforced |
| Admin from a chart value | An identity in git |
| Long-lived operator sessions | Still needs the game to start, and widens a stolen cookie |
| Agents holding `service:e2e` sessions now | A credential in an agent's hands, in an unenforced namespace, against the owner's standing rule |
| "View as" a player | Reads players' private data for little debugging value (D5) |

## Open questions

- **Q1. Is "operator" the same as "owner"?** If other players are
  operators, they are admins too. Phase 1 gives an admin nothing about the
  world, so it does not matter yet. The owner-only underground view will
  need a narrower role. Candidates, none needing an XUID in git: only XUIDs
  that hold a passkey (an operator who has gone to the trouble), or a role
  the console grants by name through a bridge command, or the owner simply
  keeping the operator list to himself. Unknown to this spec: who the
  operators are today. It was not looked at, on purpose.
- **Q2. Library or hand-written WebAuthn?** Recommended: the library.
- **Q3. Session length for a passkey login.** 12 hours is a guess at "one
  debugging evening". The owner may want longer on his own phone.
- **Q4. Tell the player in game when a passkey is added to their login?**
  The map could `tellraw` them through the bridge's allowlist. It would
  catch internal-token abuse while they are online. Left out for now as
  more surface for a rare event the alert already covers.
- **Q5. Can an operator `/op` another player with cheats off?** If yes,
  any operator can make any player an admin. Unverified; it does not change
  the design, only who the owner should trust with operator.

## Sources

- W3C, *Web Authentication: An API for accessing Public Key Credentials,
  Level 3*, https://www.w3.org/TR/webauthn-3/ : glossary "Relying Party
  Identifier" (an RP ID is the origin's effective domain or a registrable
  suffix of it); §5.1.4.3 (a `rp.id` that is neither is a `SecurityError`);
  §5.4 (`attestation` defaults to `none`); §6.1 (UP, UV, BE and BS flags);
  §6.1.1 (signature counter: a counter that does not increase suggests a
  clone, and the response is the relying party's choice; authenticators
  without a counter report 0); §7.1 steps 8, 9, 14, 15, 16 and 26.
- Dex, *Using Dex*, https://dexidp.io/docs/guides/using-dex/ (clients are
  `staticClients` with an id, redirect URIs and, for confidential ones, a
  secret; the verifier trusts only its own client ID).
- Dex, *Scopes and Claims*,
  https://dexidp.io/docs/configuration/custom-scopes-claims-clients/
  (`public: true` clients without a secret). PKCE: dexidp/dex issue 2244,
  a secondary source; to be checked against the running Dex.
- Argo CD, *User Management*,
  https://argo-cd.readthedocs.io/en/stable/operator-manual/user-management/
  (the bundled Dex is served under `/api/dex`).
- Bedrock `permissions.json`: no Mojang page was found online. The
  primary source is `bedrock_server_how_to.html` in the server archive,
  not read for this spec. What this design relies on is what the bridge's
  own parser and tests accept (`minecraft/bridge/permissions.go`) and
  what the agent already trusts. Third-party guides agree on the shape
  (an array of `{permission, xuid}` with `operator`, `member`, `visitor`)
  and are not relied on.
- Platform ADR 0003, Headlamp mobile OIDC; `tenants/platform/services/
  argo-cd/values.yaml`; `.github/CODEOWNERS`; `tenants/jdwillmsen/
  tenant.yaml`, all at the platform's `main` on 2026-10-14.
