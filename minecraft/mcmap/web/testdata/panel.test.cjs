'use strict';

// What the layer panel does, checked by doing it: the panel's own script
// and the script that keeps the record are run in a page of the kind
// dom.cjs makes, rows are registered as the layers' scripts register them,
// and the lines are pressed as a viewer would press them. Run by
// `go test ./web/` where there is a node to run it with, or by hand:
// `node web/testdata/panel.test.cjs`.
const assert = require('node:assert/strict');
const { page } = require('./dom.cjs');

// What the page hands back is made in the page, so it is compared as written out.
const same = (got, want) => assert.equal(JSON.stringify(got), JSON.stringify(want));
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

const many = (n) => Array.from({ length: n }, (_, i) => ({ id: `w${i}`, label: `Waypoint ${String(i).padStart(3, '0')}` }));

test('"only this, and that one too" over hundreds of items is kept as the two, and is the two after a reload', async () => {
  const waypoints = many(260);
  const p = await load(new Map(), { waypoints });
  await p.open('Markers', 'Waypoints');
  await p.press(['Markers', 'Waypoints', 'Waypoint 003'], 'only');
  await p.press(['Markers', 'Waypoints', 'Waypoint 007']);
  const kept = p.settings.get('layers')['markers#waypoints'];
  same(kept, { only: null, hidden: [], mode: 'just', just: ['w3', 'w7'] });
  const q = await load(p.storage, { waypoints });
  const shown = waypoints.filter((w) => q.rows['markers/waypoints'].shows(w.id)).map((w) => w.id);
  assert.deepEqual(shown, ['w3', 'w7']);
  // A third is added to the list, and one of them taken off it.
  await q.open('Markers', 'Waypoints');
  await q.press(['Markers', 'Waypoints', 'Waypoint 001']);
  await q.press(['Markers', 'Waypoints', 'Waypoint 003']);
  same(q.settings.get('layers')['markers#waypoints'].just.sort(), ['w1', 'w7']);
  // Showing them all again is "everything", and nothing is kept for it.
  await q.menu(['Markers', 'Waypoints'], 'Show all in group');
  assert.equal(q.settings.get('layers')['markers#waypoints'], undefined);
});

test('more hides than are kept turn into the shorter list of what is shown, and none is dropped', async () => {
  const waypoints = many(260);
  const p = await load(new Map(), { waypoints });
  const handle = p.layers.facet('markers', 'waypoints');
  // Hidden one at a time, as their checkboxes would: 210 of the 260.
  await p.open('Markers', 'Waypoints');
  await p.part(['Markers', 'Waypoints'], 'more').click();
  for (let i = 0; i < 210; i++) {
    // The list shows forty until asked for the rest.
    const rest = p.li('Markers', 'Waypoints').querySelectorAll('.rest')[0];
    if (rest) { rest.querySelector('button').click(); await p.tick(); }
    await p.press(['Markers', 'Waypoints', `Waypoint ${String(i).padStart(3, '0')}`]);
  }
  const kept = p.settings.get('layers')['markers#waypoints'];
  assert.equal(kept.mode, 'just');
  assert.equal(kept.just.length, 50);
  assert.equal(handle.shows('w209'), false);
  assert.equal(handle.shows('w210'), true);
  const q = await load(p.storage, { waypoints });
  assert.equal(waypoints.filter((w) => q.rows['markers/waypoints'].shows(w.id)).length, 50);
  assert.equal(q.rows['markers/waypoints'].shows('w209'), false);
  assert.equal(q.li('Markers', 'Waypoints').querySelectorAll('.note')[0].textContent, '');
});

