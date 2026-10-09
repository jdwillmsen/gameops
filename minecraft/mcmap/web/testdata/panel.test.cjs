'use strict';

// What the layer panel does, checked by doing it: the panel's own script
// and the script that keeps the record are run in a page of the kind
// dom.cjs makes, rows are registered as the layers' scripts register them,
// and the lines are pressed as a viewer would press them. Run by
// `go test ./web/` where there is a node to run it with, or by hand:
// `node web/testdata/panel.test.cjs`.
const assert = require('node:assert/strict');
const { page } = require('./dom.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// A page with the rows a server with everything gives it. Each layer's
// script is stood in for by the few lines of it that talk to the panel.
async function load(storage = new Map(), options = {}) {
  const p = page(storage, options);
  const { layers } = p;
  const rows = {};
  const add = (spec, items) => {
    const row = layers.register(spec);
    rows[`${spec.group}/${spec.id}`] = row;
    if (items) row.setItems(items);
    return row;
  };
  const named = (ids) => ids.map((id) => ({ id, label: id[0].toUpperCase() + id.slice(1) }));
  add({ group: 'live', id: 'players', label: 'Players', section: 'players', whole: true, facet: 'players' }, named(['alex', 'steve']));
  add({ group: 'live', id: 'hostile', label: 'Hostile', section: 'mobs', facet: 'mobs', order: 20 }, named(['creeper', 'zombie']));
  add({ group: 'live', id: 'passive', label: 'Passive', section: 'mobs', facet: 'mobs', order: 30 }, named(['cat', 'cow', 'wolf']));
  add({ group: 'markers', id: 'waypoints', label: 'Waypoints', facet: 'waypoints', order: 10 }, options.waypoints || named(['home', 'mine']));
  add({ group: 'markers', id: 'beds', label: 'Beds', facet: 'beds', order: 20 }, named(['red', 'blue']));
  add({ group: 'markers', id: 'containers', label: 'Containers', facet: 'containers', order: 30 }, named(['chest', 'barrel']));
  add({ group: 'overlays', id: 'slime', label: 'Slime chunks', enabled: false, order: 10 });
  add({ group: 'overlays', id: 'grid', label: 'Grid', enabled: false, order: 30 });
  // On, and greyed out while the grid is off: the page's own default.
  add({ group: 'overlays', id: 'chunk', label: 'Chunk focus', enabled: true, order: 35 }).setAvailable(false);
  await tick();

  const tree = p.doc.getElementById('layers-body').querySelector('.tree');
  const nameOf = (li) => {
    const row = li.children.find((child) => child.classList.contains('row'));
    const name = row ? row.children.find((child) => child.classList.contains('name')) : null;
    return name ? name.textContent : '';
  };
  const kidsOf = (li) => li.children.find((child) => child.classList.contains('kids'));
  const li = (...labels) => {
    let list = tree;
    let at = null;
    for (const label of labels) {
      at = list.children.find((child) => child.classList.contains('node') && nameOf(child) === label) || null;
      assert.ok(at, `there is no line ${labels.join(' > ')}`);
      list = kidsOf(at);
    }
    return at;
  };
  const part = (labels, name) => li(...labels).children.find((child) => child.classList.contains('row')).children.find((child) => child.classList.contains(name));
  const press = async (labels, name = 'check') => {
    part(labels, name).click();
    await tick();
  };
  const open = async (...labels) => {
    if (part(labels, 'twist').getAttribute('aria-expanded') !== 'true') await press(labels, 'twist');
  };
  const state = (...labels) => part(labels, 'check').getAttribute('aria-checked');
  const menu = async (labels, entry) => {
    part(labels, 'more').click();
    const offered = p.panel.querySelectorAll('.menu-item');
    const found = offered.find((item) => item.textContent === entry);
    assert.ok(found, `the menu of ${labels.join(' > ')} has no "${entry}", only: ${offered.map((item) => item.textContent).join(', ')}`);
    found.click();
    await tick();
  };
  const entries = (...labels) => {
    part(labels, 'more').click();
    const offered = p.panel.querySelectorAll('.menu-item').map((item) => item.textContent);
    p.doc.dispatchEvent({ type: 'pointerdown', target: p.doc.body });
    return offered;
  };
  const status = () => p.doc.getElementById('layers-body').querySelector('.panel-status').textContent;
  const on = () => Object.entries(layers.states()).filter(([, shown]) => shown).map(([key]) => key).sort();
  const search = async (typed) => {
    const box = p.doc.getElementById('layers-body').querySelector('input');
    box.value = typed;
    box.dispatchEvent({ type: 'input' });
    await tick();
  };
  const stops = () => tree.querySelectorAll('.check').filter((check) => check.tabIndex === 0);
  return { ...p, rows, tree, li, part, press, open, state, menu, entries, status, on, search, stops, add, tick, storage };
}

test('a line is a checkbox that says mixed when some of what is under it is hidden', async () => {
  const p = await load();
  await p.open('Mobs', 'Passive');
  assert.equal(p.state('Mobs', 'Passive'), 'true');
  await p.press(['Mobs', 'Passive', 'Cow']);
  assert.equal(p.state('Mobs', 'Passive', 'Cow'), 'false');
  assert.equal(p.state('Mobs', 'Passive'), 'mixed');
  assert.equal(p.state('Mobs'), 'mixed');
  assert.equal(p.rows['live/passive'].shows('cow'), false);
  assert.equal(p.rows['live/passive'].shows('cat'), true);
  // A layer switched off keeps what was chosen in it.
  await p.press(['Mobs', 'Passive']);
  assert.equal(p.state('Mobs', 'Passive'), 'false');
  await p.press(['Mobs', 'Passive']);
  assert.equal(p.state('Mobs', 'Passive'), 'mixed');
  assert.equal(p.rows['live/passive'].shows('cow'), false);
});

test('what is chosen is kept, and is there after a reload', async () => {
  const p = await load();
  await p.open('Mobs', 'Passive');
  await p.press(['Mobs', 'Passive', 'Cow']);
  await p.press(['Markers', 'Beds']);
  const q = await load(p.storage);
  assert.equal(q.state('Markers', 'Beds'), 'false');
  assert.equal(q.state('Mobs', 'Passive', 'Cow'), 'false');
  assert.equal(q.part(['Mobs', 'Passive'], 'twist').getAttribute('aria-expanded'), 'true');
});

test('Only on a section has a way back though a row elsewhere is greyed out', async () => {
  const p = await load();
  const before = p.on();
  assert.ok(before.includes('overlays/chunk'), 'the chunk focus is on, and greyed out, to begin with');
  await p.menu(['Markers'], 'Only this section');
  assert.deepEqual(p.on(), ['markers/beds', 'markers/containers', 'markers/waypoints']);
  assert.equal(p.entries('Markers')[0], 'Back to before');
  assert.equal(p.part(['Markers'], 'only').getAttribute('aria-pressed'), 'true');
  // Pressed again it goes back, and does not show the section alone again.
  await p.press(['Markers'], 'only');
  assert.deepEqual(p.on(), before);
  assert.equal(p.entries('Markers')[0], 'Only this section');
});

test('what was shown alone is still alone, with its way back, after a reload', async () => {
  const p = await load();
  const before = p.on();
  await p.press(['Markers', 'Beds'], 'only');
  assert.deepEqual(p.on().filter((key) => key.startsWith('markers/')), ['markers/beds']);
  const q = await load(p.storage);
  assert.deepEqual(q.on().filter((key) => key.startsWith('markers/')), ['markers/beds']);
  assert.equal(q.entries('Markers', 'Beds')[0], 'Back to before');
  assert.equal(q.part(['Markers', 'Beds'], 'only').getAttribute('aria-pressed'), 'true');
  await q.menu(['Markers', 'Beds'], 'Back to before');
  assert.deepEqual(q.on(), before);
  // And that is kept too.
  const r = await load(q.storage);
  assert.deepEqual(r.on(), before);
  assert.equal(r.entries('Markers', 'Beds')[0], 'Only this');
});

test('the way back from Only is to exactly what was on before, not to everything', async () => {
  const p = await load();
  await p.press(['Markers', 'Containers']);
  await p.press(['Markers', 'Containers'], 'only');
  assert.deepEqual(p.on().filter((key) => key.startsWith('markers/')), ['markers/containers']);
  await p.menu(['Markers', 'Containers'], 'Back to before');
  assert.deepEqual(p.on().filter((key) => key.startsWith('markers/')), ['markers/beds', 'markers/waypoints']);
  // Showing everything is its own action, and is still there.
  await p.menu(['Markers', 'Containers'], 'Show all in group');
  assert.equal(p.state('Markers', 'Containers'), 'true');
});

test('switching a row oneself lets go of the way back, and a second Only keeps the first one\'s', async () => {
  const p = await load();
  await p.press(['Markers', 'Containers']);
  await p.press(['Markers', 'Beds'], 'only');
  await p.press(['Markers', 'Waypoints'], 'only');
  await p.menu(['Markers', 'Waypoints'], 'Back to before');
  assert.deepEqual(p.on().filter((key) => key.startsWith('markers/')), ['markers/beds', 'markers/waypoints']);
  await p.press(['Markers', 'Beds'], 'only');
  await p.press(['Markers', 'Waypoints']);
  assert.equal(p.entries('Markers', 'Beds')[0], 'Only this');
  assert.equal(p.part(['Markers', 'Beds'], 'only').getAttribute('aria-pressed'), 'false');
});

test('a section that is partly on is switched off by its checkbox, and comes back as it was', async () => {
  const p = await load();
  await p.press(['Markers', 'Containers']);
  assert.equal(p.state('Markers'), 'mixed');
  await p.press(['Markers']);
  assert.equal(p.state('Markers'), 'false');
  assert.deepEqual(p.on().filter((key) => key.startsWith('markers/')), []);
  // And that is what a reload finds, with the way back.
  const q = await load(p.storage);
  assert.equal(q.state('Markers'), 'false');
  await q.press(['Markers']);
  assert.deepEqual(q.on().filter((key) => key.startsWith('markers/')), ['markers/beds', 'markers/waypoints']);
  assert.equal(q.state('Markers'), 'mixed');
  // A section wholly on goes off and comes back wholly on.
  await q.press(['Mobs']);
  assert.equal(q.state('Mobs'), 'false');
  await q.press(['Mobs']);
  assert.equal(q.state('Mobs'), 'true');
  // With every row switched off by hand there is nothing remembered, and
  // the checkbox shows everything.
  await q.press(['Mobs', 'Hostile']);
  await q.press(['Mobs', 'Passive']);
  await q.press(['Mobs']);
  assert.equal(q.state('Mobs'), 'true');
});

test('an item that is off until asked for filters nothing until the viewer changes something', async () => {
  const p = await load();
  const kinds = [{ id: 'village', label: 'Villages', count: 3 }, { id: 'stronghold', label: 'Strongholds', count: 1, off: true }];
  const row = p.add({ group: 'structures', id: 'recorded', label: 'Known', facet: 'kinds' }, kinds);
  row.setCount(4);
  await p.tick();
  await p.open('Structures', 'Known');
  assert.equal(p.state('Structures', 'Known', 'Strongholds'), 'false');
  assert.equal(row.shows('stronghold'), false);
  assert.equal(p.state('Structures', 'Known'), 'true', 'the layer is not half on for it');
  assert.equal(p.state('Structures'), 'true');
  assert.equal(p.part(['Structures', 'Known'], 'count').textContent, '4');
  assert.match(p.status(), /^All \d+ shown/);
  const reset = p.doc.getElementById('layers-body').querySelector('.panel-status').querySelector('button');
  assert.equal(reset.hidden, true, 'there is nothing to reset');
  const railed = p.panel.querySelector('.panel-rail').children.find((button) => button.title.startsWith('Structures'));
  assert.equal(railed.dataset.state, 'all');
  // Asked for, it is shown; hidden again, it is back at how the page first has it.
  await p.press(['Structures', 'Known', 'Strongholds']);
  assert.equal(row.shows('stronghold'), true);
  assert.equal(reset.hidden, false);
  await p.press(['Structures', 'Known', 'Strongholds']);
  assert.equal(reset.hidden, true);
  assert.equal(p.settings.get('layers')['structures#kinds'], undefined, 'nothing is kept for a choice that is the default');
  // Something of the viewer's own doing does read as filtered, and Reset undoes exactly that.
  await p.press(['Structures', 'Known', 'Villages']);
  assert.equal(p.state('Structures', 'Known'), 'mixed');
  assert.match(p.status(), /^\d+ of \d+ shown/);
  assert.equal(railed.dataset.state, 'some');
  reset.click();
  await p.tick();
  assert.equal(p.state('Structures', 'Known'), 'true');
  assert.equal(row.shows('stronghold'), false);
  assert.match(p.status(), /^All \d+ shown/);
});

test('rows that are off as the page first has them do not read as hidden', async () => {
  const p = await load();
  assert.match(p.status(), /^All \d+ shown/);
  await p.press(['Overlays', 'Slime chunks']);
  assert.match(p.status(), /^All \d+ shown/);
  await p.press(['Markers', 'Beds']);
  assert.match(p.status(), /^\d+ of \d+ shown/);
});

test('an item ticked under a layer that is off brings the layer on as it was chosen, with that item', async () => {
  const p = await load();
  await p.open('Mobs', 'Passive');
  await p.press(['Mobs', 'Passive', 'Cow']);
  await p.press(['Mobs', 'Passive']);
  assert.equal(p.state('Mobs', 'Passive', 'Cat'), 'false', 'under a layer that is off, nothing reads as shown');
  await p.press(['Mobs', 'Passive', 'Cow']);
  assert.equal(p.state('Mobs', 'Passive'), 'true');
  for (const id of ['cat', 'cow', 'wolf']) assert.equal(p.rows['live/passive'].shows(id), true, `${id} is shown`);
  // An item that was hidden and is not the one ticked stays hidden.
  await p.press(['Mobs', 'Passive', 'Wolf']);
  await p.press(['Mobs', 'Passive']);
  await p.press(['Mobs', 'Passive', 'Cat']);
  assert.equal(p.rows['live/passive'].shows('wolf'), false);
  assert.equal(p.rows['live/passive'].shows('cat'), true);
  assert.equal(p.rows['live/passive'].shows('cow'), true);
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

module.exports = { test, load, tick, main };
if (require.main === module) main();
