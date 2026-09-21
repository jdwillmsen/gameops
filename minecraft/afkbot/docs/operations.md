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
