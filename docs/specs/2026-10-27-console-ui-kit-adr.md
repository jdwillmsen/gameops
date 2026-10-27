# ADR: console UI approach, framework or shared kit

Status: proposed, 2026-10-27. Scope: `minecraft/crew-control/internal/console/web` (the operator console). The camera page in `minecraft/crew` is out of scope.

## Decision

Adopt **option B: an in-house shared kit of plain ES-module helpers and components**, extracted incrementally under `web/kit/`, converted view by view (strangler). Do not adopt a framework now. Vendored Preact + htm (option C) is the runner-up and the named fallback if the revisit triggers below fire.

## Context

The console is vanilla ES modules served by a Go binary (`go:embed` with an explicit file list), no bundler, no third-party runtime dependency, tested with `node --test` against a hand-written fake DOM. It has one maintainer plus agents and renders up to 20+ live feeds. `docs/ui-design-principles.md` is the rulebook; it already says "vanilla ES modules, no dependencies, no bundler".

## Measurements (2026-10-27, `origin/main` at `92699652`)

All numbers come from `wc` and `grep` over `git ls-files` in `web/`; the commands are in the last section.

### Size and coverage

| Measure | Value |
|---|---|
| Non-test JS/MJS files | 65 (includes charts, graph, demos, fake DOM), 12,321 lines |
| Test files / lines | 39 / 4,584; `node --test` runs 400 tests in 0.42 s, all green |
| CSS files / lines | 17 / 1,248 |
| Shipped payload (js, css, html, no demos or tests) | 549,486 bytes raw, 163,206 bytes gzip |
| View modules with no test file | 11 (`console.js`, `ops.js`, `overview-ui.js`, `dash-ui.js`, `explorer-ui.js`, `heat-ui.js`, `insights-ui.js`, `containers-ui.js`, `invhistory-ui.js`, `bulk-ui.js`, `director-ui.js`), 3,542 lines. Pure logic next to them is tested; the DOM halves are not |
| Fake-DOM test files | 6 (`graph/*`, `detail-ui`, `replay-ui`, `timeline-ui`), all on `graph/fake-dom.mjs` |

### DOM-building boilerplate

- `el(tag, props, ...kids)` is defined privately in **20 files**, in two incompatible variants: 9 use `Object.assign(createElement(tag), props)` (alerts, bulk-ui, console, dash-panels, dash-ui, ops, overview-ui, squad, timeline-ui) and 11 route `-`/`role` keys through `setAttribute`. The charts library has a third (`charts/dom.js` `h`).
- 420 `textContent` writes, 219 `addEventListener`, 237 `setAttribute`, 179 `append`, 103 `replaceChildren`, 31 direct `createElement`, **0 `innerHTML`** (a property worth keeping).

### Repeated patterns (each hand-built per view)

| Pattern | Count | Note |
|---|---|---|
| Empty-state elements | about 24 across 8 class names (`empty`, `db-empty`, `dg-empty`, `tl-empty`, ...) | no shared wording or role |
| `Could not ...` error text | 7 | principle requires it everywhere |
| `Sign in to ...` signed-out state | 8 | |
| `Loading...` state | 2 | the principles require it in every view |
| `role="status"` live regions | 16 | |
| Sort headers with `aria-sort` | 3 (`overview-ui.js:397`, `ops.js:99`, `explorer-ui.js:287`) | |
| `aria-pressed` toggles | 43 | |
| Dialogs | 3 (`bulk-ui.js`, help and palette in `ops.js`), plus 2 native `confirm()` | no focus trap, no focus return (`ops.js:410`) |
| Toasts | 1 (`alerts.js`) | |
| Tooltips | 1 shared (`charts/tooltip.js`) | fine as is |
| `keydown` listeners | 15 files; 16 `Escape` branches; `isTyping` used in 2 files | `console.js:278-280` chains view Escapes by hand |
| `EventSource` | 4 (`console.js:310`, `alerts.js:197`, `squad.js:110`, `explorer-ui.js:107`) | 3 carry their own close and backoff code |
| `.focus()` calls | 12 | no helper keeps focus across redraws |

