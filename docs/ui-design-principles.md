# Console UI design principles

Applies to the crew-control console (`minecraft/crew-control/internal/console/web`) and the camera page in `minecraft/crew`. The README is the design record for each view; this file is the rulebook new views are held to.

## Principles

1. **Vanilla ES modules, no dependencies, no bundler.** Files load as `/static/<name>.js`.
2. **Pure logic in `x.js` with a node test; DOM in `x-ui.js`.** A view's decisions (sorting, bucketing, labels) must be testable without a DOM.
3. **Keyboard first.** Every view has a single-letter toggle key and a button whose `title` names the view and the key, e.g. `Heatmap (h)`.
4. **Accessible.** Real buttons and links, labels on every control, visible focus, state never carried by colour alone.
5. **Public vs operator split.** Coordinates and anything private are served only on signed-in routes with `Cache-Control: no-store`; signed-out visitors get a sign-in state, not an error.
6. **Bounded cost.** Every endpoint and every redraw has a cap (rows, window, bytes, frame rate).
7. **Closed vocabularies.** Anything shown to the user that came from a unit is mapped through a fixed list; unknown values render as a neutral word, never raw text.
8. **Dark monospace theme.** Surfaces come from tokens, not literals.

## Tokens

Defined once in `console.css` (`:root`); chart tokens in `charts/charts.css`. Do not redeclare them in a view stylesheet; view-local variables are prefixed (`--ov-`, `--hm-`).

| Token | Value | Use |
|---|---|---|
| `--bg` | `#111` | page background |
| `--surface` | `#1b1b1b` | bar, panels, cards |
| `--text` | `#ddd` | body text (12.7:1 on `--surface`) |
| `--border` / `--line` | `#333` / `#2a2f2c` | dividers |
| `--ok` `--warn` `--crit` `--info` | `#6fd08c` `#f0b040` `#ff5d5d` `#5fd0ff` | status, all at least 5.7:1 on both surfaces |
| `--focus` | `#5fd0ff` | focus ring (global `:focus-visible`) |
| `--cc-series-1..8`, `--cc-status-*` | see `charts/charts.css` | chart marks, validated in `charts/palette-validation.txt` |

Dimmed secondary text must keep `opacity` at .55 or above; .5 measures 4.1:1 on `#1b1b1b` and fails.

## View registry and keys

The registry is `web/views.js`. `views.test.mjs` fails if a registered view lacks a key or title, if keys or toggles collide, if `index.html` disagrees, or if `console.js` has no handler for the key.

| Key | View | Toggle id |
|---|---|---|
| `w` | Wall | `wall-toggle` |
| `o` | Fleet overview | `fleet-toggle` |
| `b` | Dashboard | `dash-toggle` |
| `d` | Unit detail | `detail-toggle` |
| `h` | Heatmap | `heat-toggle` |
| `g` | Job graph | `jobgraph-toggle` |
| `x` | Event explorer | `explorer-toggle` |
| `l` | World ledger | `ledger-toggle` |
| `t` | Timeline | `timeline-toggle` |
| `y` | Replay | `replay-toggle` |

Other keys: `1`-`9`,`0` pick a unit; `[` `]` step units; `c` camera mode; `Shift-J` jobs, `Shift-S` squads, `Shift-A` alerts; `Ctrl/Cmd-K` palette. Insights is a dashboard panel and signin is its own page (`signin.html`); neither takes a toggle key.

## Adding a view

1. Add `x.js` (pure, with `x.test.mjs`) and `x-ui.js` exporting `initX(host)` with `mount(stage)` / `unmount()`.
2. Append the view name to `VIEWS` in `state.js` and a row to `views.js`.
3. Add the toggle button to `index.html` with `title="Name (k)"`.
4. In `console.js`: import and init, unmount in `renderStage`, mount for its view, a `toggleX`, the key in `onKey`, and `aria-pressed` in `renderBar`.
5. Provide signed-out, loading, empty and error states (below). Run the crew-control `test` task.
6. Document the view in the README.

## States

Every view paints exactly these, in plain sentences, in the stage:

- **Signed out** (operator-only data): heading `Sign in to <do the thing>` and a link to `/signin`.
- **Loading**: `Loading...` in a `role="status"` element; never a blank stage.
- **Empty**: says what is empty and why (`No events yet.` vs `No events match these filters.`).
- **Error**: `Could not <verb>.` plus the server message or `Failed (<status>).`, and a retry where it is safe.

## Accessibility checklist

- [ ] Reachable by Tab; order follows reading order; no focus trap except modal overlays, which return focus on close.
- [ ] Interactive elements are `button`/`a`/`input`; custom widgets get `tabindex`, role, and Enter/Space.
- [ ] Every control has a visible label or `aria-label`; repeated controls name their target.
- [ ] Sortable headers carry `aria-sort`; toggles carry `aria-pressed`.
- [ ] Canvas and SVG have a text equivalent (`aria-label` plus a table or list view).
- [ ] Meaning survives greyscale: pair colour with a word, shape, or pattern.
- [ ] Text contrast at least 4.5:1 on `#111` and `#1b1b1b`; marks at least 3:1.
- [ ] Live updates announce through `role="status"`, not focus moves.
- [ ] Letter keys are ignored while typing (`isTyping`) and with Ctrl/Cmd/Alt held.
- [ ] Respects `prefers-reduced-motion`.

## Chart rules

The README's "Chart library" section is the reference; the rules a new chart must meet:

- Use `web/charts`; no other chart code. Colours are the `--cc-*` tokens, never literals.
- Categorical order is fixed per dashboard (`createRegistry()`); past eight series the tail folds into a grey "Other (n)"; never generate a ninth hue.
- Change a hue only after re-running the dataviz validator; `palette.test.mjs` fails on drift.
- Every panel keeps its **Table** view, keyboard point navigation and live-region readout.
- Two or more series get a legend that toggles series.
- Status colours carry an icon and a word; texture mode must stay working so hue is not the only identity channel.
- Series and category names reach the DOM through `textContent` only.
- Redraw through the shared frame-budgeted queue; `update()` at most about 1 Hz per chart.