test('where neither list is short enough to keep, the panel says what will not be kept', async () => {
  const waypoints = many(500);
  const p = await load(new Map(), { waypoints });
  await p.open('Markers', 'Waypoints');
  p.li('Markers', 'Waypoints').querySelectorAll('.rest')[0].querySelector('button').click();
  await p.tick();
  for (let i = 0; i < 250; i++) await p.press(['Markers', 'Waypoints', `Waypoint ${String(i).padStart(3, '0')}`]);
  const note = p.li('Markers', 'Waypoints').children.find((child) => child.classList.contains('note')).textContent;
  assert.match(note, /^More of these are chosen one by one than are kept: the first 200/);
  // On this page all 250 are hidden; what is kept is the first 200, the same every time.
  assert.equal(p.rows['markers/waypoints'].shows('w249'), false);
  const kept = p.settings.get('layers')['markers#waypoints'];
  assert.equal(kept.hidden.length, 200);
  assert.equal(kept.hidden[0], 'w0');
  assert.equal(kept.hidden[199], 'w199');
});

test('an id that has been in none of its lists for a month is dropped, and one that comes back is not', async () => {
  const p = await load();
  await p.open('Markers', 'Waypoints');
  await p.press(['Markers', 'Waypoints', 'Home']);
  // The waypoint called Mine is hidden too, and then deleted in the game.
  await p.press(['Markers', 'Waypoints', 'Mine']);
  const only = [{ id: 'home', label: 'Home' }];
  const q = await load(p.storage, { waypoints: only });
  await q.tick(); await new Promise((done) => setTimeout(done, 30));
  let kept = q.settings.get('layers')['markers#waypoints'];
  same(kept.hidden.sort(), ['home', 'mine']);
  same(kept.missing, ['mine']);
  // Ten days on it is still kept, and if it is back by then it is off the note.
  const back = await load(new Map(q.storage), { days: 10 });
  await new Promise((done) => setTimeout(done, 30));
  assert.equal(back.settings.get('layers')['markers#waypoints'].missing, undefined);
  assert.equal(back.rows['markers/waypoints'].shows('mine'), false);
  // Still gone a month after it was first missed: dropped, and the other kept.
  const r = await load(q.storage, { waypoints: only, days: 31 });
  await new Promise((done) => setTimeout(done, 30));
  kept = r.settings.get('layers')['markers#waypoints'];
  same(kept.hidden, ['home']);
  assert.equal(kept.missing, undefined);
});

// The structures' kinds are listed by dimension: the script sets the list
// again with the kinds the dimension on screen can hold, over one choice.
const OVERWORLD_KINDS = [{ id: 'village', label: 'Villages', count: 3 }, { id: 'monument', label: 'Ocean Monuments', count: 2 },
  { id: 'ruined_portal', label: 'Ruined Portals', count: 9 }, { id: 'stronghold', label: 'Strongholds', count: 1, off: true }];
const NETHER_KINDS = [{ id: 'fortress', label: 'Nether Fortresses', count: 4 }, { id: 'ruined_portal', label: 'Ruined Portals', count: 2 }];
const END_KINDS = [{ id: 'end_city', label: 'End Cities', count: 5, off: true }, { id: 'end_gateway', label: 'End Gateways', count: 1 }];
const EVERY_KIND = [...OVERWORLD_KINDS, ...NETHER_KINDS, ...END_KINDS].map((kind) => kind.id);
const kindLines = (p) => {
  const kids = p.li('Structures').children.find((child) => child.classList.contains('kids'));
  return kids.children.filter((child) => child.classList.contains('node') && child.querySelector('.count')).map((child) => child.querySelector('.name').textContent);
};

