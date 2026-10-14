# Admin access to the production map

Status: proposed 2026-10-14, revised the same day after two independent
reviews. Nothing is built. Each decision below is numbered so that it can be
approved or rejected on its own.

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

**What phase 1 does not do: it does not let agents see the production map
itself.** No agent holds a production session, sees its tiles or drives a
browser against it. Agents get what the server says about itself (D8),
which needs no credential. Seeing the map is phase 3 (D9), and it is
blocked on two things only the owner can give: network-policy enforcement
in the namespace, and his word that the standing rule may change.

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
  2026-10-14 through the rulesets API), so map, agent and chart changes
  need nothing from the owner.
- **The standing rule on agent runs.** Each run of the browser checks
  from an agent's machine against production needs the owner's approval
  that time (README, "Who has to approve what"). This design does not
  change that rule.

## What was found by reading the code

- The map already holds `BRIDGE_URL` and `BRIDGE_TOKEN`
  (`internal/config/config.go`), for snapshots and the live layer.
  `BRIDGE_URL` is required, so a map always has a bridge to ask, though
  the bridge may be down.
- The console bridge already serves `GET /permissions` behind that token
  (`minecraft/bridge/http.go`). It reads the server's own
  `permissions.json` off the game's data volume, which it mounts
  read-only, on every request, and answers a map of XUID to `operator`,
  `member` or `visitor`. Its parser keeps the last entry when an XUID is
  listed twice (`ParsePermissions` writes into a map).
- The agent already decides who may run operator commands from exactly
  that answer, cached for 10 seconds, failing closed to `visitor` on any
  error (`agent/internal/adapters/permissions.go`).
- `permissions.json` is not in git and not rendered by the chart. The
  nightly backup copies it beside the world.
- The bridge's command allowlist has no `op`, `deop` or `permission`
  command, so the bridge token cannot change who is an operator.
- A session is a signed cookie carrying `Identity{XUID, Gamertag}`, issued
  for 7 days by default (`SESSION_TTL`), `SameSite=Lax`. Revocation is per
  XUID, "every session issued up to this moment", on the data volume
  (`internal/auth/revoke.go`).
- Every route goes through `route` with a gate, and `service_test.go`
  fails on any route registered another way. `auth.FromContext` answers
  "no player" for a service session, so anything keyed by XUID is out of
  its reach by construction.
- `GET /api/diagnostics` is on the **public** listener behind
  `gateViewer`. Without a session it cannot be read. The internal listener
  has only `/healthz`, `/metrics`, the agent's routes and the checks'
  exchange.
- The map already logs every login with XUID and gamertag
  (`map login issued`, `internal/server/auth.go`).
- `worker.Status.Problem` is only a stage name, `snapshot` or `render`;
  the detail goes to the log.
- The metrics already cover render, snapshot, live, markers, history,
  structures and the checks' exchange. They do not carry the build
  revision, the page's file fingerprints, the problem the page is showing,
  or which features the server resolved as on.
- The world runs with `allowCheats: false` (chart values).
- **The kube credential agents use on the devbox is `system:masters`**
  (`kubectl auth whoami`, 2026-10-14). An agent can technically read every
  Secret in the namespace, exec into the map and read `session.key`. So
  "agents never hold a credential" is a rule of conduct, not something the
  cluster enforces. This design never requires an agent to touch a
  credential; it cannot prevent one from doing so.

### What the game does with `permissions.json`

Read from `bedrock_server_how_to.html` in the Bedrock Dedicated Server
1.26.52.3 archive for Linux, Mojang's own manual (a copy already unpacked
on the devbox; the current download, checked 2026-10-14, is the same
version):

- "If you change this file while the server is running, then run the
  command `permission reload` to make sure that the server knows about your
  new change." `permission list` prints the operator list in use.
- Console `op <player>`: "Promote a player to operator. This will also
  persist in permissions.json if the player is authenticated to XBL. If
  permissions.json is missing it will be created. If the player is not
  connected to XBL, the player is promoted for the current server session
  and it will not be persisted on disk."
- Console `deop <player>`: "Demote a player to member. This will also
  persist in permissions.json if the player is authenticated to XBL."
- An XUID not in the file gets `default-player-permission-level`, which
  defaults to `member`.

**Verified:** the console's `op` and `deop` rewrite the file for a player
signed in to Xbox Live, and `permission reload` re-reads it. **Not
verified:** whether an operator typing `/op` or `/deop` in game chat does
the same, and whether they can at all with cheats off; the manual
describes console commands only. **Consequence:** the bridge reads the
file, not the server's memory. An `op` for a player not signed in to Xbox
Live, or a hand edit before `permission reload`, makes the game and the
map disagree until the file and the server agree again.

## Decisions

### D1. An admin is an operator in the game's own `permissions.json`

A logged-in player is the map's admin while the operator list says
`operator` for their XUID. Nothing else makes an admin. Any other word, an
absent XUID (which the game treats as `member`), or an unknown word means
"not an admin".

**Where the list comes from, in order:**

1. **Live.** The bridge's `GET /permissions`, cached 10 seconds, as the
   agent does.
2. **Last known good.** The map keeps the set of operator XUIDs in
   `DATA_DIR/auth/operators.json` with a separate `confirmedAt`, the last
   time a lookup succeeded and returned that set. The file is written
   whole like `revoked.json`, holds XUIDs and the one timestamp and
   nothing else, and is never logged. The set is rewritten only when it
   changes; `confirmedAt` is rewritten at least once an hour while
   lookups succeed, so an operator list that has not changed for weeks is
   still "confirmed within the hour". When the bridge cannot be reached,
   or answers with any error, a 404 for a missing file included, the map
   uses this set if `confirmedAt` is under 24 hours old.
3. **Nobody.** Past 24 hours, or with no file, nobody is an admin.

**When lookups happen.** Once at startup, and then whenever a request
needs to know (`/api/me`, `gateAdmin`, a passkey login, confirming a
passkey), at most once per 10 seconds, plus once an hour in the
background so that `confirmedAt` and `mcmap_admin_operators` stay
current with nobody using the map. If the startup lookup fails, the gauge
is set from the last-known-good set while it is in its window, and to 0
past it.

**The trade-off of (2):** while the bridge is down, de-opping someone
takes effect only once it is reachable again, up to 24 hours late. The
same holds for deleting `permissions.json`: the bridge then answers 404,
which the map treats as an error and not as "no operators", so the
operators it last saw stay admins for up to 24 hours. In
return the owner can still get in to debug the outage that took the bridge
down, which is when he most needs to. A bridge outage of that length also
stops snapshots and the live layer, which have their own alerts
(`JdwillmsenMinecraftMapStale`, `JdwillmsenMinecraftMapLiveStale`).

