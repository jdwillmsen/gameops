# Crew ops console: prior art and feature ranking

Date: 2026-10-20. Research only; no code. Claims below come from pages actually read (URLs in section 5). Where a page did not cover something, it is not claimed.

## 1. Goal and constraints

Turn the Crew web console (crew.prd.jdwlabs.com) into an "operations console" that feels like one, for a fleet of Minecraft Bedrock worker bots (3 now, 20+ soon).

- Fleet size: 20+ units. Per-unit state: health, hunger, task, online/dead, queue.
- Two audiences. Public viewers: view-only, no coordinates. Signed-in operators (passkey, 2 people): command panel, work queue, and anything position-bearing.
- Stack: plain JS, no build step, static page served by a Go server; status already arrives over SSE.
- Per-feed cost: each live camera is a websocket plus a WebGL context. 20 simultaneous live feeds is not viable, so the design needs a live-tile budget, with the rest of the wall showing cheap state.
- Implication: most of the "console feel" should come from the SSE state stream (cheap), not from video (expensive). Video is a scarce resource to allocate.

## 2. Findings per domain

### RimWorld and Factorio
- Factorio alerts are short-lived typed events (e.g. entity under attack 10 s, destroyed 30 s, train out of fuel 5 s) shown beside the quickbar, with optional sound. Clicking one jumps the view to the cause; hovering highlights the entity and shows edge arrows. Types can be toggled or muted individually. [Factorio Alerts]
- Factorio separates transient alerts (time-boxed) from persistent per-entity status icons (missing fuel/ammo, stay until fixed). Same split fits Crew: event feed vs. tile status badge. [Factorio Alerts]
- RimWorld Work tab is a grid: colonists as rows, work types as columns, priority 1 (high) to 4 (low), blank = never. Equal priority resolves left to right. A grid of units x task types is a compact bulk-edit surface for the work queue. [RimWorld Work]
- RimWorld cell outline color encodes skill (red/white/yellow) and hover shows detail: encode secondary data in cell styling, details on hover. [RimWorld Work]
- RimWorld supports direct orders on top of priorities (right-click target, Shift chains tasks): maps to "command now" vs. "queue". [RimWorld Work]
- Not obtained: RimWorld colonist bar/alerts page and Factorio map/train overview pages returned 404, so no claims are made about them.

### StarCraft II observer UI
- Primary sources were blocked (Liquipedia 403). Secondary results only: patch 2.0.10 added "Split 1v1" and "Streamlined" observer interfaces chosen from a dropdown; production tab is reported on hotkey D by one guide (sources disagree on keys, verify in-game); Liquipedia describes army value/supply and per-player income panels. [SC2 search results]
- Transferable but low-confidence: switchable observer layouts (presets), and a stats strip (income, army, production) separate from the main view. Treat as inspiration, not spec.

### Robot/drone ground stations
- QGroundControl: toolbar multi-vehicle selector appears only when more than one vehicle is connected; fixed status indicators (flight status, mode, GPS, battery) always shown, others conditional and each expands to a dropdown. [QGC toolbar]
- QGC Fly View: a switcher swaps Video and Map as main display, with the other as small inset; the video can be resized, hidden, detached. This is the PiP-swap pattern. [QGC Fly View]
- QGC: map shows all connected vehicles at once; dangerous actions require confirmation. [QGC Fly View]
- Mission Planner: HUD can be detached to its own window or swapped with the map; a "Quick" tab shows a few chosen values in large text; "Messages" lists status messages by severity, important ones also surface on the HUD; Actions tab holds commands; double-click a status value to graph it live. [Mission Planner]
- Foxglove: layouts of panels are saved per user and shareable to the org; unsaved changes are flagged; tab panels group panels. [Foxglove layouts]
- Formant docs URL returned 404; no claims made.

