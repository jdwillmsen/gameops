# Bedrock bot account pool: how many real accounts, and how to run them safely (2026-10-30)

Research and policy only. No code was written or changed. Source keys (`S1`..)
resolve to URLs in the table at the end.

## Executive summary

1. Need: 3 accounts now (warriors), 6 with duels, 22 only if gate G5 passes; 100 has no use case.
2. Every account is made by hand by the owner with truthful details; bulk, CAPTCHA, phone/IP and disposable-mail tricks break the terms and cause bans.
3. Whether a free account (no Bedrock licence) can join a BDS over NetherNet is unpublished; the token has no ownership claim. [unverified]
4. Settle it with one hand-made account and no purchase: free, then launcher sign-in, then one $29.99 bundle (section 1.4).
5. Forbidden: scripted creation, false sign-up data, credential sharing, alts to dodge a suspension. A headless bot is not addressed: unclear risk.
6. No per-IP, per-phone or per-email limits are published, only anecdotes; Minecraft EULA and Usage Guidelines could not be loaded.
7. Email: Porkbun already forwards jdwlabs.com (free, 20 addresses, no catch-all); enough for 20 accounts.
8. Account-free options (RakNet offline, Dragonfly, Script API) each give something up; none is proven for our add-ons on 26.60.
9. Plan: policy sign-off, 1-account experiment, 3 accounts for 7 days, then 6, then decide 22; any lock or ban stops everything.
10. What we do not know is listed in section 8.

## Scope and guardrails

The owner asked how to "create ~100 accounts and emails and manage them,
scalably, without getting limits or blocks." This note answers the part that
fits the terms: a **small, hand-made, well-documented pool**. It describes
bulk, CAPTCHA-solving, phone/IP rotation and disposable-mail methods only to
say they violate Microsoft's rules and backfire. If Microsoft shows a block or
verification during sign-up, the correct response is to stop and use
Microsoft's own support route, not to retry from another network or device.

Tags: **[verified: Sn]** means the text was read from that source in this
session. **[unverified]** means a search snippet, a secondary or community
source, or an inference; the reason follows. **[inference]** is mine.

## 1. Does a NetherNet join need a Bedrock licence?

### 1.1 What the sources say about the token

- NetherNet's client identity is a `GameServerToken` JWT "issued by the
  Minecraft auth service", carrying "the player's identity claims (XUID,
  PlayFabId, UUID, permissions) and a `cpk` claim". [verified: S1]
- With no `a=identity` in the offer, the server "may either reject ... if
  authentication is required" or "allow the connection for open/unauthenticated
  scenarios. This is a server policy decision." [verified: S1]. This guide is
  Mojang's partner-signaling guide, not a description of BDS's own policy; BDS
  policy is not documented there. Our error 37 on BDS is consistent with BDS
  choosing "reject".
- The guide does **not address entitlement, ownership or Microsoft/Xbox account
  linkage**. [verified: S1] The listed claims contain no ownership claim.
  [inference] That suggests BDS cannot see a licence from the token, so any
  licence check would have to happen at the Minecraft auth service when the
  token is issued.
- gophertunnel obtains the token with a PlayFab session ticket sent as
  `user.token` to `POST {ServiceURI}/api/v1.0/session/start`, then
  `POST .../api/v1.0/multiplayer/session/start` with the key that becomes the
  `cpk` claim. The request body carries a device block (including
  `treatmentOverrides`) and a user block (language, region, token). **No
  entitlement field or endpoint appears in the code.** [verified: S2, S3, S4]
- `TreatmentOverrides` lets the client override service "treatments" (the code
  names `mc-signaling-usewebsockets`). Nothing links treatments to ownership.
  [verified: S2]
- When offline, gophertunnel signs its own identity with the hard-coded issuer
  `https://authorization.franchise.minecraft-services.net/`, mirroring the
  vanilla client's offline behaviour per a code comment. [verified: S5] go-nethernet's `PublicKey(selfSigned
  bool)` comment names "servers or offline clients" as self-signers. [verified:
  S7] So self-signed clients are a real, vanilla case; BDS just refuses them
  online.
- `prismarine-auth` says the PlayFab session ticket is used to generate the
  MCToken, and that the token authenticates the NetherNet signalling WebSocket.
  [verified: S8, secondary, JavaScript, not our library]

### 1.2 Ownership: the honest answer

