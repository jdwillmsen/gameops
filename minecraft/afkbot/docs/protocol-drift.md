# Protocol drift

The server runs Mojang's `LATEST` and upgrades itself on restart, so the
protocol number it speaks moves without anyone deciding it should. Bedrock
point releases regularly bump that number without changing a single packet —
and the server still hard-kicks any client announcing the older one
(`play_status: failed_client`, before login even starts).

## What the bot does

Before each session the bot pings the server and announces whatever protocol
number the pong advertises, while still speaking the packet schema it was
built against (`internal/mcproto`). A substitution is logged as
`protocol_spoofed`.

On a release that genuinely changes the schema, this trades a clean pre-login
kick for a parse failure mid-join — the same reconnect loop either way, with
the cause named in the log rather than inferred.

A ping failure is not fatal: the bot falls back to its compiled-in number,
which is what it would have used anyway. The ping is bounded at 5 seconds, so
a server that is mid-restart costs one timeout rather than a hung connect.

## Keeping the compiled-in version current

`cmd/protocolcheck` answers the related question out of band — whether the
version this binary speaks still matches production, and whether a gophertunnel
release exists that would close the gap.

The `jdw-deployments` version-check CronJob dispatches it hourly, since it is
the only thing with a network path to the LAN-only server.

Announcing the server's number keeps the fleet connectable through a
number-only bump; it does nothing for a real schema change. Upgrading
gophertunnel is the actual fix, and the check is what says when one is due.

## Incidents

| Date | Release | Detail |
|---|---|---|
| 2026-08 | 1.26.45 | Shipped as protocol 2169 over unchanged packets. Every bot went offline at once and stayed down until the client library caught up days later. The ping-and-announce behaviour above was the response. |
| 2026-09 | — | gophertunnel v1.62.0 bumped the compiled-in schema to protocol 2193. |