### Minecraft spectator, replay and map tools
- Java spectator mode: number keys teleport to a player; clicking an entity attaches first-person view that follows it; Shift returns to free flight. On Bedrock, spectator cannot attach to other entities, which is why Crew needs its own camera layer. [Minecraft Wiki Spectator]
- BlueMap markers: marker sets are named groups that can be toggleable and default-hidden; POI markers have a click-to-show detail; shape markers outline areas; markers can be driven by an API. Good model for radar layers (units, task zones, hazards) with per-layer toggles. [BlueMap Markers]
- Replay Mod: README states only "record game sessions and replay them afterwards from any perspective"; feature details were not on the page read. [ReplayMod repo]
- CS observer (Valve wiki, historical CS:GO, via search summary): number key follows a player, Space cycles In-Eye / Chasing / Roaming, Shift+number picks a static camera that sees the player. Auto-director behavior was not confirmed. [CS:GO Spectator Tools]
- Dynmap wiki page fetched had no player-marker details; not claimed.

### CCTV / VMS video walls
- Frigate "smart streaming" (default): camera tiles show a still image about once a minute and switch to live video when motion/objects are detected; alternatives are continuous or none. Stills come from the low-res detect stream and refresh at about 5 fps until the live player loads. This is the closest match to a live-tile budget. [Frigate live]
- Frigate: choose a substream for dashboards/mobile and full-res for desktop, saved per device in local storage; docs advise reducing simultaneous live streams. [Frigate live]
- Frigate falls back to a lower-quality player ("low bandwidth mode") on timeouts/buffering and shows it in the UI, with reset. [Frigate live]
- Milestone XProtect adaptive streaming: the client picks the camera stream whose resolution best fits the tile, reducing data handled; a diagnostics overlay shows current resolution per tile. Analogue for Crew: low-detail render (smaller canvas, lower fps) for small tiles, full for the focused one. [Milestone adaptive streaming]
- Camera groups (Frigate) let a dashboard override stream settings: saved groups of units with their own streaming policy. [Frigate live]
- Genetec tours/sequences: not obtained, no claims.

### Grafana-style ops dashboards
- State timeline: one row per entity, colored bars per state until the next state; value mappings give text/color; option to merge equal consecutive values; pagination limits rows shown. Direct fit for per-unit online/dead/task history. [Grafana state timeline]
- Status history: same idea without merging, one box per sample; drag to zoom, double-click to zoom out. [Grafana status history]
- Alert list panel: group by label, sort by importance or time, cap items (default 20), filter by state (alerting, pending, no data, normal, error). [Grafana alert list]
- Annotations: vertical markers on charts with description and tags, creatable by API; toggleable by tag. Use for task start/finish, deaths, and operator commands overlaid on health sparklines. [Grafana annotations]
- Variables: dashboard dropdowns drive queries, titles, and repeated panels; values sync to the URL as var-name=value, with an opt-out. Model for URL-shareable views and per-group repeat. [Grafana variables]

### Keyboard-first consoles and alert ack
- VS Code: command palette on Ctrl+Shift+P, `?` lists sub-modes, Ctrl+P quick open by name, Ctrl+1..3 switch groups, Zen Mode hides most UI, status bar at bottom with a notification bell and Do Not Disturb. [VS Code UI]
- k9s: `:` enters command mode with aliases, `/text` filters (regex, inverse `/!`, fuzzy `/-f`), `?` shows hotkeys, single-letter actions on the selected row, destructive ones need confirmation. [k9s commands]
- PagerDuty: incident states triggered -> acknowledged -> resolved; ack stops escalation; an ack timeout returns the incident to triggered; un-ack re-escalates. Model for operator alert ack with a timeout so acks do not hide problems forever. [PagerDuty incidents]
- MDN IntersectionObserver: standard browser API to activate/deactivate content by visibility with rootMargin; usable to start a camera only when its tile is on screen. [MDN IntersectionObserver]

## 3. Ranked feature list

Ranking is by value divided by cost. Cost: S small, M medium, L large (for plain JS + Go + existing SSE).