### State sharing

`state.js` (194 lines) is pure helpers (view list, address parse/format, request validators), not a store. Live state is module-level `let`s in `console.js` (`state`, `operator`, `jobs`, `places`, `panel`, ...). Each of 14 `initX(host)` views receives a `host` object of callbacks (counts of use: `host.operator` 31, `host.state` 30, `host.api` 21, `host.statuses` 20, `host.focus` 18, `host.setView` 12, `host.order` 12). `console.js` fans each stream event out by hand (the `events` handler calls five views, `jobs` calls three). It works, and is easy to follow, but every new view edits `console.js` in several places (`docs/ui-design-principles.md`, "Adding a view", step 4).

### CSS tokens

Tokens are defined once (`console.css:65`, 10 values) and chart tokens in `charts/charts.css`. Outside both, the view stylesheets contain **190 hex literals (96 distinct)**, of which **50 repeat a token value** (`#333` 13 times, `#ddd` 7, `#111` 6, `#1b1b1b` 6, `#f0b040` 6, `#6fd08c` 4, `#ff5d5d` 4, `#5fd0ff` 3). The focus ring has three colours: global `--focus` (`console.css:66`), `#4a7bb0` in `overview.css:77,92` and `detail.css:78`, and `--cc-series-1` in charts and timeline. This blocks the themes ticket (JDW-227).

### Cost of the recent accessibility findings

Four fix commits landed on 2026-10-27: `12c404a5` (focus ring and surface tokens), `c4f157c4` (roster sort and rows keyboard operable), `1db117ec` (dimmed text contrast), `cf4ee8c0` (focus restore, key test). Together 5 files and about 50 changed lines. The edits were small; the cost was **finding** them, because nothing in the tests exercises the DOM of 11 view modules. The spike below found three more, in the first panel it looked at:

1. **Unnamed selects.** `alerts.js` and `bulk-ui.js` use the `Object.assign` variant of `el` and pass `'aria-label'`, which sets a JS expando, not the attribute. In Chromium: 204 selects in the alerts panel, **0 with an `aria-label`** (7 call sites: `alerts.js:135,136,164,180,183`, `bulk-ui.js:32,33`). Ticketed as JDW-278.
2. **Focus lost on live redraw.** The panel re-renders every 5 s with `replaceChildren`; a focused Ack button is dropped. Reproduced by the `BASELINE LOSES focus` test.
3. **Overlays never return focus** and do not trap Tab, against the checklist in `docs/ui-design-principles.md`.

All three are one root cause: no shared builder or redraw rule, so each of 20 view files re-decides it.

## Options

| | Fits the constraints | Verdict |
|---|---|---|
| A. Status quo | Yes, but defects repeat per view and the 20 `el` copies keep diverging | Rejected: the audit and the spike show a recurring defect class |
| B. In-house kit, extracted incrementally | Yes: no dependency, no build, runs on the existing fake DOM, converts one view at a time | **Chosen** |
| C. Vendored pinned Preact + htm (+ hooks), no bundler | Mostly: 3 files, MIT/Apache-2.0, about 7 KB gzip; needs a DOM shim for tests and a supply-chain review | Runner-up |
| D. Vendored Lit | Weaker: 5 packages (lit, lit-html, lit-element, @lit/reactive-element, ssr-dom-shim) with bare imports that need an import map; BSD-3; custom elements and shadow DOM cannot run on the fake DOM, so tests need jsdom or happy-dom (a dev dependency) and the global focus ring and tokens must pierce shadow roots | Rejected without a spike: the test story alone fails the constraint |
| E. Vite + framework | Breaks "no build step" and the Go embed list; React alone is 69 KB gzip | Rejected |

Measured bundle sizes (esbuild, minified, tree-shaken, in a scratch directory): minimal Lit component 6.1 KB gzip, Preact + hooks + htm 6.4 KB, React + react-dom 69 KB.

## Spike: alerts panel, B versus C