test('a dimension lists only the kinds it can hold, with its own counts, and a kind in two is in both', async () => {
  const p = await load();
  p.add({ group: 'structures', id: 'recorded', label: 'Known', heading: 'How sure' });
  const kinds = p.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, OVERWORLD_KINDS);
  kinds.setElsewhere(EVERY_KIND);
  await p.tick();
  for (const name of ['Villages', 'Ocean Monuments', 'Ruined Portals', 'Strongholds']) assert.ok(p.li('Structures', name), name);
  assert.equal(p.part(['Structures', 'Ruined Portals'], 'count').textContent, '9');
  assert.throws(() => p.li('Structures', 'Nether Fortresses'), /there is no line/);
  assert.throws(() => p.li('Structures', 'End Cities'), /there is no line/);
  // The Nether: the fortress, and the portals with the count they have there.
  kinds.setItems(NETHER_KINDS);
  await p.tick();
  assert.equal(p.part(['Structures', 'Ruined Portals'], 'count').textContent, '2');
  assert.ok(p.li('Structures', 'Nether Fortresses'));
  for (const name of ['Villages', 'Ocean Monuments', 'Strongholds', 'End Cities']) assert.throws(() => p.li('Structures', name), /there is no line/, name);
  // The End: its own two, and none of the others'.
  kinds.setItems(END_KINDS);
  await p.tick();
  assert.equal(p.part(['Structures', 'End Gateways'], 'count').textContent, '1');
  for (const name of ['Villages', 'Ruined Portals', 'Nether Fortresses']) assert.throws(() => p.li('Structures', name), /there is no line/, name);
});

test('a kind keeps its choice across dimensions, and one that is not listed here is not counted as hidden', async () => {
  const p = await load();
  p.add({ group: 'structures', id: 'recorded', label: 'Known', heading: 'How sure' });
  const kinds = p.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, OVERWORLD_KINDS);
  kinds.setElsewhere(EVERY_KIND);
  await p.tick();
  assert.match(p.status(), /^All \d+ shown/);
  // Hidden in the overworld: villages, and the portals, which the Nether has too.
  await p.press(['Structures', 'Villages']);
  await p.press(['Structures', 'Ruined Portals']);
  assert.equal(kinds.shows('village'), false);
  assert.match(p.status(), /^\d+ of \d+ shown/);
  const [, on, all] = /^(\d+) of (\d+) shown/.exec(p.status()).map(Number);
  assert.equal(all - on, 2);
  // In the End neither is a line, and nothing reads as hidden there.
  kinds.setItems(END_KINDS);
  await p.tick();
  assert.match(p.status(), /^All \d+ shown/, 'kinds of another dimension are not hidden here');
  assert.equal(p.state('Structures'), 'true');
  assert.equal(kinds.shows('end_city'), false, 'off until asked for');
  await p.press(['Structures', 'End Cities']);
  assert.equal(kinds.shows('end_city'), true);
  // The Nether has the portals, still hidden, and its fortress as it ever was.
  kinds.setItems(NETHER_KINDS);
  await p.tick();
  assert.equal(p.state('Structures', 'Ruined Portals'), 'false');
  assert.equal(p.state('Structures', 'Nether Fortresses'), 'true');
  assert.match(p.status(), /^\d+ of \d+ shown/);
  // And back in the overworld, and after a reload, each is as it was left.
  kinds.setItems(OVERWORLD_KINDS);
  await p.tick();
  assert.equal(p.state('Structures', 'Villages'), 'false');
  assert.equal(p.state('Structures', 'Ocean Monuments'), 'true');
  const q = await load(p.storage);
  const again = q.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, END_KINDS);
  await q.tick();
  assert.equal(again.shows('end_city'), true);
  assert.equal(again.shows('village'), false);
  // Reset puts every kind back, the ones not on screen with the rest.
  q.doc.getElementById('layers-body').querySelector('.panel-status').querySelector('button').click();
  await q.tick();
  assert.equal(again.shows('village'), true);
  assert.equal(again.shows('end_city'), false);
});

test('a kind of another dimension is not dropped as gone, though it is in no list for a month', async () => {
  const p = await load();
  const kinds = p.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, END_KINDS);
  await p.tick();
  await p.press(['Structures', 'End Gateways']);
  // A month in the overworld, where a gateway is never listed.
  const stay = async (storage, days, elsewhere) => {
    const q = await load(storage, { days });
    const row = q.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, OVERWORLD_KINDS);
    if (elsewhere) row.setElsewhere(EVERY_KIND);
    await q.tick(); await new Promise((done) => setTimeout(done, 30));
    return q;
  };
  let q = await stay(new Map(p.storage), 0, true);
  q = await stay(q.storage, 31, true);
  same(q.settings.get('layers')['structures#kinds'].hidden, ['end_gateway']);
  assert.equal(q.settings.get('layers')['structures#kinds'].missing, undefined);
  // Said of nowhere, it is an id that has gone, and goes.
  q = await stay(new Map(p.storage), 0, false);
  q = await stay(q.storage, 31, false);
  assert.equal(q.settings.get('layers')['structures#kinds'], undefined);
});