**Duplicates.** The bridge keeps the last entry for an XUID listed twice,
so the map does too. What the game does with a duplicate is not
documented. Q5 asks whether the bridge should instead treat an XUID with
conflicting entries as not an operator, which would change the agent too.

**Why this source:**

- **No identity in git.** The truth is the game's own file on the game's
  own volume, already backed up nightly, which the owner already curates.
- **No new secret and no new wiring.** The map already has the bridge
  token, the bridge already serves the file, and the agent already trusts
  it.
- **One list for the game and the map.** Taking someone off it in one
  takes them off in both, within 10 seconds while the bridge is up.
- **Spoofing.** The XUID comes from the session, which comes from the code
  flow (the agent reports the XUID the server stamped on the chat packet;
  the gamertag is never trusted) or from a passkey bound to that XUID
  (D3). The answer comes from the bridge over the cluster network. Faking
  it takes a write to the Service or the pod, which is cluster-admin, and
  cluster-admin can rewrite `permissions.json` itself.

What this does not settle: every operator is an admin. If the owner has
made friends operators, they get D5 too. D5 is chosen so that this is
harmless (nothing in it is about the world or another player), but the
owner-only views on the roadmap need something narrower. See Q1.

Rejected for who is admin:

- **A chart value naming the owner.** An XUID in git, which is not
  allowed. A hash of one is no better: XUIDs are short numbers, and the
  hash is found by trying them.
- **The first player to claim "owner" in the map.** Whoever gets there
  first wins, including someone holding the internal token.
- **A value in an existing Secret.** Someone has to write it, with Vault
  or `kubectl`, and it is a second operator list to keep in step.

### D2. Admin is a gate, not a kind of cookie

A new gate, `gateAdmin`, is a player's session (`Require`) plus D1's check
on `auth.FromContext`'s XUID. The cookie says nothing about being an
admin. A cookie issued while someone was an operator stops opening admin
routes as soon as the list says otherwise. A service session has no XUID
and so can never be an admin, by the same construction that keeps it from
waypoints. `service_test.go` learns the new gate.

`/api/me` gains `admin: true` for an operator, so the page can show what
only an admin has. Nothing else in a viewer's answers changes for an
admin.

**With the admin switch off** (`ADMIN_ENABLED=false`), no `gateAdmin`
route is registered, so each answers 404, and `/api/me` never says
`admin`. **With the login off** (`AUTH_DISABLED=true`) there is no
player, so nobody is an admin and the admin and passkey switches do
nothing. That is logged once at start and is never an error. **With the
bridge unreachable** the map uses D1's order and, past it, says "not an
admin". It never answers 5xx for that.

### D3. Phase 1: passkeys, added from the game and confirmed in the game

The owner gets in without the game with a passkey (WebAuthn) that the map
itself checks. Nothing is configured, no client is registered anywhere,
and no secret exists that a person or an agent has to hold: the map
stores only public keys.

**Only operators may add one** (`gateAdmin`). The owner asked for an admin
way in, and every player already has the game login, so giving passkeys
to everyone would add surface nobody asked for. **At most 5 per XUID**:
enough for a phone, a laptop, a security key and two spares, and few
enough that a list that keeps growing is noticed.

#### Adding a passkey

1. The owner is logged in, by game code or by an earlier passkey, as an
   operator. The page's admin section offers "Add a passkey" and asks for
   a label of 1 to 32 letters, digits, spaces or hyphens. The page shows
   it as text only, never as HTML.
2. `POST /auth/passkey/register/options` (admin gate) answers with
   creation options: a 32-byte random challenge, the RP ID (the host of
   `PUBLIC_ORIGIN`, `fwb.prd.jdwlabs.com`), and a user handle of 32 random
   bytes kept beside the XUID. The handle is never the XUID itself.
   `user.name` is `fwb-map` and `user.displayName` is `FWB map admin`.
   Neither is the gamertag or the XUID, so neither ends up in a password
   manager or a cloud keychain. The options also set
   `residentKey: required`, `userVerification: required`,
   `attestation: none`, ES256 and RS256, and an `excludeCredentials` list
   of the XUID's existing credentials. The map keeps the challenge for 2
   minutes, single use, together with the XUID and the session's
   `IssuedMilli`. The table is bounded and admin-only, so filling it takes
   an operator's session.
3. The browser creates the credential. `POST
   /auth/passkey/register/finish` (admin gate) checks that the caller's
   XUID and `IssuedMilli` are the ones the challenge was issued to, then
   runs the §7.1 steps listed below. On success it stores the credential
   as **pending**, tied to that XUID, and at most one pending credential
   per XUID: a new one replaces the old. A pending credential cannot log
   anyone in. The page says: "Now type `!map passkey` in the game. You
   will be whispered a code to type here."
4. The operator types `!map passkey` in game. The code goes **from the
   game to the browser**, the opposite way to a login code, so the
   player is never asked to type something a page showed them.
5. The agent sees the chat line, with the XUID the server stamped on it,
   and asks the map for a code: `POST /internal/v1/passkey-codes`
   (`gateAgent`), body `{"xuid": "<the sender's XUID>"}`. The map answers
   `200 {"code": "<6 characters>", "expiresIn": 120}` only if that XUID
   has a pending credential less than 2 minutes old, and `404` otherwise.
   The code is drawn from the login codes' alphabet, bound to that XUID
   and that pending credential, single use, and replaces any earlier code
   for it.
6. The agent whispers the code to that player alone, through its usual
   private reply path, which reaches them through the bridge's `tellraw`:
   "Your map passkey code is ABC234. Type it into the map page that asked
   for it, within 2 minutes. It lets that browser log in to the map as
   you, as admin, without the game. Never give it to anyone, and never
   type it into a page someone else sent you. If you did not just add a
   passkey, type !map logout." The reply is marked `RedactReply`, so the
   code is not written to the agent's log. One place can still record it:
   the bridge logs a command's text when sending it to the console fails
   (`command send failed` in `minecraft/bridge/http.go`). Such a code was
   never delivered, is single use and expires in 2 minutes. The agent
   change includes making the bridge log only the rule name for `tellraw`
   instead of the whole text, so that whispers never land there. On a `404` the agent answers:
   "There is no passkey waiting to be added for you. Start from the map
   page." The console cannot use the command.
7. The player types the code into the page that is registering, which
   sends it to `POST /auth/passkey/register/confirm` (admin gate). The map
   activates the credential only if the caller's XUID and `IssuedMilli`
   are the ones the registration was made under, the code is the current
   one for that pending credential, and both are under 2 minutes old.
   **Three wrong codes delete the pending credential and its code**, and
   the owner starts again. With 32^6 codes, three tries in two minutes is
   no attack.