| Rank | Feature | Value | Cost | Source inspiration | Operator-only? | Slice 1 |
|---|---|---|---|---|---|---|
| 1 | Roster table (one dense row per unit: state, health, hunger, task, last event; sort/filter) | H | S | k9s, Grafana state timeline, RimWorld work grid | No (public sees non-positional fields) | yes |
| 2 | Status bar (fleet counts online/dead/idle, open alerts, SSE health, live-tile count) | M | S | VS Code, QGC toolbar | No | yes |
| 3 | Event/alert feed with toasts (death, low health/hunger, task done/failed, offline) | H | S-M | Factorio alerts, Mission Planner Messages | Feed no; coordinates in events yes | yes |
| 4 | Keyboard nav (j/k or arrows, 1-9 select, Enter focus, `?` help, Esc) | H | S | k9s, CS observer, Minecraft spectator | No | yes |
| 5 | Command palette (unit by name, view mode, layout, commands) | H | M | VS Code, k9s | Command verbs operator-only; navigation public | yes |
| 6 | Adaptive wall with live-tile budget (N live, rest state cards/stills, low-detail tiles when small) | H | M | Frigate smart streaming, Milestone adaptive streaming, IntersectionObserver | No | yes |
| 7 | PiP / pin (pin a unit as inset while browsing others; swap with main) | H | S-M | QGC video/map swap, Mission Planner HUD detach | No | yes |
| 8 | Fleet radar / minimap (unit dots, state color, click to focus) | H | M | QGC multi-vehicle map, BlueMap markers, Factorio map | Yes (positions private) | yes |
| 9 | Alert acknowledge (ack/snooze with timeout, un-ack) | M | S | PagerDuty, Factorio per-type mute | Yes (shared ack state) | no |
| 10 | Sound / desktop notification, per-type mute | M | S | Factorio alerts, VS Code DND | No (local setting) | no |
| 11 | Follow / auto-director (switch live tile to the unit with the most interesting event, hold time, manual override) | H | M | CS observer camera modes, SC2 observer | No | no |
| 12 | Fullscreen / kiosk mode (hide chrome, auto-director on) | M | S | VS Code Zen Mode | No | no |
| 13 | URL-shareable views (selected unit, camera mode, layout, filter in query string) | M | S | Grafana variables | No (never include coordinates) | no |
| 14 | Unit groups and tags (filter, color, group tiles) | M | M | Frigate camera groups, Grafana variables | Edit operator-only; view public | no |
| 15 | Saved layouts (wall arrangement, budget, columns) | M | M | Foxglove layouts, SC2 observer layout presets | Shared save operator-only; local per-browser OK | no |
| 16 | Sparklines of health/hunger per unit in roster | M | S-M | Grafana, Mission Planner quick tab | No | no |
| 17 | Per-unit state timeline (online/dead/task bars) | M | M | Grafana state timeline/status history | No | no |
| 18 | Event-annotated timeline / replay of recent history | M | L | Grafana annotations, Replay Mod | Yes if it carries coordinates | no |
| 19 | Bulk select + bulk commands (stop, recall, assign) with confirm | H | M | RimWorld work grid, k9s, QGC confirmations | Yes | no |
| 20 | Tour / sequence mode (rotate focus through units on a timer) | L-M | S | CCTV tours (not read; unverified) | No | no |
| 21 | Mobile layout (single column, roster + one tile, no wall) | M | M | Frigate substream per device | No | no |
| 22 | Work-priority grid (units x task types, 1-4) | M | M | RimWorld Work tab | Yes | no |
| 23 | Degraded-mode indicator ("low detail", budget exceeded) | L | S | Frigate low bandwidth mode | No | no |
| 24 | Radar layers/toggles (units, zones, hazards) | M | M | BlueMap marker sets | Yes | no |
| 25 | Hotkey cheat sheet overlay | M | S | k9s `?`, SC2 F11 help | No | no |

## 4. Recommended first slice