Throwaway branch `spike/console-ui-kit` (commit `687420d2`, never merged). The alerts panel (`initAlerts`, 157 lines) was rewritten twice with the same host contract: `spike/alerts-kit.js` on `kit/` (`h`, `createPanel`, `stateView`, `createStore`, `keepFocus`) and `spike/alerts-preact.js` on vendored `preact@11.0.2`, `preact/hooks`, `htm@3.1.1`. One behaviour suite (`spike/alerts.spike.test.mjs`, 9 cases) runs against the shipped panel, the kit and Preact: 27 tests, all pass; two of them assert the shipped panel's defects.

| Measure | Shipped (A) | Kit (B) | Preact + htm (C) |
|---|---|---|---|
| View code, lines | 157 | 120 (+ kit library 139 lines, shared by every view) | 138 |
| View code, bytes | 7,315 | 6,655 | 7,133 |
| Added to payload | 0 | 5,389 B raw, 2,288 B gzip (library, all views) | 16,904 B raw, 7,233 B gzip vendored (+4.4 % of the 163 KB console) |
| Test infrastructure beyond the existing fake DOM | none | none | 54-line shim (`preact-dom.mjs`: node API, `data`, `insertBefore`, event `this` binding) plus `requestAnimationFrame`, `cancelAnimationFrame`, `queueMicrotask` globals |
| Selects with accessible name (Chromium, 200 alerts) | 0 / 204 | 204 / 204 | 204 / 204 |
| Focus survives the 5 s redraw | no | yes (`keepFocus`) | yes (diffing) |
| Render, 200 alerts, 2,420 nodes (Chromium headless, one run each, noisy) | first 82 ms, tick 14 ms, one change 13 ms | first 80 ms, tick 12 ms, one change 11 ms | first 11 ms (78 ms on the cold run), tick 5 ms, one change 0.7 ms |

Reading the numbers:

- **LoC**: the kit saves 24 % on the view; Preact saves 12 %. The kit's real payoff is that `panel`, states and `h` are written once; Preact's payoff is declarative markup. Neither changes the shape of the work: the pure logic in `alerts.js` is untouched in both.
- **Render cost**: Preact's diffing is clearly cheaper when 200 rows change slightly (5 ms against 12 ms per redraw). Both kit and shipped redraw by rebuilding the list, and all three stay under one 16 ms frame at 200 rows; at the 20-row scale alerts actually have, the difference is not measured and is unlikely to matter. The weak spot of B is therefore **many-row views redrawn at 1 Hz or more**; the kit must offer keyed row patching (a `keyed(parent, items, key, make, update)` helper) before it converts the explorer and fleet tables. Preact also showed 201 DOM mutation records per unchanged tick that were not investigated; do not read its win as free.
- **Test ergonomics**: B tests run unmodified on `graph/fake-dom.mjs`. C needed a 54-line shim, and any future Preact feature (portals, refs, effects timing) can need more. htm templates are plain template strings: a typo is a runtime error, not caught by `syntax.test.mjs`.
- **a11y**: both repair the unnamed selects because both stop relying on `Object.assign`; the bug disappears in B because `h` has exactly one rule for attributes.

Not measured: real-device low-end performance, memory, and a multi-view Preact app (hooks, context). The spike used one view.

## Recommendation: B, with C as a named fallback

Reasons, in order of weight:

1. **Constraint fit.** B adds no dependency, no review burden, no build, no second test harness, and no new paradigm for agents and the maintainer to hold alongside the existing imperative views and the chart library.
2. **The defects are builder and redraw-rule defects, not rendering-model defects.** One attribute rule, one focus-keeping redraw, one dialog and one set of states fix all three spike findings.
3. **Strangler works with B at zero marginal risk.** An unconverted view and a converted one are both plain DOM; with Preact two rendering models coexist in the same page for a year.
4. **Cost of being wrong is low and asymmetric.** Kit code is about 140 lines today; if it grows past its budget it can be replaced by Preact view by view, because the views already separate pure logic from DOM.

Revisit (re-open this ADR) when any of these holds:

- the kit's keyed list or table code passes about 400 lines, or
- two views need per-row reactive updates at 1 Hz or more over 100+ rows and the keyed helper cannot keep a redraw under 8 ms in the browser, or
- the same kit bug class recurs after migration (three separate defects traced to hand-rolled diffing).