No Microsoft, Mojang, gophertunnel or go-nethernet document we read says a
free account is enough, and none says a licence is required. Evidence
points in both directions:

- Against "any account works": a bedrock-protocol user reported that a **new
  account with an Xbox profile** failed device-code sign-in to a dedicated
  server until it had signed into the official launcher once; the issue is
  open with no maintainer fix and was never tested for ownership. [verified:
  S9; anecdotal]
- A second report says a brand-new Microsoft account **without an Xbox profile**
  gets a generic HTTP 500. [unverified: search snippet of S39]
- Forum answers say a Microsoft account without Minecraft "generally can't
  join" licence-checking servers. [unverified: forum snippets; no primary]
- Realms invitees do not need their own subscription, which is a Realms rule,
  not a BDS rule. [unverified: secondary snippet]

### 1.3 What BDS itself documents

`online-mode=true` means "all connected players must be authenticated to Xbox
Live", and clients on remote servers "will always require Xbox Live
authentication regardless of this setting". There is no property for
ownership. [verified: S10]

### 1.4 Cheapest safe experiment (one hand-made account)

Goal: learn whether a licence is required, at the lowest cost and risk.

1. The owner creates the mailbox address (for example a Porkbun forward
   `crew-ci01@jdwlabs.com` to their own inbox), signs up **in a normal browser,
   with truthful details**, and completes every verification step themselves.
   An adult date of birth avoids child-account restrictions on multiplayer.
   [verified: S19 4.a.i for truthful data; the multiplayer note is
   [unverified: S36b snippet]]
2. Create the Xbox profile (gamertag) by signing in on xbox.com once.
   [unverified: needed or automatic; S9 and S39 suggest it matters]
3. **Condition A (free, never opened Minecraft):** run the existing crew unit
   against the lab with an empty token cache; the owner finishes the device-code
   login in their **own terminal**. Record: login result, HTTP status from
   session start, join result, disconnect reason. Never record tokens.
4. **Condition B (if A fails):** sign into the official launcher once with that
   account, buy nothing, retry. [from S9]
5. **Condition C (if B fails):** buy the Java and Bedrock bundle for that one
   account ($29.99, section 2) and retry. This is the only spend, and it
   isolates "licence required".

| Result | Meaning | Next |
|---|---|---|
| A joins | Free account is enough for this BDS version. Re-test after BDS updates. | Section 7 plan, licence cost 0 |
| A fails, B joins | Profile or launcher first-run was missing, not a licence | Add "launcher first-run" to the creation checklist |
| B fails, C joins | Licence required | Section 2 costs apply per account |
| C fails | Not a licence or profile issue | Stop; file a ticket; compare with disconnect reason in BDS log |

A side result of the same run is a reference for which disconnect reason we get
for each cause. [unverified: whether the reasons differ]

## 2. Costs

| Item | Price | Source |
|---|---|---|
| Minecraft Java and Bedrock for PC (one account, one-time) | $29.99+ | [verified: S15] |
| Deluxe Collection | $39.99+ | [verified: S15] |
| Ultimate Collection | $49.99+ | [verified: S15] |
| Included with Game Pass Premium; the listing also shows "PC only" and "Ultimate" | monthly | [verified: S15] |
| Game Pass first-period offers | Premium "first 14 days for $1"; prices shown as "$XX.XX" | [verified: S16] |
| Game Pass monthly: Essential $9.99, Premium $14.99, PC $13.99, Ultimate $22.99 | monthly | [unverified: S18, aggregator sites; Microsoft's page shows placeholders] |
| Tiers that include Minecraft: Premium, PC, Ultimate, not Essential | | [unverified: S17 secondary; S15 confirms Premium/PC/Ultimate listing] |

Arithmetic from the verified $29.99 base: 3 accounts $89.97, 6 $179.94, 10
$299.90, 22 $659.78, 100 $2,999.00. Game Pass Premium at the unverified $14.99
costs more than the bundle after about two months, and the game disappears when
the subscription ends [inference].

Other routes:

- **Family group.** One Microsoft 365 Family plan shares with six people total;
  that is a Microsoft 365 rule, and no source says it shares a Minecraft licence.
  Family group size itself has no official hard cap that we found; each person
  can be in one family group. [unverified: S40 Q&A threads]
- **Volume licensing.** We found no volume or multi-seat route for Bedrock for
  a non-school buyer. [unverified: absence of evidence]
- **Minecraft Education.** The licence is $5.04 per user per year for eligible
  institutions and $36 per user per year commercial (from 2025-09-04), and it
  signs in with a school or organisation Microsoft 365 account, not a personal
  account. [unverified: search snippets quoting edusupport.minecraft.net; the
  pages returned 403 to our fetcher] A staff reply says Education and Bedrock
  do not cross-play. [unverified: snippet of S35] gophertunnel carries a
  separate `EduPlayFabTitleID`, which is consistent with Education being a
  different service. [verified: S2] Creating a tenant of school accounts to run
  bots on a Bedrock server is outside what an education licence is sold for
  [unverified: not read] and is **not recommended**.

## 3. Terms: allowed, forbidden, unclear

Texts read: Microsoft Services Agreement (published 2026-07-30, effective
2026-09-30; first 100,000 of 131,891 characters read, the remainder only
produced Store licence terms) [verified: S19]; Microsoft's "account has been
locked" page [verified: S20]; Xbox Community Standards (May 2024) [verified:
S21]. **The Minecraft EULA and Usage Guidelines on minecraft.net timed out on
every attempt**, so they are not read; see 3.4.