Build on the existing SSE stream and add no new video load. Ship: roster table, event feed with toasts, status bar, keyboard nav plus command palette (unit jump and view switching for everyone; command verbs only for signed-in operators), and an adaptive wall with a live-tile budget. The budget gives live WebGL to the focused, pinned and on-screen tiles first; other tiles show a state card or periodic still, as Frigate's smart streaming does, and a tile can be promoted to live on a notable event. The default budget number (a handful) should be tuned by measurement, not taken from this research. Add PiP/pin so one unit stays live while browsing, and an operator-only radar fed by the same stream, never sent to public sessions (filter server-side, do not just hide in the DOM). Defer acks, auto-director, groups, layouts, timelines and bulk commands to later slices; auto-director is the natural next step once the budget and event feed exist.

## 5. Sources

Fetched directly unless noted.

- Factorio Alerts: https://wiki.factorio.com/Alerts
- RimWorld Work: https://rimworldwiki.com/wiki/Work
- SC2 observer (search results only; Liquipedia fetch 403): https://tl.net/blogs/474765-observing-with-gameheart-ui-tips , https://liquipedia.net/starcraft2/Replay_Features
- QGC Fly View: https://docs.qgroundcontrol.com/master/en/qgc-user-guide/fly_view/fly_view.html
- QGC toolbar: https://docs.qgroundcontrol.com/master/en/qgc-user-guide/fly_view/fly_view_toolbar.html
- Mission Planner Flight Data: https://ardupilot.org/planner/docs/mission-planner-flight-data.html
- Foxglove layouts: https://docs.foxglove.dev/docs/visualization/layouts
- Minecraft Wiki Spectator: https://minecraft.wiki/w/Spectator
- BlueMap Markers: https://bluemap.bluecolored.de/wiki/customization/Markers.html
- ReplayMod repo: https://github.com/ReplayMod/ReplayMod
- CS:GO Spectator Tools (via search summary; direct fetch 403): https://developer.valvesoftware.com/wiki/CS:GO_Spectator_Tools
- Frigate live view: https://docs.frigate.video/configuration/live/
- Milestone adaptive streaming: https://doc.milestonesys.com/2025R1/en-US/feature_flags/ff_adaptivestreaming/sc_adptstreamcheckadaptstreamset.htm
- Grafana state timeline: https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/state-timeline/
- Grafana status history: https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/status-history/
- Grafana alert list: https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/alert-list/
- Grafana annotations: https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/annotate-visualizations/
- Grafana variables: https://grafana.com/docs/grafana/latest/dashboards/variables/
- VS Code UI: https://code.visualstudio.com/docs/getstarted/userinterface
- k9s commands: https://k9scli.io/topics/commands/
- PagerDuty incidents: https://docs.pagerduty.com/incident-management/incidents/overview
- MDN IntersectionObserver: https://developer.mozilla.org/en-US/docs/Web/API/IntersectionObserver

Gaps (fetch failed or not covered): RimWorld alerts/colonist bar, Factorio map/train overview, Liquipedia SC2 observer, Formant, Genetec, Dynmap, Linear, tmux, Replay Mod feature details.

## 6. Built in JDW-130 slice 1

Ranks 1-8 and 12 (kiosk), 25 (help overlay) shipped, plus a first cut of 19 (select rows in the roster, stop selected or stop all, with a confirm). Where the build differs from the recommendation:

- The live-tile budget is page-based (Cards, 4, 6, 9, 12, All; `,` and `.` turn pages) rather than promoted on events or by IntersectionObserver. Auto-promotion needs the auto-director (rank 11) and is the next step.
- The event feed is derived in crew-control from the status poll (no change to the units): died, offline, low health or hunger, task started, done, failed with the unit's reason, ended with an unknown outcome when the task has already left the unit's recent list, and stuck (no progress and no movement of more than 3 blocks for 90 s). It keeps the last 200 in memory, so a restart empties it.
- Positions are only ever sent on signed-in streams; the radar and everything position-bearing is operator-only, filtered on the server.