At that point adopt C under the guardrails below. Lit stays rejected unless the fake-DOM constraint is dropped.

## Migration plan (strangler)

Rule: new views use the kit from day one; existing views convert only when touched for another reason or when scheduled below. No big-bang rewrite, no view is left half-converted across a release.

| Phase | Work | Ticket |
|---|---|---|
| 0 | Decision, spike branch | JDW-276 (this) |
| 0 | Fix the unnamed selects now, independent of the kit | JDW-278 |
| 1 | Kit slice 1: `h`, `keepFocus`, states, panel, tests, embed, docs | JDW-277 |
| 2 | Store, SSE subscription helper with shared backoff | JDW-279 |
| 2 | Dialog (trap, focus return), toast, tabs, key registry | JDW-280 |
| 3 | First conversion, the alerts panel (spike code, UI test) | JDW-282 |
| 4 | Worst-duplicated: sort tables (explorer, fleet, roster) onto a kit table; overlays onto the dialog | JDW-281, JDW-283 |
| 5 | Mechanical: delete the 20 `el` copies, replace the 50 token literals, add guard tests | JDW-284 |

Order inside phase 4 follows measured duplication: tables first (3 hand-written `aria-sort` sites, the roster just needed an a11y fix), then overlays (no focus return, 2 native `confirm()`). Views being changed by the world-selector work (JDW-271) convert after it lands to avoid conflicts. Each conversion adds the first DOM test its module has.

## Guardrails

1. **No dependency without all of**: exact pinned version, source tarball integrity hash recorded next to the vendored file, an OSI licence (MIT, BSD, Apache-2.0, ISC) with its text committed, a diff review of the vendored bytes, a stated size, and an amendment to this ADR. Vendor files live under `web/vendor/` and are embedded; no `npm install` at build time, no CDN, no bundler.
2. **Size budget.** Kit total at most 10 KB gzip (slice 1 at most 6 KB; spike library is 2.3 KB). Any vendored runtime at most 8 KB gzip. Whole console payload at most 180 KB gzip (163 KB today), checked by a test that gzips the embedded files.
3. **Review tier.** A vendored library is R1 (dependency) at minimum and is reviewed by an agent of another model family before merge; a major-version bump re-opens the review.
4. **Kit rules.** Text reaches the DOM only through `textContent` (no `innerHTML`); the kit imports nothing from views; every kit component has a fake-DOM test; every kit file is added to the `go:embed` list and the `node --test` glob in `component.yaml`.
5. **Fake DOM** is extended only additively, so existing tests keep their meaning.
6. **Views.** Pure logic in `x.js`, DOM in `x-ui.js` stays. A converted view keeps its host contract and gets a UI test with the conversion.
7. **Performance.** Views with more than 50 rows that redraw at 1 Hz or more use keyed row patching, never `replaceChildren` of the whole list, and are measured in the browser.

## Reproducing the measurements

From a checkout of `origin/main` at `92699652`, in `minecraft/crew-control/internal/console/web`:

- sizes: `git ls-files | grep -E '\.js$|\.mjs$' | grep -v test | xargs wc -l`; payload: `cat` of the non-test, non-demo js, css and html through `gzip -9 | wc -c`
- patterns: `grep -ho <pattern> $(git ls-files | grep '\.js$' | grep -v test) | wc -l`
- token literals: `grep -ho "#[0-9a-fA-F]\{3,6\}\b"` over the non-chart CSS
- spike: `git checkout spike/console-ui-kit`, then `node --test spike/alerts.spike.test.mjs`; for timings serve `web/` with any static server and open `/spike/bench.html?impl=shipped|kit|preact&n=200`, then read `window.__result`
- bundle sizes: esbuild `--bundle --minify --format=esm` of a one-component entry per library, in a scratch directory outside the repository
- licences and versions (npm, 2026-10-27): preact 11.0.2 MIT, htm 3.1.1 Apache-2.0, lit 3.3.3 BSD-3-Clause