### 3.1 Forbidden (explicit text)

- "Creating fake accounts" and "automating inauthentic activity" (Code of
  Conduct 3.a). Also "Don't circumvent any restrictions on access to, usage, or
  availability of the Services". [verified: S19]
- "False, inaccurate or misleading information when signing up" (4.a.i).
  [verified: S19]
- Transferring account credentials to another user or entity; you are
  responsible for all activity under the account (4.a.i). [verified: S19]
  Practical rule: the owner holds every password; Josh and agents never do.
- "Don't use programs, scripts, or bots to create Microsoft accounts" and
  "Don't create or use multiple accounts to violate Microsoft policies."
  [verified: S20]
- "Create alternate Microsoft accounts to circumvent a suspension or
  restriction"; "Give another person access to your Microsoft account"; "Sell
  ... your gamertag or account"; cheating and exploits. [verified: S21]

### 3.2 Allowed (no prohibition found)

- An individual holding **several** accounts is not banned by any clause we
  read. The multiple-account text we found limits the Microsoft Rewards
  programme ("only one Rewards Account"), which is unrelated. [verified: S19]
  Microsoft's wording ties the ban to using extra accounts "to violate Microsoft
  policies". [verified: S20]
- Accounts created by hand with true information, kept active. [inference from
  3.1, no explicit approval text]

### 3.3 Unclear

- **A headless bot signed in as a real account.** The Community Standards "don't
  address bots or automated tools directly" and the MSA's "automating
  inauthentic activity" is about deceiving, which ours is not (the bots are the
  owner's disclosed crew on the owner's server). Still no text blesses it.
  [verified: S21 for the silence; the reading is [inference]]
- How many accounts one person may hold, and whether 24/7 sessions are
  expected. No limit is published.
- Whether Microsoft treats refreshing a token as the "sign in" that keeps an
  account from closing: the MSA closes an account after two years without
  sign-in (one year for Outlook.com and OneDrive). [verified: S19; refresh
  semantics [unverified]]

### 3.4 Minecraft EULA and Usage Guidelines

Not read. A search snippet says the EULA works alongside the Microsoft Services
Agreement and that the Usage Guidelines are revocable extra permissions about
servers, and secondary blogs say accounts are single-user. [unverified:
snippets of S36; blogs] Microsoft also moderates across Bedrock with account
level suspensions tied to Community Standards, including on third-party
servers. [unverified: S41 snippets]

**Action for the owner:** read the EULA once, and consider asking Xbox or
Minecraft support in writing whether headless clients for a private server are
acceptable. Keep the reply with the inventory.

### 3.5 Why the evasion methods backfire

Microsoft's own pages say scripted creation and multi-account policy evasion
lead to locks (S20), and Q&A answers list "many attempts from the same
device, IP, or network", "scripts, bots", and "reusing similar details" as
triggers for a "temporary or permanent block" [unverified: S23, a volunteer
and an AI-generated answer, not Microsoft staff]. Community Standards permit
"permanent suspension" of a profile or device, with loss of "game licenses,
subscription time, and account balances" [verified: S21]. Evading a block also
makes a later appeal weaker, because creating alternates to get around a
restriction is itself a listed violation (S21).

## 4. Published limits (and what is only anecdote)

