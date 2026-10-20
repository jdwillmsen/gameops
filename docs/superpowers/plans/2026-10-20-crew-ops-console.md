# Crew ops console, slice 1

Jira: JDW-130. The owner asked for the crew console to become an operations console with much more functionality. Research and ranking: `docs/research/2026-10-20-crew-ops-console.md`. Fleet design target: 20 units or more.

## Decisions

- Public and operator split stays as in JDW-116. Everything public derives from the existing status poll. Coordinates exist only on signed-in streams.
- No change to `minecraft/crew`: the camera renderer belongs to JDW-129, and the live-feed budget is done in the console by choosing which tiles get an iframe.
- New code goes in new files (`telemetry/events.go`, `web/fleet.js`, `web/ops.js`) so the JDW-117 console and stream changes rebase cleanly. Edits to `console.js`, `sse.go`, `poller.go` stay small.

## Server (`crew-control/internal/telemetry`)

1. `Status` gains `hunger`; the task summary gains its id so a finished task can be matched to the unit's recent list.
2. A per-unit tracker turns poll results into events: online, offline, died, respawned, low health or hunger, task started, done, failed (with reason), cancelled, stuck. The first poll sets a baseline and announces nothing. A ring of 200 is kept.
3. The stream sends an `events` backlog on connect and a `log` event per new one to everyone, and `positions` (2 s, only when changed) to signed-in streams only.

## Console (`crew-control/internal/console/web`)

- `fleet.js`: pure logic (alerts, counts, sort, wall grid and paging, event merge, radar projection, fuzzy palette, key table). Node tests in `fleet.test.mjs`.
- `ops.js`: roster, wall with a live-feed budget (Cards, 4, 6, 9, 12, All) and paging, picture-in-picture pin, event dock with level filter, toasts, status bar, radar (operator), command palette, help, kiosk, bulk stop with confirm.
- `console.js`: wires the stream events and keys to `ops.js`; above 9 units the top bar shows a stepper because the roster is the picker.

## Verify

- `go test -race ./...` in crew-control; `node --test` in the web directory.
- Local run against 20 fake units: roster, wall pages and cards, palette, events and toasts, pin, radar and stop with a mocked operator session.
- Live: view-only checks against crew.prd.jdwlabs.com. The signed-in view is for the owner.

## Not in this slice

Ranked follow-ups (acks, auto-director, groups and tags, saved layouts, sparklines, state timeline, replay, work-priority grid) are listed in the research doc and the JDW-130 comment.