test('a saved view naming kinds this dimension has none of shows no line for them and hides nothing of it', async () => {
  const p = await load();
  p.add({ group: 'structures', id: 'recorded', label: 'Known', heading: 'How sure' });
  const kinds = p.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, OVERWORLD_KINDS);
  kinds.setElsewhere(EVERY_KIND);
  await p.tick();
  // As a saved view puts it: the End's cities shown, its gateways and a kind no server has hidden.
  p.settings.set('layers', { ...p.settings.get('layers'), 'structures#kinds': { only: null, hidden: ['end_gateway', 'kind_of_a_later_version'], shown: ['end_city'] } });
  same(p.layers.adopt(['structures/recorded']), []);
  await p.tick();
  same(kindLines(p).filter((name) => name !== 'Known').sort(), ['Ocean Monuments', 'Ruined Portals', 'Strongholds', 'Villages']);
  assert.match(p.status(), /^All \d+ shown/);
  assert.equal(p.state('Structures'), 'true');
  assert.equal(kinds.shows('village'), true);
  // And in the End the view is what it says.
  kinds.setItems(END_KINDS);
  await p.tick();
  assert.equal(p.state('Structures', 'End Cities'), 'true');
  assert.equal(p.state('Structures', 'End Gateways'), 'false');
});

test('the list always has one stop for the Tab key, on a line that is showing', async () => {
  const p = await load();
  const showing = (check) => check.offsetParent !== null;
  assert.equal(p.stops().length, 1);
  // Narrowed to what is under another section than the one the stop was on.
  await p.search('bed');
  assert.equal(p.stops().length, 1, 'one stop while the search hides the first section');
  assert.ok(showing(p.stops()[0]), 'and it is on a line that is showing');
  await p.search('nothing is called this');
  assert.equal(p.tree.querySelectorAll('.check').filter(showing).length, 0);
  await p.search('');
  assert.equal(p.stops().length, 1);
  assert.ok(showing(p.stops()[0]));
  // A group put away takes the stop off the item it was on.
  await p.open('Mobs', 'Passive');
  p.part(['Mobs', 'Passive', 'Cow'], 'check').focus();
  await p.tick();
  assert.equal(p.stops()[0], p.part(['Mobs', 'Passive', 'Cow'], 'check'));
  await p.press(['Mobs', 'Passive'], 'twist');
  assert.equal(p.stops().length, 1);
  assert.ok(showing(p.stops()[0]));
  // A line that goes while it has the focus hands both to the line it was under.
  await p.open('Mobs', 'Passive');
  p.part(['Mobs', 'Passive', 'Cow'], 'check').focus();
  p.rows['live/passive'].setItems([{ id: 'cat', label: 'Cat' }]);
  await p.tick();
  assert.equal(p.doc.activeElement, p.part(['Mobs', 'Passive'], 'check'));
  assert.equal(p.stops().length, 1);
  assert.equal(p.stops()[0], p.part(['Mobs', 'Passive'], 'check'));
  // Never on a line that cannot be pressed while another can.
  assert.notEqual(p.stops()[0].getAttribute('aria-disabled'), 'true');
});