8. The map writes `{credential ID, public key, sign count, backup flags,
   user handle, XUID, gamertag, label, created at, last used at}` to
   `DATA_DIR/auth/passkeys.json`, written whole like `revoked.json`. A
   pending credential not confirmed in 2 minutes is deleted.

**What the confirmation proves, and what it does not.** It proves that
the player whose XUID is on the registration was in the game during those
2 minutes and read a whisper sent only to them. It does not prove that
they meant to add a passkey to that browser. A player can still be talked
into typing `!map passkey` and passing the code on. That is why the code
travels from the game to the page: the player has to carry a secret out
of the game into a page, and the whisper says plainly what that does and
never to do it for anyone. It does not make phishing impossible.

What it does stop is a stolen session cookie, a session created by
tricking an operator into typing a login code off someone else's screen,
or a script on the page adding a passkey without the player. This
replaces the first draft's two rules, "only in a session's first 10
minutes" and "never from a passkey session". Both stood in for "the real
player is here now", and the confirmation shows that directly. With both
rules gone, an owner who logged in with a passkey can add a second one, as
long as he is in the game to get the code.

**The credential the authenticator kept.** A pending credential that is
never confirmed, or is deleted after three wrong codes, still exists on
the owner's authenticator, which created it before the map decided
anything. It will never log in: the map no longer knows it. The pinned
library builds the WebAuthn Level 3 `signalUnknownCredential` payload
(`protocol.CredentialDescriptor.SignalUnknownCredential`), so where the
browser offers `PublicKeyCredential.signalUnknownCredential`, the page
calls it for that credential to ask the authenticator to drop it. Where it
does not, the page says that a passkey named for this map may be left in
the password manager and can be deleted there.

#### Logging in with one

The login screen gains "Log in with a passkey". `/api/config`, the one
answer the map gives before login, gains `passkeys: true` so that the
screen knows to show it. That tells anyone who loads the page that the map
has a passkey login. It names nobody and says nothing about the world.

1. `POST /auth/passkey/options` (public) answers with a 32-byte random
   challenge and no allowed-credentials list (a discoverable credential:
   nothing to type). The map keeps no state for it. The challenge, its
   expiry 2 minutes on, and an HMAC over both go in a
   `__Host-mcmap_passkey` cookie, `HttpOnly`, `Secure`,
   `SameSite=Strict`. The HMAC key is derived from the session key with
   its own label, `mcmap passkey login challenge v1`, as `ServiceKey`
   derives the service key with its own, so no other key in the map can
   be used in its place. A flood of requests for challenges therefore
   costs nothing to store and can push nobody's challenge out.
2. The browser asks the authenticator, and `POST /auth/passkey/finish`
   (public, behind `CrossOriginProtection` like every `POST`) runs the
   §7.2 steps below.
3. Single use. After the signature has verified and before anything is
   issued, the challenge's hash is checked for and inserted into a used
   set in **one atomic step** under its lock: if it was already there, the
   login is refused as `replayed`. So two copies of one assertion sent at
   once cannot both get a session. Only an assertion that verifies against
   a stored credential is ever added, so only a holder of a passkey can
   grow the set, and each entry leaves when its challenge expires. The set
   is in memory. A restart empties it, so an assertion captured in the 2
   minutes before a restart could be replayed once after it. That needs
   the assertion, which travels only over TLS to this host, and the
   challenge cookie, which is `HttpOnly`. The sign counter catches it
   where the authenticator keeps one.
4. A session is issued only if the credential is active (not pending),
   its XUID is an operator now (D1), and the sign count passes.
5. The session is an ordinary player session with two more signed fields:
   `m: "passkey"` and `k`, the first 16 bytes of the SHA-256 of the
   credential ID, base64url. Its gamertag is the one stored with the
   credential. That is the gamertag of the game-code session that added
   it, refreshed every time that XUID logs in through the game. A passkey
   session lasts 12 hours, not 7 days.

**Sign count.** If both the stored and the presented count are 0, the
authenticator keeps no counter (synced passkeys do this) and the count is
not judged. If the presented count is greater than the stored one, the
stored one is raised. Otherwise, equal or lower with either one non-zero,
the login is refused and logged as `counter`, which is what §7.2 step 22
says may mean a cloned key. Reading, comparing and writing the count
happen under one lock on the store, so two logins at once cannot both
pass with the same count.

**Every request, not only at login,** a `m: "passkey"` cookie is refused
unless passkeys are switched on and the credential its `k` names is still
stored and active. So removing a passkey, or switching passkeys off, ends
its sessions at once, not after 12 hours. A cookie whose `m` is anything
but absent or `passkey`, or that has `m: "passkey"` and no `k`, is
refused. A cookie with no `m` is a game-code session and verifies as it
does today.

#### The WebAuthn steps the map performs

From §7.1 and §7.2 of the W3C Recommendation. Step numbers are the spec's.

Registration (§7.1):

| Step | Check |
|---|---|
| 5-6 | Decode `clientDataJSON` as UTF-8, removing a leading BOM, and parse it |
| 7 | `type` is `webauthn.create` |
| 8 | `challenge` is the one issued, unexpired and not used before, and the caller's XUID and `IssuedMilli` are the ones it was issued to |
| 9 | `origin` equals `PUBLIC_ORIGIN` exactly |
| 10 | `crossOrigin` true is **refused**: the map is never framed |
| 11 | `topOrigin` present is **refused**, for the same reason |
| 13 | CBOR-decode the attestation object |
| 14 | `rpIdHash` is the SHA-256 of the RP ID |
| 15 | UP is set |
| 16 | UV is set |
| 17 | BS is not set unless BE is |
| 20 | The key's `alg` is ES256 or RS256 |
| 21-22, 24 | Format `none` only. Anything else is refused, since the map asked for none and has no trust anchors |
| 25 | Credential ID at most 1023 bytes |
| 26 | Credential ID not stored for anyone, pending or active |
| 27, 29 | Store it, as pending, then active on confirmation |

Login (§7.2):

| Step | Check |
|---|---|
| 6 | `userHandle` is present, names a stored user handle, and that user's credentials include `rawId`. A missing handle, or one that names a different XUID than the credential's, is refused |
| 8-9 | Decode and parse `clientDataJSON` |
| 10 | `type` is `webauthn.get` |
| 11 | `challenge` is the one in the HMAC'd cookie, unexpired and not in the used set |
| 12 | `origin` equals `PUBLIC_ORIGIN` exactly |
| 13 | `crossOrigin` true is refused |
| 14 | `topOrigin` present is refused |
| 15 | `rpIdHash` is the SHA-256 of the RP ID |
| 16 | UP is set |
| 17 | UV is set |
| 18 | BS is not set unless BE is; BE is what it was at registration |
| 20-21 | The signature over `authData` followed by the SHA-256 of `clientDataJSON` verifies with the stored key |
| 22 | Sign count, as above |
| 24 | Store the new sign count, backup state and last-used time |