| Topic | What we found | Status |
|---|---|---|
| Accounts per phone / per IP / per email | **No published number** | [unverified: S23 anecdotes only] |
| Outlook alias caps | Reports of 10 active and 10 new per year; the Q&A page we read gave no numbers | [unverified: S24] |
| Aliases vs accounts | An alias is another sign-in name on **one** account, so it is not a second identity | [inference] |
| Reusing an address | Addresses used for a Microsoft account "cannot be reused", even from a long-closed account | [verified: S22] |
| VoIP phone numbers | Reported as not accepted | [unverified: snippet of support.microsoft.com] |
| Family group size | No official hard cap found; Microsoft 365 sharing is 6 | [unverified: S40] |
| Simultaneous sessions per account | Expected one at a time; the second login kicks the first; no official statement | [unverified: forum/blog snippets] |
| Xbox sign-in throttling | **Nothing published** | unknown |
| Device-code flow | In Entra ID: 15 min default `expires_in`, poll no faster than `interval`; consumer endpoint behaviour not read | [verified: S25 for Entra only; consumer flow [unverified]] |
| PlayFab ticket refresh | About 24 hours, per a gophertunnel comment | [verified: S3] |
| Inactivity closure | 2 years (Outlook.com / OneDrive: 1 year) | [verified: S19] |

The block messages people report ("Account creation has been blocked") come
with workarounds like changing network, which is exactly the evasion we do not
use. [unverified: S23]

## 5. Email on jdwlabs.com

Current state, read from DNS today: MX is `fwd1.porkbun.com` and
`fwd2.porkbun.com`; name servers are Porkbun. So mail for the domain is
Porkbun forwarding. [verified: `dig` on 2026-10-30]

| Option | Limits and cost | Status |
|---|---|---|
| Porkbun forwarding (current) | **20 forwarding addresses free per domain**; wildcard or catch-all **not supported**; replies appear from your personal address | [verified: S32] |
| Extra Porkbun forwards | $3.00 per address per year | [unverified: search snippet of Porkbun product page] |
| Cloudflare Email Routing | 200 rules per domain, 200 destination addresses per account, 25 MiB per message, sends to verified destinations free; needs Cloudflare DNS (moving name servers). Catch-all not confirmed | [verified: S31 limits; catch-all [unverified]] |
| Google Workspace Business Starter | $7 per user per month annual or $8.40 monthly; up to 30 aliases per user | [unverified: S33 aggregators] |
| Zoho Mail free | 5 users, 1 domain, 5 GB, web and mobile only (older posts say 25 users) | [unverified: S34] |

What Microsoft accepts as the sign-up email: Microsoft says to use an existing
email address or a new Outlook one; community answers confirm Gmail and other
non-Microsoft addresses work and that Microsoft sends a verification code to
them. [verified: S22 for the Outlook route; non-Microsoft acceptance
[unverified: community answer]] **Plus-addressing** (`name+bot1@`) is reported
as rejected for Microsoft sign-up by one user. [unverified: Microsoft Answers
post] Avoid it: use a distinct local part per account instead.

Recommendation for a small pool: **one Porkbun forward per account**
(`crew-<role><nn>@jdwlabs.com`), each forwarding to the owner's own inbox. Up
to 20 accounts fit in the free tier. Do not use a catch-all (also unsupported
here), and do not use disposable or temporary mail services: they are on the
"do not" list and Microsoft's own pages tie reuse and automation to blocks.
Forwarding has one weak spot: Microsoft's verification and security mails go
through a forward, so confirm delivery before relying on it as the recovery
address. Add the owner's other recovery contact as security info.
Beyond 20 accounts, hosted mailboxes (Workspace) or a DNS move to Cloudflare
are the two supported routes; neither is needed for the stages below.

## 6. Alternatives with fewer or no real accounts

### 6.1 RakNet offline on a pinned BDS

- NetherNet became the default dedicated-server transport in 26.50; in 26.60
  "the warning that suggests trying NetherNet" became "an error when users are
  not using NetherNet, as RakNet is deprecated". 26.60 released 2026-10-27,
  server 1.26.60.6. [verified: S12, S13; secondary community wiki quoting the
  changelog; the minecraft.net changelog timed out]
- That is a **log level**, so RakNet probably still works in 26.60. [inference]
  The wiki says RakNet "still remains available in the game" [verified: S13],
  while a BedrockConnect issue claims 26.60 "removes RakNet from the client"
  with no citation [verified: S14 that the claim is uncited; claim
  [unverified]]. Whether retail clients still dial RakNet is unknown.
