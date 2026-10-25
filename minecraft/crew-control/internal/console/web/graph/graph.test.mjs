// DOM-level checks for the graph component on its own: wheel units, the drag
// lifecycle, the accessible name of a node, the selected mark and the legend
// markup. Uses the fake DOM in fake-dom.mjs; real layout and painting are left
// to the end-to-end run.
import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, one, all, classesOf } from './fake-dom.mjs';

const { doc, win, observers } = installDom();
const { createDagGraph, wheelPixels } = await import('./graph.js');
const { jobModel, stepChip } = await import('./jobs.js');

const step = (key, deps, state = 'waiting', extra = {}) => ({ key, state, depends_on: deps, template: { template: 'mine_shaft' }, ...extra });
const job = (steps) => ({ id: 'j', kind: 'plan', spec: { template: 'p' }, state: 'running', steps });

function graph(opts, steps = [step('a', [], 'done'), step('b', ['a'], 'running'), step('c', ['a'], 'queued')]) {
  const g = createDagGraph({ label: 'Test graph', ...opts });
  doc.body.append(g.root);
  g.connect();
  g.setData(jobModel(job(steps)));
  const svg = one(g.root, 'dg-canvas');
  return {
    g, svg,
    node: id => all(g.root, n => n.getAttribute('data-id') === id)[0],
    transform: () => all(g.root, n => classesOf(n).includes('dg-zoom'))[0].getAttribute('transform'),
    // No transform yet means the view was never moved.
    xy: () => {
      const m = /translate\((\S+) (\S+)\)/.exec(all(g.root, n => classesOf(n).includes('dg-zoom'))[0].getAttribute('transform') || '');
      return m ? m.slice(1).map(Number) : [0, 0];
    },
    scale: () => Number(/scale\((\S+)\)/.exec(all(g.root, n => classesOf(n).includes('dg-zoom'))[0].getAttribute('transform'))[1]),
  };
}

test.afterEach(() => { doc.body.children.length = 0; doc.activeElement = null; });

// ---- the wheel ----

test('wheelPixels: pixel, line and page units land on one scale', () => {
  assert.equal(wheelPixels({ deltaMode: 0, deltaY: 100 }), 100);
  assert.equal(wheelPixels({ deltaMode: 1, deltaY: 3 }), 48, 'three lines are three 16px rows');
  assert.equal(wheelPixels({ deltaMode: 2, deltaY: 1 }, 500), 200, 'a page is a viewport tall, then bounded');
  assert.equal(wheelPixels({ deltaMode: 2, deltaY: 0.1 }, 500), 50);
  assert.equal(wheelPixels({ deltaMode: 0, deltaY: -5000 }), -200, 'a free-spinning wheel is bounded');
});

test('wheel: a Firefox line notch zooms like the equivalent pixels, not 0.4%', () => {
  const lines = graph();
  lines.svg.dispatch('wheel', { deltaMode: 1, deltaY: -3, clientX: 0, clientY: 0 });
  const pixels = graph();
  pixels.svg.dispatch('wheel', { deltaMode: 0, deltaY: -48, clientX: 0, clientY: 0 });
  assert.ok(Math.abs(lines.scale() - pixels.scale()) < 1e-9);
  assert.ok(lines.scale() > 1.05, `a notch zooms visibly (${lines.scale()})`);
});

// ---- the drag lifecycle ----

const press = (svg, over = {}) => svg.dispatch('pointerdown', { button: 0, pointerId: 1, clientX: 100, clientY: 100, buttons: 1, ...over });
const move = (svg, dx, over = {}) => svg.dispatch('pointermove', { pointerId: 1, clientX: 100 + dx, clientY: 100, buttons: 1, ...over });

test('drag: a held pointer pans, with capture once it is a drag', () => {
  const t = graph();
  press(t.svg);
  move(t.svg, 40);
  assert.equal(t.xy()[0], 40);
  assert.ok(t.svg.hasPointerCapture(1), 'captured so the release is delivered off the svg');
  t.svg.dispatch('pointerup', { pointerId: 1 });
  assert.equal(t.svg.hasPointerCapture(1), false, 'and released at the end');
  move(t.svg, 90);
  assert.equal(t.xy()[0], 40, 'a hover after the release does not pan');
});

for (const ending of ['pointercancel', 'lostpointercapture', 'contextmenu']) {
  test(`drag: ${ending} ends it, so a later hover does not pan`, () => {
    const t = graph();
    press(t.svg);
    move(t.svg, 30);
    t.svg.dispatch(ending, { pointerId: 1 });
    move(t.svg, 80);
    assert.equal(t.xy()[0], 30);
  });
}