**Library.** `github.com/go-webauthn/webauthn`, pinned (v0.18.2 is the
newest on the Go module proxy, 2026-09-19), its version moved only by a
reviewed change. The map does not rely on the library's defaults for the
counter, `crossOrigin`, `topOrigin` or the user handle. It checks those
itself, and the tests below prove every step in the tables whatever the
library does. A hand-written verifier was rejected: CBOR, COSE keys and
attestation parsing are where such code goes wrong.

**Why passkeys for phase 1.**

- Everything is inside the map: no Dex client, no Vault, no chart secret.
- The owner's phone or laptop already has a passkey provider with a
  screen lock.
- A passkey is bound to this host's RP ID, so another site cannot use it.
- Bootstrapping uses the proof the map already trusts, being in the game,
  and recovery is the same: if every passkey is lost, log in through the
  game and add a new one.

**Limits, said plainly.**

- A port-forward is `localhost`, a different origin, so passkeys work only
  on the public address.
- The map's volume is not backed up. Losing it loses the passkeys and the
  last-known-good operators. The owner adds passkeys again through the
  game.
- Adding a passkey needs the owner in the game, once per passkey.

Rejected for phase 1:

- **Long-lived operator sessions** (a 90-day cookie). Still needs the game
  to start, and turns one stolen cookie into three months of access.
- **A recovery code or admin password.** A credential that someone has to
  create, see and keep, and a thing to guess at on the public listener.
- **A magic link sent out of band.** There is no channel to send it on
  that is not itself a new credential.

### D4. Phase 2, optional and owner-gated: log in through Dex

Phase 2 registers the map with the platform's Dex as a **public client
using PKCE**, so that it, too, needs no secret. Dex documents
`public: true` clients with no secret. PKCE has been in Dex since 2.26,
but the only source for that is the Dex issue that introduced it; check it
against the running version before relying on it. The platform change is
one `staticClients` entry in
`tenants/platform/services/argo-cd/values.yaml`: id `mcmap`,
`public: true`, redirect URI
`https://fwb.prd.jdwlabs.com/auth/oidc/callback`. That is the one thing
that needs the owner's approval.