- Pinning means bots can stay on RakNet offline with no accounts, but human
  players' clients auto-update and likely cannot join an older server protocol
  [inference]. Our lab is deliberately unpinned. Deprecation implies later
  removal; no date is published. [unverified]
- Zero-cost check available: dial the current lab with a RakNet offline bot and
  see if 26.60 still accepts it with `online-mode=false`.

### 6.2 Third-party server software

| Software | Anonymous NetherNet? | Runs our add-ons and world? |
|---|---|---|
| Dragonfly | NetherNet support merged 2026-09-16. Config has `AuthEnabled` (default true) under `[Server]`. The listener doc says with authentication disabled "no verification will be done" [verified: S27, S28, S6]. Whether that covers the NetherNet path is **not shown** | A Go library with its own world engine; "generally used as a library to extend". Behaviour-pack and Script API support not documented [unverified] |
| Endstone | Not documented; BDS-based plugin framework per a search snippet, so BDS's auth rules likely apply [unverified] | Runs BDS, so probably yes [inference] |
| LeviLamina | Not documented; a mod loader for BDS whose README says to "read and adhere to our EULA and usage guidelines" [verified: S30] | Probably yes [inference] |
| PocketMine-MP | Releases found target 1.26.30 and 1.26.40; no NetherNet or 26.60 evidence [unverified: snippet] | Different pack model; unknown |

Patching BDS to skip identity checks is a licensing question we have not read
the EULA on; we do not recommend it.

Cheap test for Dragonfly: run it in a throwaway container with
`AuthEnabled=false` and NetherNet on, and dial it with the offline bot. That
settles anonymous NetherNet there in an hour. It would still not run our
behaviour packs.

### 6.3 Script API simulated players

`@minecraft/server-gametest` `SimulatedPlayer` extends `Player` and offers
`attack`, `breakBlock`, `chat`, `navigateToLocation`, `giveItem`, `useItemOnBlock`
and more; it is described as used "within GameTests", and "many types of events
... may not fire in the same capacity for simulated players". The Learn page
sits under the **experimental** moniker. [verified: S29] Trade-offs:
needs no account; but runs inside the server in TypeScript, so our Go
tactics, camera and control plane would need a rewrite, and it exercises no
NetherNet path at all. [inference] Whether `spawnSimulatedPlayer` works on a
production BDS without test mode is not shown. [unverified]

### 6.4 Small pool for integration tests only

Keep 2 accounts for a nightly NetherNet join test and run everything else
against a zero-account lab (RakNet offline or Dragonfly) where possible.
This is the lowest-risk way to keep real-account exposure small.

## 7. Recommended architecture and runbook

### 7.1 Pool size per use case

| Use | Accounts | Notes |
|---|---|---|
| Training (3 warriors) | **3** | One account per warrior, persistent |
| Duels | **6 total** (the 3 plus 3) | One session per account at a time, so pairs need distinct accounts |
| Crew fleet | Start **0 extra**; decide 22 at gate G5 | Run only as many units as there are accounts; do not queue 22 |
| CI / integration | **2** (`ci-01`, `ci-02`) | Separate from production, run in the cluster, not from shared hosted runners |
| Ceiling | **22**, not 100 | 100 has no use, costs up to $2,999 if licences are needed, and is the most conspicuous pattern |

Initial total: 3 + 3 + 2 = **8**.

### 7.2 Naming and inventory