test('drag: a window blur ends it', () => {
  const t = graph();
  press(t.svg);
  move(t.svg, 30);
  win.fire('blur');
  move(t.svg, 80);
  assert.equal(t.xy()[0], 30);
});

test('drag: a release off the svg before capture is noticed by the first move without a button', () => {
  const t = graph();
  press(t.svg);
  move(t.svg, 2); // below the drag threshold: not captured yet
  move(t.svg, 60, { buttons: 0 }); // the button came up elsewhere
  assert.equal(t.xy()[0], 0, 'nothing panned');
  move(t.svg, 90);
  assert.equal(t.xy()[0], 0, 'and the drag is over');
});

test('drag: a secondary button never starts one', () => {
  const t = graph();
  press(t.svg, { button: 2, buttons: 2 });
  move(t.svg, 60);
  assert.equal(t.xy()[0], 0);
});

test('drag: a drag is not a click on the node under it', () => {
  const picked = [];
  const t = graph({ onSelect: id => picked.push(id) });
  press(t.svg);
  move(t.svg, 40);
  t.svg.dispatch('pointerup', { pointerId: 1 });
  t.node('a').click();
  assert.deepEqual(picked, []);
  press(t.svg);
  t.svg.dispatch('pointerup', { pointerId: 1 });
  t.node('a').click();
  assert.deepEqual(picked, ['a']);
});

test('connect/disconnect: the window listener and observer follow the graph on and off screen', () => {
  const baseline = win.count('blur');
  const obs = observers.size;
  const t = graph();
  assert.equal(win.count('blur'), baseline + 1);
  assert.equal(observers.size, obs + 1);
  t.g.connect();
  assert.equal(win.count('blur'), baseline + 1, 'connecting twice adds nothing');
  press(t.svg);
  move(t.svg, 30);
  t.g.disconnect();
  assert.equal(win.count('blur'), baseline);
  assert.equal(observers.size, obs);
  assert.equal(t.svg.hasPointerCapture(1), false, 'and a drag in progress is let go');
});

// ---- what a node says and shows ----

test('node name: state, the critical path in words, and a failure reason', () => {
  const t = graph({}, [
    step('a', [], 'done'),
    step('b', ['a'], 'failed', { reason: 'no pickup after 3 breaks' }),
    step('lone', [], 'waiting'),
  ]);
  const name = id => t.node(id).getAttribute('aria-label');
  assert.match(name('a'), /^a, .*done, on critical path$/);
  assert.match(name('b'), /failed, on critical path, no pickup after 3 breaks$/);
  assert.doesNotMatch(name('lone'), /critical path/, 'a step off the path says nothing of it');
  assert.ok(classesOf(t.node('a')).includes('dg-path'), 'the thicker outline still marks it');
});

test('selected: the node is marked visibly and with aria-current, and only that node', () => {
  const t = graph();
  const model = jobModel(job([step('a', [], 'done'), step('b', ['a'], 'running'), step('c', ['a'], 'queued')]));
  for (const n of model.nodes) n.selected = n.id === 'b';
  t.g.setData(model);
  assert.equal(t.node('b').getAttribute('aria-current'), 'true');
  assert.ok(classesOf(t.node('b')).includes('dg-on'));
  assert.equal(t.node('a').getAttribute('aria-current'), null);
  assert.ok(!classesOf(t.node('a')).includes('dg-on'));
  for (const n of model.nodes) n.selected = false;
  t.g.setData(model);
  assert.equal(t.node('b').getAttribute('aria-current'), null, 'the mark is removed with the selection');
});

test('legend: a list whose children are list items', () => {
  const t = graph();
  t.g.setLegend(['waiting', 'done'].map(stepChip));
  const legend = one(t.g.root, 'dg-legend');
  assert.equal(legend.getAttribute('role'), 'list');
  assert.equal(legend.children.length, 2);
  for (const item of legend.children) assert.equal(item.getAttribute('role'), 'listitem');
  assert.equal(all(t.node('a'), n => n.getAttribute('role') === 'listitem').length, 0, 'node chips are not list items');
});

test('cycle note: names only the steps on the cycle', () => {
  const t = graph({}, [step('x', [], 'done'), step('a', ['x', 'b']), step('b', ['a']), step('c', ['b'])]);
  assert.match(t.g.note.textContent, /cycle: a, b —/);
  assert.doesNotMatch(t.g.note.textContent, /\bc\b/);
  assert.equal(t.g.note.hidden, false);
});