The map does not trust an email. The Google connector accepts any Google
account on the internet (the platform's own comment says so), so the map
decides who gets in, and refuses anyone it cannot place. An operator,
logged in, presses "Link Dex login", goes through Dex once, and confirms
with a code whispered in game, as in D3. The map stores the pair
`(issuer, sub)` beside his XUID on the volume. A later Dex login whose
pair is linked is that XUID, subject to D1, to revocation and to the kill
switches, like a passkey. No email and no XUID go in git.
`github.com/coreos/go-oidc/v3`, already an indirect dependency of this
module, verifies the ID token.

What it adds over phase 1: the owner's GitHub or Google account, with
whatever second factor it has, as a second way in; a way in when every
passkey is lost and the game is not to hand; the identity he already uses
for Argo CD and Headlamp. Why it is optional: phase 1 already gets him in
without the game, and phase 2 adds a dependency on Argo CD's Dex and a
platform change.

### D5. What an admin can do in phase 1

The admin powers are small and read-only, and none of them shows anything
about the world that every player cannot already see. The owner plays this
world too: an admin view that showed more of it than other players see
would be the cheat the login exists to prevent.

Behind `gateAdmin`:

1. **The server's status in the page.** `GET /api/admin/status`, D8's
   payload plus the admin-only fields listed there, shown in an Admin
   section of the debug panel.
2. **Add a passkey** (D3).

Behind `gatePlayer`, each acting only on the caller's own XUID, so that a
player who is no longer an operator can still clean up after themselves:

3. **List their passkeys** (label, created, last used, whether pending).
   `GET /api/passkeys`.
4. **Remove one.** `DELETE /api/passkeys/{id}`. Its sessions end at once
   (D3).
5. **Log out everywhere.** `POST /api/logout-all`, the same revocation
   `!map logout` makes (D7). The page asks first: "This logs out every
   browser logged in as you and removes all your passkeys."

And, without a gate of its own, an admin sees the map as any player does.

Rejected for phase 1:

- **"View as" a player.** The only things that differ by player are their
  own waypoints, their standing in a village and which head is "me".
  Seeing another player's waypoints is reading their private notes. It
  helps with no bug that a fixture cannot reproduce, and it makes every
  admin request ambiguous in the log about whose data it touched. If a
  bug ever needs it, it should get its own design, with the player told
  in game.
- **Withheld structures, caves, seeds, others' trails.** Not admin powers
  in phase 1. The underground view on the roadmap is the owner's to
  decide (Q1).
- **Anything that writes to the world or the game**, including the chunk
  reset approvals on the roadmap. That design will add its own routes
  behind this gate.
- **A list of who logged in.** It is in the log already, and in the page
  it becomes a record of players' habits shown to every operator.

### D6. Audit and metrics

Every admin-relevant event is one log line and a count. The lines carry
the XUID, as the existing login line already does. They do **not** carry
a gamertag (the existing `map login issued` line keeps its gamertag, as
today), a label, a coordinate, a challenge, a passkey code, a public
key or a cookie. A credential is named by the first 8 hex digits of the
SHA-256 of its ID. A user agent is cut as `said` already cuts it.

| Event | Log line | Metric |
|---|---|---|
| Passkey made pending | `map passkey pending` xuid, credential | `mcmap_admin_passkeys_registered_total{result}`, `result` one of `pending`, `refused` (a §7.1 check failed), `code_issued`, `code_wrong`, `abandoned` (three wrong codes), `expired`, `confirmed` |
| Code asked for by the agent | `map passkey code issued` xuid, credential (never the code) | same, `code_issued` |
| Passkey confirmed | `map passkey registered` xuid, credential | same, `confirmed` |
| Passkey removed | `map passkey removed` xuid, credential, why (`player`, `revoked`) | `mcmap_admin_passkeys_removed_total{why}` |
| Passkey login | `map login issued` (the existing line) with `method=passkey`, credential | `mcmap_admin_logins_total{result}` with `ok`; before the signature verifies `bad_assertion`, `unknown_credential`, `expired`, `disabled`; after it `pending`, `not_operator`, `counter`, `replayed` |
| Admin route used | `map admin request` xuid, route pattern, allowed | `mcmap_admin_requests_total{route,allowed}` |
| Operator lookup | `map operator lookup failed` error class, on failure only, at most once a minute | `mcmap_admin_operator_lookups_total{result}`, `result` one of `live`, `last_known_good`, `none` |
| Operators known | none | `mcmap_admin_operators` (a count, no identities) |
| Passkeys held | none | `mcmap_admin_passkeys{state}` (`pending`, `active`), set from `passkeys.json` at startup and on every change |

Every label value in the table is declared at startup, at zero, the way
`declareServiceMetrics` declares the exchange's, so that a series exists
before its first event and a rule never has to tell "nothing happened"
from "no series yet".

**Where an alert goes.** Read from the platform's Alertmanager
configuration (`tenants/platform/services/kube-prometheus-stack/
postInstall/alertmanager-config-externalsecret.yaml`) and this chart's
rules:

- A `warning` without a `tenant` label, which is every rule this chart
  has today, goes to the AI-SRE relay and nowhere else. The direct
  Discord routes take only `tenant` labels and `severity="critical"`. So
  a warning from this chart reaches the owner only if the relay posts
  about it.
- A rule labelled `tenant: jdwillmsen` reaches Discord directly, at any
  severity, as the tenant's own `jdwillmsen-alerts` rules already do.
- **This chart's Loki rules do not evaluate.** The chart says so itself
  (`world-integrity-loki-rule.yaml`), and the platform's Loki rules
  sidecar is pinned to the `platform` org (`folder: /rules/platform` in
  `tenants/platform/services/loki/values.yaml`), while these logs are in
  org `jdwillmsen`. Fixing that is a platform change, which needs the
  owner.

So the alerts are:

- **`JdwillmsenMinecraftMapPasskeyAdded`**, `severity: warning`,
  `tenant: jdwillmsen`, so that it reaches the owner directly. It is
  based on the persisted gauge, not on a counter. A counter starts again
  at zero when the map restarts, and its first increment on a new series
  is invisible to `increase()`. The gauge is read back from
  `passkeys.json` at startup, so a restart leaves it where it was. The pod
  label changes with every restart, so the rule aggregates it away:

  ```promql
  (
    max without (pod, instance) (mcmap_admin_passkeys{state="active"})
      > max without (pod, instance) (mcmap_admin_passkeys{state="active"} offset 15m)
  )
  or
  (
    max without (pod, instance) (mcmap_admin_passkeys{state="active"}) > 0
      unless max without (pod, instance) (mcmap_admin_passkeys{state="active"} offset 15m)
  )
  ```

  The second half covers the first passkey ever, which has no series 15
  minutes earlier. It also fires once if the map was not scraped for over
  15 minutes while holding passkeys. That is a known false alarm, and
  cheaper than missing the first passkey. A removal and an addition in
  the same 15 minutes cancel out, so the counter's `confirmed` results are
  on the dashboard beside it. It should fire a few times a year, and each
  firing is worth a look. A **promtool rule test** in the chart covers:
  - the first passkey on a new series;
  - a second one added;
  - a restart with an unchanged count, which must stay silent;
  - a restart followed by an addition;
  - a removal, which must stay silent.
- **The same as a Loki rule** on the `map passkey registered` line, in
  the chart's existing Loki ConfigMap pattern. It ships now, so that it
  starts working when the platform fixes the ruler, and until then the
  PrometheusRule is the one that fires.
- **`JdwillmsenMinecraftMapPasskeyLoginRefused`**, any increase over 15
  minutes of `mcmap_admin_logins_total` for the results that come **after
  the signature has verified**: `counter`, `replayed`, `not_operator`,
  `pending`. Each one means someone held a real passkey and was still
  turned away, which is a clone, a replay, a de-opped holder or a
  half-made passkey. `severity: warning`, `tenant: jdwillmsen`. The
  results before the signature, `bad_assertion` and
  `unknown_credential`, are noise anyone on the internet can make and
  stay on the dashboard.
- **No alert on operator lookups.** A bridge down long enough to matter
  already raises `JdwillmsenMinecraftMapStale` and
  `JdwillmsenMinecraftMapLiveStale`. Lookups are made on demand and once
  an hour (D1), too seldom for a failure ratio to mean anything. The
  metric stays for the dashboard.

### D7. Revocation and turning it off

`!map logout` and Log out everywhere now do two things. They record the
revocation, as today, and they **delete every passkey that XUID holds**,
pending or active. The agent's reply to `!map logout` becomes: "Every
browser that was logged in to the map as you has been logged out, and any
passkeys for your map login have been removed."

The chart switches take effect when Argo CD has synced the merge (the
tenant's applications sync automatically) and the map has restarted. The
map's Deployment is `Recreate`, so the map is down for that restart. In
all, a few minutes.

| To end | Do | Takes effect |
|---|---|---|
| One person's admin | Take them off the operator list: the console's `deop`, which writes the file, or edit `permissions.json` and run `permission reload` (both verified in the manual). Whether an in-game `/deop` writes the file is not verified. **Deleting `permissions.json` does not do it**: the bridge answers 404, which the map treats as an outage and falls back to the last-known-good set | Within 10 s while the bridge is up; up to 24 h late while it is down or the file is missing (D1) |
| One person's sessions and passkeys | `!map logout` in game, or Log out everywhere in the page | At once |
| One passkey and its sessions | Remove it in the page | At once |
| Passkey logins for everyone | `map.admin.passkeys: false` in the chart | Argo sync plus restart. Every passkey session is refused from then on (D3). Stored passkeys are kept but unused |
| The admin role for everyone | `map.admin.enabled: false` **and** `map.admin.passkeys: false`, in one change. The map refuses to start with passkeys on and admin off, so turning admin off alone would stop the map | Argo sync plus restart. No admin route is registered and no passkey session is accepted |
| Every passkey | Delete `DATA_DIR/auth/passkeys.json` and restart the map | Needs the volume and a restart, so a person with cluster access |
| Every login of every kind | Delete `session.key` and restart the map | Needs the volume and a restart. Passkeys survive it and work again at once |

Turning passkeys off is one value. Turning admin off is both values in one
change. `jdw-deployments` needs no approval, so an agent can make either
change.

### D8. Agents: what the server says about itself, with no session

Agents do not hold a production session in phase 1, and this design does
not create one for them. Holding one means holding a credential, the
namespace is not enforced, and the owner's rule is an approval per run.

**What the status gives that metrics and logs do not.** An owner's bug
report from the page's debug panel has the page's view: its script
fingerprints, the problem it is showing, the features it was told of. An
agent reading it today cannot tell whether the server agrees. The status
answers that, and nothing more:

- the build the server is running, and the fingerprint of each page file
  it serves, so that "this report came from a stale script" is a
  comparison and not a guess;
- the problem stage the page is showing players right now;
- which features the server resolved as on, after defaults;
- whether the map is in a quiet window, which is why it would not be
  refreshing;
- where the operator list is coming from, so that "the owner says he is
  not admin" can be checked.

Everything else an agent needs is already in `/metrics`, and this
endpoint does not repeat it.

**How an agent reads it.** `GET /internal/v1/status`, `gatePublic` on
`INTERNAL_ADDR`, with the kube access agents already use for Prometheus,
through the API server's service proxy:

```sh
kubectl get --raw \
  "/api/v1/namespaces/jdwillmsen-prd/services/http:<map service>:internal/proxy/internal/v1/status"
```

No secret is involved: the request is authorised by the agent's own kube
identity, and the map needs none. Why not a token: an agent would then
hold it, and any pod that can reach the port could read the token's Secret
no less easily.

**The fields.** Every pod in the cluster can read the internal copy, so it
has no free-text field. A Go test builds it from a server full of fixture
players, markers, a seed and withheld structures. It fails if any key or
string value falls outside these shapes:

| Field | Type | Values |
|---|---|---|
| `build` | string | 0 to 12 lowercase hex digits, the VCS revision |
| `go` | string | `go` then digits and dots |
| `now` | string | RFC 3339 UTC |
| `problem` | string | `""`, `snapshot` or `render` |
| `quiet` | boolean | in a quiet window now |
| `features` | object | keys from the fixed list `login`, `live`, `markers`, `waypoints`, `icons`, `structures`, `biomes`, `trails`, `history`, `admin`, `passkeys`; values boolean |
| `snapshotAt` | string or absent | RFC 3339 UTC |
| `renderedAt` | object | keys from `render.Dimensions` (`overworld`, `nether`, `end`); values RFC 3339 UTC |
| `assets` | object | keys are the page's own file names, fixed at build; values 8 lowercase hex digits |
| `operators` | string | `live`, `last_known_good` or `none` |

Left out on purpose: URLs, the icons ref, the withheld structure kinds,
the seed, paths, the nether level labels, and any count of players,
sessions or entities.

**The admin copy** (`/api/admin/status`, D5) is the same, plus:

| Field | Type | Values |
|---|---|---|
| `live.lastFrameAgeSeconds` | object | keys from `render.Dimensions`; values a number of seconds, or absent |
| `sessions` | object | `issued`, `passkeyLogins`, `refused`: whole numbers since the map started |

**What else agents use, as today:** metrics on the same listener; logs
through `kubectl logs`, which hold XUIDs and gamertags in login lines, so
an agent quotes counts and event names from them and never names in a
ticket, commit or pull request; the owner's pasted debug report; and the
harness (`e2e/local.sh`), the real server over a made-up world, where page
bugs are reproduced.

### D9. Phase 3, for the owner: agents seeing the map itself

**Phase 1's behaviour is (a): the rule stays.** Each agent run against
production needs the owner's approval that time. Agents use D8 and
nothing that holds a session.

**(b) is a question for the owner, and it is blocked today.** The
in-cluster browser checks already exist, off. Turning them on renders only
with `map.checks.networkPolicyEnforced: true`, and
`charts/minecraft-fwb/templates/_helpers.tpl` line 129 in
`jdw-deployments` fails the render otherwise. That value may be true only
once the platform enforces network policy in `jdwillmsen-prd`, which is an
owner-approved change to `tenants/jdwillmsen/tenant.yaml`. Once it is, the
owner could decide:

- **(b) agents may start a run of the in-cluster checks through git**,
  for example by a chart value that renders a one-off Job from the
  CronJob. The agent never sees the secret. The run is the same read-only
  10-minute `service:e2e` session. Each run is a commit, and the existing
  `maxExchangesPerHour` alert bounds them. This changes the standing rule
  from "approval per run" to "approval once, audited per run", and the
  owner would have to say so in writing.

Not offered: an agent running the checks from its own machine without
approval. `run.mjs --secret-from-kube` reads the secret into the runner's
memory, which is the agent holding it.

## Threat model

| Threat | What stops it |
|---|---|
| Stolen session cookie | `HttpOnly`, `Secure`, `__Host-`, `SameSite=Lax` as today. A passkey session lasts 12 h and ends when its passkey is removed. A stolen cookie cannot make a passkey usable: that takes a code whispered to the real player in game. The owner ends it with Log out everywhere or `!map logout` |
| **Phishing an operator** into making a passkey for someone else: someone gets an operator's session (a code typed off another person's screen, or a stolen cookie), starts adding a passkey, and talks the operator into typing `!map passkey` and passing on the code | The passkey stays pending until its code is typed into the registering page within 2 minutes, with three tries. The code is whispered to that XUID alone, and the whisper says what it does and never to give it to anyone. **This reduces phishing and does not prevent it:** the confirmation proves the player was present, not that they meant it. A player who passes the code on anyway has given away admin access to the map, which the alert, the passkey list and `!map logout` are there to catch and undo |
| CSRF on the new endpoints | Every state change is `POST` or `DELETE` behind `http.NewCrossOriginProtection`, as today. The registration challenge is bound to the caller's XUID and session; the login challenge to a `SameSite=Strict` cookie |
| Script on the map's own origin calling WebAuthn with the admin's session | The content security policy, `default-src 'self'` with nothing from elsewhere, is what keeps foreign script off the page, and the code whispered in game is what keeps a passkey such a script made from working. **The CSP is never relaxed**; a test asserts the header byte for byte. With `attestation: none`, UV and attestation say nothing about whose authenticator it is, so they are not controls here |
| Replay of an assertion or attestation | Challenges are random, last 2 minutes and are single use: registration in a server-side table, login by an atomic check-and-insert into the used set after the signature verifies and before anything is issued. The used set does not survive a restart (D3); the sign counter is checked where there is one |
| Flooding login challenges | Login challenges are stateless; only verified assertions enter the used set |
| A normal player making themselves admin | They cannot write `permissions.json`; the bridge has no command to; a claim for a code can only be for the XUID the server stamped on their own chat |
| **The internal token as an escalation path** | Whoever holds `INTERNAL_TOKEN` can claim a login code as any XUID, an operator's included, start a passkey under that session, and ask `POST /internal/v1/passkey-codes` for the code themselves. So they can add a passkey that outlives a token rotation. This exists today for logins; passkeys make it last. **A code fetched with the token never goes through the agent, so no whisper is sent and the player is told nothing in game.** The alert to the owner on every new passkey is then the only timely control. After it come the passkey list in the page, `!map logout` removing passkeys, and the cap of 5. Only the map and the agent pods mount the token. Accepted, with the alert as the control |
| **The internal token as a lockout** | The same holder can post a revocation for the owner's XUID, which now deletes his passkeys. He gets back in through the game and adds them again. The revocation is logged with its XUID |
| An agent or anyone with cluster-admin | Can read every Secret and the map's volume. Whoever can read `session.key`, which is on the same volume as `passkeys.json`, can mint any session, a passkey session included, given the hash of a stored credential's ID. They can also edit `permissions.json`. Nothing in the map can stop them; the design only makes sure no workflow needs it |
| Logs leaking identities | New lines carry XUIDs as the existing login line does, never gamertags, labels, codes, coordinates, challenges, keys or cookies (D6). A test captures them |
| Brute force | A passkey cannot be guessed. A passkey code is 6 symbols of 32 (about a billion), lasts 2 minutes, and three wrong tries delete the pending passkey, so guessing it is hopeless. Typing it needs the session the registration was made under. Refused assertions are counted and logged at most once a minute past a small budget, as the exchange does. The map sees only the gateway's address, so there is no per-client limit |
| A pod reading the internal status | Closed field set, tested (D8) |
| Lockout | Lost passkey: log in through the game and add another. Bridge down: the last-known-good operators for 24 h, then no admin, while the map still works for players. Lost volume: passkeys gone, the game login still works. Phase 2 adds Dex as a second way |
| `permissions.json` read wrongly | Only the literal `operator` counts. An unparsable file is an error, which falls to the last-known-good order |

## Rollout

Phase 1 is four changes, each its own pull request, none needing the
owner:

1. **gameops: map release N and agent release M.** The map gets D1, D2,
   D3, D5, D7 and D8. The agent gets `!map passkey` (Visitor permission,
   like `!map`, since the map decides who is an operator; refused for the
   console; reply marked `RedactReply`), the call to
   `POST /internal/v1/passkey-codes` in its map client, the whisper and
   the new `!map logout` reply. New map settings, all off unless set:
   - `ADMIN_ENABLED` (default `false`): recognise operators as admins.
   - `PASSKEYS_ENABLED` (default `false`): the passkey routes and
     sessions. Without `ADMIN_ENABLED` the map refuses to start, since
     only admins may add a passkey. With the login off it does nothing
     (D2).
   - `PUBLIC_ORIGIN`, the exact `https://` origin passkeys are bound to.
     Required with `PASSKEYS_ENABLED`.

   The internal status is on whenever the map runs: it holds nothing a
   switch would protect.
2. **jdw-deployments: run map N and agent M, with
   `map.admin.enabled: true`.** Sets `ADMIN_ENABLED` and `PUBLIC_ORIGIN`
   (from `map.host`) and adds the alerts. `tools/tests/test-map.sh` checks
   that the origin is `https://` plus `map.host`. It also checks that
   `map.admin.passkeys` cannot be on without `map.admin.enabled`, and that
   both off together, which is how admin is turned off (D7), renders. From
   here agents can read the internal status.
3. **Wait for `mcmap_admin_operators` to be at least 1.** The map looks
   the list up at startup and hourly (D1), so the gauge is set within
   minutes of the rollout. An agent can check it through Prometheus. If it
   stays at 0, the operator list is empty or unreadable and step 4 waits.
   **At least 1 is necessary, not sufficient.** It says the map can read
   an operator list with someone on it. It does not say that the owner is
   that someone, or that his login will work.
4. **jdw-deployments: `map.admin.passkeys: true`.**

**Done is the owner seeing it, and that needs him.** No agent can log in
as an operator, so the last check, the owner seeing "Admin" in the page,
adding a passkey and logging in with it from another device, waits for him.
Until then the change is shipped but not proven end to end in production.
Steps 1 to 4 are proven by the tests and the harness.

Phase 2 (D4) needs the owner's approval of one platform change and is
built only if he wants it. Phase 3 (D9) needs network-policy enforcement
and his written choice.

## Tests

Go, in `internal/auth`, `internal/server` and the agent, all run in CI:

- **The operator list.** `operator` is admin; `member`, `visitor`,
  absent, unknown words are not. The cache expires at 10 s. With the fake
  bridge failing, a last-known-good file under 24 h is used and one over
  24 h is not, and with neither there is no admin. A 404 from the bridge
  (the file deleted) falls back the same way. The set is rewritten only
  when it changes, `confirmedAt` at least hourly, and the file holds only
  XUIDs and that time. **Unchanged set:** with the set the same for over
  24 h and lookups succeeding throughout, then the bridge failing, the set
  is still used (the window runs from `confirmedAt`, not from the last
  change). A lookup is made at startup; with the bridge down at startup
  the gauge comes from a last-known-good set in its window, and is 0 past
  it. Duplicate entries follow the bridge's last-wins rule.
- **`gateAdmin`.** No session is 401; a service session is 403; a
  `member` is 403; an operator is let through; demoting the operator in
  the fake bridge refuses the same cookie within the cache's life.
  `service_test.go` knows the gate.
- **Kill switches.** With `ADMIN_ENABLED=false`, every admin route is
  404 and `/api/me` has no `admin`, for an operator's cookie issued while
  it was on. With `PASSKEYS_ENABLED=false`, a `m: "passkey"` cookie
  issued while it was on is refused on every route, and the passkey routes
  are 404.
- **Login off and bridge down.** With `AUTH_DISABLED=true` and both
  switches on, the map starts, nobody is admin, and no route errors. With
  the bridge unreachable and no last-known-good, `/api/me` answers
  without `admin` and `gateAdmin` answers 403, never 5xx.
- **Registration**, against a software authenticator in the test (ES256
  and RS256, `none` attestation). Refused: without an operator; by a
  session whose XUID or `IssuedMilli` differs from the challenge's; with
  a reused or expired challenge; `type` not `webauthn.create`; a wrong
  origin; `crossOrigin` true; `topOrigin` present; a wrong RP ID hash; UP
  or UV clear; BS without BE; another `alg`; a format other than `none`;
  a credential ID over 1023 bytes or already held; a sixth passkey; a
  label that is not 1 to 32 allowed characters. A pending credential
  cannot log in. A second registration replaces the XUID's pending one.
- **Passkey codes.** `POST /internal/v1/passkey-codes` without the token
  is 401; for an XUID with no pending credential, or one over 2 minutes
  old, is 404; otherwise it returns a code, and asking again replaces it.
  `POST /auth/passkey/register/confirm` is refused for:
  - a wrong code;
  - an expired code, or a pending credential over 2 minutes old;
  - a code already used, or one replaced by a newer one;
  - the right code from a session with another XUID, or another
    `IssuedMilli`.

  The third wrong code deletes the pending credential, and the right code
  after that is refused. A right code activates the credential.
  Unconfirmed credentials are gone after 2 minutes, and confirmed ones are
  written whole to disk. Neither the code nor the challenge appears in any
  log line or metric label.
- **Login.** Refused: an unknown credential; a pending one; a missing
  `userHandle`; a `userHandle` that names another XUID; `type` not
  `webauthn.get`; a challenge reused (the used set), expired, or with a
  bad HMAC; a wrong origin; `crossOrigin` true; `topOrigin` present; a
  wrong RP ID hash; UP or UV clear; BE changed since registration; a bad
  signature; an equal or lower counter with either non-zero; a
  non-operator. Accepted with both counters 0, and with a higher one,
  which is stored. Two concurrent logins with the same counter: one
  passes. The same assertion sent twice at once: exactly one session is
  issued, and the other is refused as `replayed`. A challenge cookie
  signed with any key but the one derived under the
  `mcmap passkey login challenge v1` label is refused. The cookie carries `m: "passkey"`, the credential hash, the
  stored gamertag and a 12-hour expiry.
- **Sessions.** Removing a passkey refuses its sessions on the next
  request. A cookie with an unknown `m`, or `passkey` without `k`, is
  refused. A cookie without `m` verifies as today.
- **Revocation.** `POST /internal/v1/revocations` and Log out everywhere
  delete that XUID's passkeys, pending and active, and only that XUID's.
- **Options.** The creation options hold no gamertag and no XUID.
- **The internal status.** Built from a fixture server full of made-up
  players, markers, a seed and withheld kinds, every key and string is
  within the shapes in D8. The admin copy adds only its two fields.
- **Logs.** A test captures every new log line and fails on a gamertag,
  a label, a code, a challenge, a public key or a cookie value in them.
- **CSP.** The header on every response is the one in `secured`,
  byte for byte.
- **Config.** Each refusal named under Rollout, and none of the others.
- **Metrics.** Every label value in D6 exists at zero right after
  startup. `mcmap_admin_passkeys{state}` is read back from disk at
  startup.
- **Agent.** `!map passkey` asks the map for a code with the sender's
  XUID, never the gamertag. It whispers the code with the exact warning
  text to that player only, and never broadcasts it. The reply is
  redacted from the agent's log. On a 404 it gives the "nothing waiting"
  reply, and it refuses the console. The new `!map logout` reply.

Chart, in `jdw-deployments`:

- A promtool rule test for `JdwillmsenMinecraftMapPasskeyAdded`. It must
  fire on the first passkey on a new series and on a later addition. It
  must stay silent on a restart with the count unchanged and the pod label
  changed, and on a removal. It must fire on an addition after a restart.
- A promtool test that `JdwillmsenMinecraftMapPasskeyLoginRefused` fires
  on `counter`, `replayed`, `not_operator` and `pending`, and not on
  `bad_assertion` or `unknown_credential`.

Page, under `web/testdata/*.test.cjs`, run by the existing Go wrappers:

- The login screen offers a passkey only when `/api/config` says so, and
  keeps the code when the browser has no WebAuthn.
- The Admin section renders from a fixture status, and is absent without
  `admin` in `/api/me`.
- Add a passkey tells the player to type `!map passkey` in game, then
  takes the whispered code in a field, says how many tries are left after
  a wrong one, and starts again after the third. A label is rendered as
  text (a label of `<b>x</b>` shows those characters).
- After an abandoned or expired registration, the page calls
  `PublicKeyCredential.signalUnknownCredential` where the browser has it.
  Where it does not, the page shows the note about the leftover passkey.
- Log out everywhere asks first and says passkeys are removed.

`e2e/`: the harness gains a fake bridge `GET /permissions` with one
made-up operator, and the run gains a check that a service session is
refused by every admin and passkey route. A full passkey ceremony in
Chromium would use the DevTools virtual authenticator; whether
`playwright-core` exposes it cleanly enough for the suite is unverified.

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
| Passkeys for every player | Nobody asked for it, and every player already has the game login |
| A hand-written WebAuthn verifier | Where such code goes wrong; the library is pinned and the tests prove each step |
| A stateful login challenge table | A flood of requests would push real challenges out; the HMAC'd cookie needs no table |

## Open questions

- **Q1. Is "operator" the same as "owner"?** If other players are
  operators, they are admins too. Phase 1 gives an admin nothing about the
  world, so it does not matter yet. The owner-only underground view will
  need a narrower role. There are three candidates, none of which needs an
  XUID in git:
  - only XUIDs that hold a passkey;
  - a role the console grants by name;
  - the owner keeping the operator list to himself.

  Who the operators are today was not looked at, on purpose.
- **Q2. Session length for a passkey login.** 12 hours is a guess at "one
  debugging evening".
- **Q3. Can an operator `/op` or `/deop` in game chat with cheats off,
  and does it write the file?** The manual covers only the console.
  Testing it needs a player client on a test server.
- **Q4. When will the platform's Loki ruler read tenant orgs?** Until
  then the Loki copy of the passkey alert is inert, and the
  PrometheusRule is the one that fires.
- **Q5. Should the bridge refuse an XUID with conflicting entries,**
  rather than keep the last? It would change the agent's answer too.

## Sources

- W3C, *Web Authentication: An API for accessing Public Key Credentials,
  Level 3*, Recommendation, version of 25 August 2026,
  https://www.w3.org/TR/webauthn-3/. Sections used:
  - glossary "Relying Party Identifier";
  - §5.1.4.3;
  - §5.4 (`attestation` defaults to `none`);
  - §6.1 (the UP, UV, BE and BS flags);
  - §6.1.1 (the signature counter);
  - §7.1 steps 5 to 29;
  - §7.2 steps 6 to 24.

  Step numbers were counted from the published HTML.
- Mojang, `bedrock_server_how_to.html`, in the Bedrock Dedicated Server
  1.26.52.3 archive for Linux: sections "Permissions" and "Commands"
  (`permission list|reload`, `op`, `deop`) and the
  `default-player-permission-level` property. The version was confirmed
  as current through Mojang's download links API on 2026-10-14.
- Dex, *Using Dex*, https://dexidp.io/docs/guides/using-dex/, and
  *Scopes and Claims*,
  https://dexidp.io/docs/configuration/custom-scopes-claims-clients/
  (`public: true` clients without a secret). PKCE: dexidp/dex issue 2244,
  a secondary source.
- Argo CD, *User Management*,
  https://argo-cd.readthedocs.io/en/stable/operator-manual/user-management/
  (the bundled Dex is served under `/api/dex`).
- Go module proxy, `github.com/go-webauthn/webauthn/@latest`, and the
  v0.18.2 source (`protocol/options.go`, `SignalUnknownCredential`), read
  2026-10-14.
- Platform, at `main` on 2026-10-14: ADR 0003; `.github/CODEOWNERS`;
  `tenants/jdwillmsen/tenant.yaml`;
  `tenants/platform/services/argo-cd/values.yaml`;
  `tenants/platform/services/kube-prometheus-stack/postInstall/alertmanager-config-externalsecret.yaml`;
  `tenants/platform/services/loki/values.yaml`;
  `helm-charts/tenant-envelope/templates/deployments-appset.yaml`
  (automated sync).
- `jdw-deployments`, at `main` on 2026-10-14:
  `charts/minecraft-fwb/templates/map.yaml` (Service, route, rules,
  `Recreate`); `_helpers.tpl` line 129; `world-integrity-loki-rule.yaml`;
  `values.yaml`.
