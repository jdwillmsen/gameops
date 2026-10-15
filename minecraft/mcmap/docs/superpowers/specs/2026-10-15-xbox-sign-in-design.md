# Sign in to the map with an Xbox account

Status: proposed 2026-10-15, built behind `XBOX_LOGIN_ENABLED` (off). Stacked on
the admin-access change, whose conventions it follows: switches off by
default, `__Host-` cookies, fail closed, closed-shape metrics, no identity
in git, logs carry the XUID and never a gamertag or a token.

## What is wanted

Known players sign in to the map with their Xbox / Microsoft account, with
no game running. The code login stays; this is a second way to the same
`Sessions.Issue`. "Known players" is the owner's wording: a stranger with
an Xbox account is refused.

## Spike: can a personal app do this?

Research on 2026-10-15, from the sources linked at the end. Nothing here was
run against Microsoft: I cannot sign in as a person.

**What the documentation establishes**

- The Microsoft identity platform's authorization code flow takes a
  `consumers` tenant, `response_type=code`, `response_mode=query`,
  `code_challenge`/`code_challenge_method=S256` (PKCE is "recommended for
  all application types, both public and confidential"), `state`, and a
  `redirect_uri` that must match a registered one exactly. A confidential
  web app redeems the code at `/consumers/oauth2/v2.0/token` with
  `client_secret` and `code_verifier`. Codes "typically expire after about
  1 minute". A user who cancels is sent back to the redirect URI with
  `error=access_denied`. Without the `offline_access` scope no refresh
  token is issued.
- The sign-in must use `consumers` for the `XboxLive.signin` scope; work and
  school accounts cannot sign in this way (the Minecraft launcher
  community's documentation of the Xbox chain, which Microsoft itself does
  not publish for third parties).
- Xbox Live user authentication: `POST user.auth.xboxlive.com/user/authenticate`
  with `AuthMethod: "RPS"`, `SiteName: "user.auth.xboxlive.com"`,
  `RpsTicket: "d=<access token>"`, `RelyingParty: "http://auth.xboxlive.com"`,
  `TokenType: "JWT"`; the answer is `Token` and `DisplayClaims.xui[0].uhs`.
  Then `POST xsts.auth.xboxlive.com/xsts/authorize` with
  `Properties: {SandboxId: "RETAIL", UserTokens: [<token>]}` and
  `RelyingParty`. With `http://xboxlive.com` the `xui[0]` claims carry
  `xid` (the XUID) and `gtg` (the gamertag) beside `uhs`; this relying
  party and these claims are known only from third-party libraries'
  sample output, not from a Microsoft page, so the code takes `xid` and
  `gtg` if they are there and refuses the login if `xid` is not a number.
- A refused XSTS request is a 401 with a body `{Identity, XErr, Message,
  Redirect}`. 2148916233: no Xbox profile on the account. 2148916238: a
  child account that is not in a Family (the community list adds: "seen
  only with custom Azure apps", which is itself evidence that custom apps
  do get as far as XSTS). 2148916227: banned from Xbox. 2148916235: Xbox
  Live is not offered in the account's country. 2148916236 and 2148916237:
  adult verification (South Korea).
- The extra approval that new apps are told to seek (a form for the
  Minecraft API) concerns `api.minecraftservices.com` only. This design
  never calls it, so it does not apply. Its failure is a 403 "Invalid app
  registration" from that API, which is the error most threads about
  "XboxLive.signin needs approval" are really about.

**What the documentation does not establish**

- (a) Whether a personal Entra registration can be *consented to* for
  `XboxLive.signin` with no Xbox partner enrolment. Evidence is mixed: the
  Xbox user and XSTS endpoints are what open-source Minecraft launchers use
  with their own registrations, and the child-account note above says
  custom apps reach XSTS; against that, a Microsoft Q&A answer from February
  2026 (by a volunteer moderator, not Microsoft staff) says the scope needs
  enrolment in the Xbox Developer program, and a 2023 question reports the
  scope missing from the portal's permission picker and an `Unauthorized`
  redirect. Microsoft staff in that thread declined to say. Treat this as
  **likely feasible, unproven**. It is decided by the owner's first real
  sign-in; nothing in the code can tell beforehand.
- (b) Terms. No Microsoft or Xbox page I found grants or forbids a private
  site using these endpoints, and the user-authentication endpoints are not
  documented for third parties at all. The use is small and private (a
  handful of friends, no data kept but an XUID), but the endpoints can
  change or be closed to unregistered apps without notice. The design
  therefore fails closed and leaves the code login untouched, so that if
  Microsoft closes this the cost is a button that stops working.
- (c) Recent changes: I found none in the Entra documentation (the auth
  code flow page was updated June 2026 and still describes the flow above).
  The Xbox side has no changelog to read.

**Verified from documentation:** the OAuth 2.0 parameters, the token
request, the redirect error shape, the XBL and XSTS request shapes, the
XErr list. **Provable only at the first real sign-in:** that Microsoft
consents the app for `XboxLive.signin`; that `xid`/`gtg` come back for
`http://xboxlive.com`; the exact `x-xbl-contract-version` the endpoints
want (sent as `1`, as the libraries do); the child-account answer.

## What the owner creates

In the Entra admin center, App registrations, New registration:

- Supported account types: **Personal Microsoft accounts only**.
- Redirect URI: platform **Web**, `https://<the map's host>/auth/xbox/callback`
  (exactly `PUBLIC_ORIGIN` plus that path).
- Certificates and secrets: one **client secret**. It expires (24 months at
  most); a calendar reminder is the owner's, and an expired secret shows
  as `exchange_failed` in the metric.
- API permissions: delegated `XboxLive.signin`, if the portal lists it
  (under APIs my organization uses, "Xbox Live"); if it does not, the
  scope is still requested at sign-in and the answer is the first test of
  (a). No admin consent applies to personal accounts.
- Nothing else: no Graph permission, no `offline_access`, no ID token.

## Design

### Flow

1. `GET /auth/xbox/start` makes a random `state` and PKCE `code_verifier`
   and answers 302 to
   `login.microsoftonline.com/consumers/oauth2/v2.0/authorize` with
   `client_id`, `response_type=code`, `redirect_uri`, `response_mode=query`,
   `scope=XboxLive.signin`, `state`, `code_challenge` (S256) and
   `prompt=select_account`, so that someone with two accounts on one
   browser is asked which.
2. The state and verifier are kept in `__Host-mcmap_xbox`: JSON, base64url,
   `.`, HMAC-SHA256 under a key derived from the session key for this
   purpose alone. `HttpOnly`, `Secure`, `Path=/`, 10 minutes (long enough
   for a password and a second factor). The map stores nothing for it.
   **`SameSite=Lax`**, where every other login cookie is `Strict`: the
   browser comes back from `login.microsoftonline.com` by a top-level
   navigation that a site other than this one started, and `Strict`
   cookies are not sent on that. `Lax` sends them on a top-level `GET` and
   on nothing a page embeds or posts. The callback changes nothing until
   state, cookie and Microsoft's code agree, so a cross-site `GET` that
   carries the cookie gains nothing an attacker without the code can use.
3. `GET /auth/xbox/callback`: the cookie is cleared on every answer. Then,
   in order: the cookie is present, signed and unexpired; the `state`
   query value equals the cookie's (constant time); that state has not
   been used (a bounded in-memory set; the cookie is also gone from an
   honest browser, and Microsoft codes are single use, so this is a
   third layer against a replayed request); an `error` from Microsoft is
   `cancelled` for `access_denied` and `microsoft_error` for anything else,
   and its description is never shown or logged; a `code` is present and
   short.
4. The code is redeemed server side (client secret, verifier), then XBL,
   then XSTS. The Microsoft access token, the XBL token and the XSTS token
   live in local variables of one function, are never stored, never
   logged, never put in an error, and the HTTP client has no redirects and
   bounded reads. The one thing taken from them is `xid` and `gtg`.
5. The identity must pass `Identity.check` (a numeric XUID; a gamertag
   with no control characters, within length, not claiming the service
   prefix).
6. The known-player gate. A stranger is refused with no session.
7. `Sessions.IssueXbox`, then 303 to `/`. A refusal is a 303 to
   `/?xbox=<result>` with `result` from a closed list; the page maps it
   to a sentence and removes it from the address bar, so nothing a caller
   chose is ever reflected.

### The known-player gate

An XUID is let in if either:

1. **It has completed a `!map` code login.** When a claim succeeds
   (`POST /internal/v1/claims`), the XUID is written to
   `DATA_DIR/auth/known.json` with a first-seen and a last-seen time and
   nothing else (no gamertag), by an atomic write. A claim, not a
   collected session, is the evidence: it is the agent, which sees the chat
   packet, saying this XUID is on the server. The record is made whether
   or not Xbox sign-in is on, so that turning it on later finds the
   players who have logged in since this ships. A failed write is logged
   and does not fail the claim; it only means that player is not yet known.
   Last-seen moves at most once an hour. **An entry expires 180 days after
   its last-seen**, so that a player who has left (or been banned) stops
   being able to sign in without anyone remembering to remove them; their
   remedy is to type `!map` in game once.
2. **It is on the server's allowlist**, when `XBOX_ALLOWLIST=true`. The
   console bridge already serves `GET /allowlist` (the game's
   `allowlist.json`: name, XUID, ignoresPlayerLimit) behind the token the
   map holds for `/permissions`. The map asks it when a sign-in needs the
   answer and keeps the XUID set for 30 seconds. Unlike the operator
   list there is no last-known-good: if the bridge cannot be reached the
   allowlist says nobody, and only the first path applies. Entries without
   an XUID (names added by hand that have not joined yet) are skipped.
   This path is what lets a player who has never typed a code sign in,
   and is off by default because a server that does not use its allowlist
   may keep a stale one.

Removing someone: take them off the allowlist and call
`POST /internal/v1/known/forget` (agent gate, `{"xuid"}`), which drops the
entry in memory and on disk at once. `known.json` is read only at start, so
editing it while the map runs does nothing and the next claim's write would
restore the entry. `!map logout` and Log out everywhere (`revokeEverywhere`)
end the player's sessions and also forget them, so that someone holding a
compromised Microsoft account needs a fresh `!map` in game before it can
sign in again; if forgetting fails the revocation answers 500 so the agent
retries. The allowlist stays an OR with the record, as the owner chose, so a
player still on it must be taken off it too.

If the bridge cannot be asked and the player is not otherwise known, the
refusal is `allowlist_unavailable` (logged within a budget, with no XUID),
not `stranger`.

The callback is guarded by a global token bucket (burst 10, one per 6
seconds) after the state is checked and before Microsoft is asked, so that
a script holding valid cookies cannot get the app throttled for everyone.
The service sees only the gateway's address, so a per-client limit would
mean nothing. A turned-away callback is `busy`.

### Sessions

`Sessions.IssueXbox` signs a normal player session with `m: "xbox"` and no
key. `verify` accepts that method only while `Sessions.XboxOn` is set, so
turning the switch off refuses every Xbox session at once (as for
passkeys), and it passes through the same revocation check as every other
session: `!map logout` and Log out everywhere end Xbox sessions. Lifetime:
**24 hours**. A code login proves someone is in the game now and lasts
7 days; an Xbox sign-in proves only who owns an account and that the
account is known, and costs one click to repeat, so it should not outlive
a day. It is longer than a passkey's 12 hours because a passkey is an
admin's. Admin follows the player: it is the operator list, by XUID, asked on every
request, so an operator who signs in with Xbox is an admin. That is
intended, so that the owner can run map operations from a phone. Adding a
passkey still needs the code typed in game.

### Configuration

| Variable | Meaning |
|---|---|
| `XBOX_LOGIN_ENABLED` | `true` to serve the routes. Default `false`. |
| `XBOX_CLIENT_ID` | The Entra application (client) ID. A GUID. |
| `XBOX_CLIENT_SECRET_FILE` | Path to a file holding the client secret (a mounted Kubernetes Secret). |
| `XBOX_CLIENT_SECRET` | The secret itself, for a local run. Setting both is refused. |
| `XBOX_ALLOWLIST` | `true` to also let allowlisted XUIDs in. Default `false`. |
| `PUBLIC_ORIGIN` | As for passkeys. The redirect URI is this plus `/auth/xbox/callback`. |

Enabled without a client ID, a secret or `PUBLIC_ORIGIN`, the service
refuses to start. With no login (`AUTH_DISABLED=true`) it is turned off
with a logged warning, as admin and passkeys are. The Microsoft and Xbox
base URLs are constants of the `xbox` package. A test builds a client
with `xbox.NewWithEndpoints`, which nothing outside tests calls; they are
not in the environment.

### Observability

`mcmap_xbox_logins_total{result}` with a closed set, each at zero from
start: `ok`, `stranger`, `cancelled`, `microsoft_error`, `expired`
(no usable state cookie), `state_mismatch`, `replayed`, `bad_request`,
`exchange_failed` (Microsoft refused the code or the app), `no_xbox_profile`,
`child_account`, `xbox_refused` (other XErr), `xbox_unavailable` (the
network, a 5xx or an unreadable answer), `bad_identity`, `fault`, `busy`, `allowlist_unavailable`. Logs: an
issued login is `map login issued` with `xuid` and `method=xbox`, as for
the other methods; a stranger is `map xbox login refused` with the `xuid`,
at the same budget as the passkey refusals; failures before an identity
exists log the result and, for XErr, the numeric code. Never a token, code,
secret, state, verifier, or Microsoft's own error text. The admin status
gains the `xbox` feature flag.

### Page

When `/api/config` says `xbox`, the login screen gets a Sign in with Xbox
link (`auth/xbox/start`; a plain navigation, no script needed to start).
`/?xbox=<result>` is read by `xbox.js` (a file of its own under the
existing CSP, which does not change), which shows the sentence for it, then
removes the parameter. Disabled: the routes are 404, `/api/config` says
nothing, and no link is made.

## Not done

- No Microsoft or Xbox token is kept, so there is no refresh, no friends
  list, no presence.
- No new endpoint for an agent; the agent's side is unchanged.
- No e2e harness check: the harness cannot stand in for Microsoft, and the
  server tests drive the callback against a fake of it.

## Sources

- Microsoft identity platform and OAuth 2.0 authorization code flow
  (learn.microsoft.com/entra/identity-platform/v2-oauth2-auth-code-flow).
- Microsoft Authentication Scheme, Minecraft Wiki
  (minecraft.wiki/w/Minecraft_Wiki:Projects/wiki.vg_merge/Microsoft_Authentication_Scheme).
- Microsoft Q&A, "How to get XboxLive.signin permission for Azure App
  Registration (Minecraft Launcher)", February 2026, and "How to get
  Azure AD Xbox Live API Access?", November 2023.
- `@xboxreplay/xboxlive-auth` documentation, for the `http://xboxlive.com`
  relying party and the claim names.
