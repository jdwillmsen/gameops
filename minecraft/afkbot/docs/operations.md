# Operations

## First run

The bot signs in as a real Microsoft account, so it needs one that owns
Minecraft, separate from any account a human plays on. The first start has to
be completed by hand:

1. Start it and watch the logs for a line reading
   `Authenticate at https://www.microsoft.com/link using the code XXXXXXXX.`
2. Open that URL and enter the code — **in a private window, or signed out of
   any account you do not want to authenticate instead.** The page
   authenticates whatever Microsoft account the browser already has active; it
   does not prompt you to choose.
3. The token caches under `AUTH_CACHE_DIR`. Keep that on a volume — losing it
   means doing this again.

## Allowlist

Add the bot to the server's allowlist after its first connection. Its XUID
appears in the server log then:

```
Player connected: <name>, xuid: <xuid>
```

The bot is an ordinary player once connected: it occupies a slot, shows up in
`/list`, and triggers join and leave broadcasts.

## Auth cache

Each `MC_USERNAME` gets its own cache file, so several accounts can share one
`AUTH_CACHE_DIR` volume. `MC_USERNAME` is only that cache key — the gamertag
other players see comes from Xbox Live after login, which is why two bots with
different values here can appear under any two gamertags.

The cache format is specific to this implementation. A bot arriving from
another client library cannot reuse its cache and has to sign in again.

## Debugging a running bot

The image is distroless and runs as nonroot, with no shell: `kubectl exec`
will not work. The JSON event stream on stdout is the interface — see the
event table in the [README](../README.md#output).

`chunk_radius_granted` is the one to watch. It is the only evidence the bot
loads the area it was asked to load; a farm producing at a fraction of its
rate looks identical to a healthy one from every other signal.

## Parking

The agent can take the bot out of the world and put it back, so the chunks
around it stop ticking without touching `replicas`. Parking is decided on the
agent — `!park`, `!unpark`, the HTTP API or `tools/mc presence` — and the bot
only follows. The chart sets these variables:

| Variable | Meaning |
|---|---|
| `PRESENCE_URL` | The agent's HTTP address. Unset turns the feature off: no call to the agent, and the bot behaves as it always has |
| `PRESENCE_TOKEN` | Bearer token with `presence:read` and `presence:report`, bound to this bot's actor |
| `PRESENCE_ACTOR_ID` | This bot's actor id, e.g. `afk-bot-1` |
| `PRESENCE_DEFAULT` | `present` or `parked`: what the bot does until the agent first answers |
| `PRESENCE_POLL_MS` | How often it asks, default `10000`; a park or resume reaches the bot within one interval |

Parked means disconnected: the reconnect loop waits instead of retrying, and
reconnects as soon as the state returns to `present`, without a backoff.

This also changed shutdown for every bot, presence on or off: a SIGTERM now
closes the connection immediately, rather than waiting for the server to send
the next packet before the process exits.

**The agent being down never moves the bot.** Unreachable, erroring or
refusing, the bot keeps doing what it was last told, and a bot started during
an agent outage does what `PRESENCE_DEFAULT` says. It keeps polling, so it
picks up the agent's answer when the agent returns.

What to look for in the logs:

| Event | Means |
|---|---|
| `presence_enabled` | Feature on at startup, with actor, URL, default and interval |
| `presence_changed` | The desired state moved, `from` → `to` |
| `session_parked` | The session was closed because the bot was parked |
| `presence_fetch_unreachable` / `presence_report_unreachable` | Agent down, timing out, or answering 5xx, 408 or 429; the bot is holding `acting_on` |
| `presence_fetch_rejected` / `presence_report_rejected` | 401: the token is wrong. 403 on a fetch: the token lacks `presence:read`. 403 on a report: it lacks `presence:report`, is bound to another actor, or is unbound. 404: `PRESENCE_ACTOR_ID` is not in the agent's `PRESENCE_ACTORS` — or the agent's presence API isn't mounted at all (`PRESENCE_TOKENS`/`PRESENCE_ACTORS` empty on the agent), which answers with the agent's plain-text 404 instead of the contract's JSON body |
| `presence_fetch_invalid` | The agent answered with something this bot cannot act on — usually a contract change this image predates |
| `presence_fetch_recovered` / `presence_report_recovered` | The failure above has ended |

Each failure is logged once when it starts and once when it ends, not on
every poll — and again if its kind changes while it is still failing, e.g. an
agent that was merely unreachable starts rejecting the token: that is a
second, more actionable line, not noise to filter out. A bot that stays
parked when it should not is almost always holding an answer from before a
`rejected` line: fix the token or actor, and it acts on the next poll.