test('a list with no switch of its own stands under its section, beside switches that are about something else', async () => {
  const p = await load();
  const kinds = p.add({ group: 'structures', id: 'kinds', label: 'Kinds', bare: true, facet: 'kinds' }, [
    { id: 'village', label: 'Villages', count: 3 }, { id: 'monument', label: 'Monuments', count: 2 }, { id: 'stronghold', label: 'Strongholds', count: 1, off: true },
  ]);
  p.add({ group: 'structures', id: 'recorded', label: 'Known', heading: 'How sure', order: 10 });
  p.add({ group: 'structures', id: 'predicted', label: 'Predicted', heading: 'How sure', order: 20 });
  await p.tick();
  const lines = p.li('Structures').children.find((child) => child.classList.contains('kids')).children.map((child) => child.textContent.replace(/Only$/, ''));
  assert.deepEqual(lines.map((line) => line.replace(/\d+$/, '')), ['Villages', 'Monuments', 'Strongholds', 'How sure', 'Known', 'Predicted']);
  assert.equal(p.layers.states()['structures/kinds'], undefined, 'the list is not a switch, and is not kept as one');
  assert.equal(p.state('Structures'), 'true');
  // Only on a kind is that kind and no other, whatever the switches under it say.
  await p.press(['Structures', 'Villages'], 'only');
  assert.equal(kinds.shows('village'), true);
  assert.equal(kinds.shows('monument'), false);
  assert.equal(p.state('Structures'), 'mixed');
  assert.equal(p.state('Structures', 'Known'), 'true');
  assert.equal(p.entries('Structures', 'Villages')[0], 'Back to before');
  assert.ok(!p.entries('Structures', 'Villages').includes('Hide all in group'));
  await p.press(['Structures', 'Villages'], 'only');
  assert.equal(kinds.shows('monument'), true);
  assert.equal(kinds.shows('stronghold'), false);
  assert.equal(p.state('Structures'), 'true');
  // The section's own checkbox switches the certainties and leaves the kinds as chosen.
  await p.press(['Structures', 'Monuments']);
  await p.press(['Structures']);
  assert.deepEqual(p.on().filter((key) => key.startsWith('structures/')), []);
  await p.press(['Structures']);
  assert.deepEqual(p.on().filter((key) => key.startsWith('structures/')), ['structures/predicted', 'structures/recorded']);
  assert.equal(kinds.shows('monument'), false);
});

test('a record of the version before is brought up, and the script from before then leaves it alone', async () => {
  const storage = new Map([['mcmap.settings', JSON.stringify({ v: 1, layers: { 'structures/village': false, 'markers/beds': false }, biome: { only: 'desert' }, look: { density: 'compact' } })]]);
  const p = await load(storage);
  const kept = JSON.parse(storage.get('mcmap.settings'));
  assert.equal(kept.v, 2);
  same(kept.layers['structures#kinds'], { only: null, hidden: ['village'] });
  same(kept.layers['biomes#items'], { only: 'desert', hidden: [] });
  assert.equal(p.state('Markers', 'Beds'), 'false');
  assert.equal(p.settings.get('look').density, 'compact');
  // A file of the version before is taken, and one of a version after is not.
  const file = { v: 1, layers: { 'structures/monument': false }, panel: { folded: [] }, live: { paused: false, interval: 1 }, trails: { seconds: 3600 }, shortcuts: { on: true }, grid: { on: false }, biome: { only: null }, look: {}, views: { list: [], order: [], start: null, hidden: [], active: null } };
  const read = p.settings.check.record(file);
  assert.ok(read, 'a file of version 1 is read');
  assert.equal(read.v, 2);
  same(read.layers['structures#kinds'], { only: null, hidden: ['monument'] });
  assert.equal(p.settings.check.record({ ...file, v: 3 }), null);
  // What the script from before does on meeting a later record is its own
  // and is checked in a browser; that this one does it too is checked here.
  storage.set('mcmap.settings', JSON.stringify({ ...kept, v: 3 }));
  const later = await load(storage);
  assert.equal(later.settings.kept(), 'newer');
  await later.press(['Markers', 'Beds']);
  assert.equal(JSON.parse(storage.get('mcmap.settings')).v, 3, 'a later record is not written over');
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