- Account id: `crew-<role><nn>`: `crew-warrior01`, `crew-duel04`, `crew-ci01`.
- Email: `<account id>@jdwlabs.com`, one Porkbun forward each.
- `MC_USERNAME` = the account id: the token store is bound to that name
  ([verified: S37], `NewFileStore(AuthCacheDir, Username)` and "two accounts are
  two stores").
- Gamertag: chosen by the owner at creation. Length and uniqueness rules were
  not verified. [unverified]

Inventory (kept in the owner's notes, **no secrets**; passwords and recovery
codes stay in the owner's password manager, referenced by item name only):

| id | role | email | gamertag | created | licence | recovery set | token location | last login | status | notes |
|---|---|---|---|---|---|---|---|---|---|---|
| crew-warrior01 | warrior | crew-warrior01@jdwlabs.com | _ | YYYY-MM-DD | free / bundle / GP until _ | y / n | PVC `crew-warrior01` | YYYY-MM-DD | active / parked / locked / banned | |

Do not record dates of birth beyond "adult: yes".

### 7.3 Where tokens live

- One token cache **per unit on the cluster PVC** (`/data`), keyed by the
  account id; the repo's `mcauth` file store does this, and a database-backed
  store exists for failover. [verified: S37]
- Refresh tokens rotate, so two writers racing can retire the stored one; the
  store refuses stale writes (`ErrStoreConflict`). [verified: S37] Never copy a
  cache between units.
- Never in git, tickets, chat, CI variables or logs. Pod logs contain the
  device code by design (S37), so read them in your own terminal.

### 7.4 Device-code login (per account)

1. In a private browser window, sign out of everything; the window will only
   ever hold this one account.
2. In the owner's own terminal (not an agent shell), start the unit with an empty
   cache and read the printed code.
3. Open the printed URL, sign in as that account, enter the code, approve.
4. Confirm the unit joins; confirm a file exists in the PVC; close the window.
5. Update the inventory row (`last login`, `status`).
The unit must **refuse to print a code unwatched** and alert instead; the repo
already splits "no token yet" from every other failure for that reason.
[verified: S37]

### 7.5 Rotation, revocation, expiry, ban

- **Rotate** a password on suspicion of leak; changing it at
  account.microsoft.com should invalidate refresh tokens [unverified], so
  re-run 7.4 afterwards.
- **Revoke:** stop the unit, delete the PVC cache, change the password, remove
  the allowlist entry.
- **Expired or rejected token:** the unit stops and alerts; the owner re-runs
  7.4. Do not loop retries.
- **Lock or verification prompt:** use the on-screen unlock path or Microsoft's
  support route yourself. Do not create a replacement account to get around it
  (S21 lists that as a violation).
- **Ban or suspension:** stop all pool units, note the exact message, appeal
  through Xbox support, and treat it as a signal about the whole pool until
  understood. Do not rotate to a new account.
- Keep bot chat and combat on consenting players only; reports by other players
  are the most plausible trigger for moderation. [inference]

### 7.6 Staged plan with go/no-go gates

| Gate | Do | Go if | No-go action |
|---|---|---|---|
| G0 | Owner reads MSA 3.a/4.a and the Minecraft EULA; optional written question to support | Owner accepts the "unclear" risk in 3.3 | Fall back to 6.1 and 6.4 |
| G1 | The 1-account experiment (1.4), plus the zero-cost RakNet and Dragonfly checks (6.1, 6.2) | Account joins under A, B or C | If C fails, stop and file a ticket |
| G2 | Make 2 more accounts by hand, spread over days, true details, no retries after any block | All sign-ups complete first try | Any block: stop, ask support |
| G3 | Run 3 warriors for 7 days | No lock, no re-auth prompt, tokens refresh | Park all, investigate |
| G4 | Add 3 duel and 2 CI accounts; run 14 days | Same | Hold at 3 |
| G5 | Decide 22 | 60 days clean, written support answer if obtained, budget approved | Stay at 8 to 12 |

## 8. What we do not know

1. Whether a free account with no Bedrock licence can join a BDS over
   NetherNet (experiment 1.4 settles it for our version).
2. Whether Xbox profile creation and a first launcher sign-in are required, and
   whether disconnect reasons differ by cause.
3. What the Minecraft EULA and Usage Guidelines say about headless clients and
   multiple accounts (pages never loaded).
4. Whether Microsoft treats a headless bot on a real account as acceptable.
5. Any published per-phone, per-IP, per-email or per-person account limit, and
   the Outlook alias numbers.
6. How many simultaneous sessions one account may hold, and whether a second
   login kicks the first.
7. Whether token refresh counts as "signing in" for inactivity closure; consumer
   refresh-token lifetime.
8. Whether 26.60 retail clients still dial RakNet, and when BDS will drop it.
9. Whether BDS with `online-mode=false` accepts offline NetherNet, and whether
   Dragonfly's `AuthEnabled=false` covers its NetherNet listener.
10. Whether `spawnSimulatedPlayer` is usable on a production BDS.
11. Current Game Pass monthly prices direct from Microsoft; Education
    pricing and its Bedrock interoperability from the primary pages.
12. Porkbun's price for forwards beyond 20 and Cloudflare catch-all support.

## Sources

| Key | Source | Kind |
|---|---|---|
| S1 | https://mojang.github.io/bedrock-protocol-docs/guides/nether-net-onboarding-guide/ | primary |
| S2 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/service/token.go | primary |
| S3 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/service/token_source.go | primary |
| S4 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/multiplayer_token_source.go | primary |
| S5 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/nethernet.go | primary |
| S6 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/listener.go | primary |
| S7 | https://github.com/df-mc/go-nethernet (README, `identity.go`) | primary |
| S8 | https://raw.githubusercontent.com/PrismarineJS/prismarine-auth/HEAD/docs/API.md | secondary (JS library) |
| S9 | https://github.com/PrismarineJS/bedrock-protocol/issues/433 | anecdotal |
| S10 | https://learn.microsoft.com/en-us/minecraft/creator/documents/bedrockserver/server-properties | primary |
| S11 | https://minecraft.wiki/w/NetherNet | secondary (community wiki) |
| S12 | https://minecraft.wiki/w/Bedrock_Edition_26.60 | secondary, quotes changelog |
| S13 | https://minecraft.wiki/w/RakNet | secondary |
| S14 | https://github.com/Pugmatt/BedrockConnect/issues/723 | anecdotal, uncited |
| S15 | https://www.xbox.com/en-US/games/store/minecraft-java-bedrock-edition-for-pc/9NXP44L49SHJ | primary (store) |
| S16 | https://www.xbox.com/en-US/xbox-game-pass | primary, placeholder prices |
| S17 | https://folly.io/articles/is-minecraft-on-game-pass | secondary |
| S18 | https://www.lilachbullock.com/xbox-game-pass-price/ and https://subscriptionpriceguide.com/services/xbox-game-pass/ | secondary, search results |
| S19 | https://www.microsoft.com/servicesagreement | primary |
| S20 | https://support.microsoft.com/en-us/account-billing/account-has-been-locked-805e8b0d-4141-29b2-7b65-df6ff6c9ce27 | primary |
| S21 | https://www.xbox.com/en-US/legal/community-standards | primary |
| S22 | https://support.microsoft.com/en-us/accounts-billing/manage/how-to-create-a-new-microsoft-account | primary |
| S23 | https://learn.microsoft.com/en-us/answers/questions/5862830/why-am-i-getting-account-creation-blocked | community and AI answers |
| S24 | https://learn.microsoft.com/en-us/answers/questions/5567212/cannot-create-an-alias-due-to-the-limit | community |
| S25 | https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-device-code | primary, Entra ID only |
| S26 | https://github.com/Sandertv/gophertunnel/blob/master/minecraft/auth/live.go | primary |
| S27 | https://github.com/df-mc/dragonfly/blob/master/config.toml | primary |
| S28 | https://github.com/df-mc/dragonfly/pull/1290 and https://github.com/df-mc/dragonfly/pull/1482 | primary |
| S29 | https://learn.microsoft.com/en-us/minecraft/creator/scriptapi/minecraft/server-gametest/simulatedplayer | primary, experimental |
| S30 | https://github.com/LiteLDev/LeviLamina | primary |
| S31 | https://developers.cloudflare.com/email-routing/limits/ | primary |
| S32 | https://kb.porkbun.com/article/10-how-to-set-up-email-forwarding | primary |
| S33 | https://www.hostraha.com/blog/google-workspace-pricing-2026 and similar | secondary |
| S34 | https://aiproductivity.ai/pricing/zoho-mail/ | secondary |
| S35 | https://edusupport.minecraft.net/hc/en-us/community/posts/4409176573076-the-ability-to-crossplay-between-bedrock-and-education-edition | search snippet only (403) |
| S36 | https://www.minecraft.net/en-us/eula and https://www.minecraft.net/en-us/usage-guidelines | not read (timeouts) |
| S36b | https://support.xbox.com/en-US/help/family-online-safety/child-accounts/child-account-safety-on-xbox | search snippet only |
| S37 | This repo: `minecraft/agent/pkg/mcauth/store.go`, `minecraft/crew/cmd/crew/auth.go`, `minecraft/crew/README.docker.md` | repo code |
| S39 | https://github.com/PrismarineJS/node-minecraft-protocol/issues/782 | search snippet only |
| S40 | https://learn.microsoft.com/en-gb/answers/questions/5997305/microsoft-family-group-can-be-consist-of-how-many | community |
| S41 | https://www.minecraft.net/en-us/article/moderating-minecraft and https://www.pcgamer.com/minecraft-adds-permabans-for-players-who-violate-community-standards/ | search snippets only |
