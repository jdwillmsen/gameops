# crew-control

Public, view-only console for the Crew worker units. It reverse-proxies an
allow-list of each unit's camera routes and never forwards anything else.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `CREW_UNITS` | required | JSON list `[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"}]`, 1 to 26 units |
| `LISTEN_ADDR` | `0.0.0.0:8080` | Listen address |
| `LOG_LEVEL` | `info` | Log level |

## Routes

- `GET /`: the console, one focused unit or a wall of every unit; `GET /static/{file}` its assets; `GET /camera` redirects to `/`
- `GET /v1/units/stream`: server-sent `units` events from a once-a-second poll of each unit's `/v1/state`, sent on connect, on change and every 15 s; at most 64 clients, then 503
- `GET /units/{id}/camera`, `/units/{id}/v1/state`
- `GET /units/{id}/v1/camera/{file}` for `ws`, `mesher.js`, `entities.{js,png,json}`, `atlas.{png,json}`, `items.{png,json}`, `stats`
- `GET /healthz`

Any other path, method, unknown unit or unclean path under `/units/` gets 404
without an upstream request. Only the `r` query (1 to 3 digits) is forwarded,
and only on `ws`.
Cookies, `Authorization` and client `X-Forwarded-*` headers are dropped.
