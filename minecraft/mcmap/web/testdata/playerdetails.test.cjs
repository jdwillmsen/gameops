'use strict';

// The player details panel, run: the script is given a page of the kind
// dom.cjs makes, the few parts of the map it leans on, and a server that
// answers as each test says. Every name, number and place is made up. Run
// by `go test ./web/` where there is a node, or by hand:
// `node web/testdata/playerdetails.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const SAVED = {
  health: { current: 17, max: 20 }, hunger: { current: 14, max: 20 }, saturation: { current: 3.5, max: 20 },
  absorption: { current: 0, max: 16 }, air: 300, level: 12, progress: 0.25, gameMode: 'survival', dimension: 'overworld',
  armor: [{ slot: 0, name: 'iron_helmet', count: 1, damage: 33, maxDamage: 165, enchantments: [{ id: 0, name: 'Protection', level: 4 }, { id: 99, level: 1 }] }],
  mainhand: { slot: 0, name: 'diamond_sword', count: 1, customName: 'Sting' },
  inventory: [{ slot: 0, name: 'cobblestone', count: 64 }, { slot: 20, name: 'chest', count: 2 }],
  enderChest: [{ slot: 26, name: 'ender_pearl', count: 16 }],
  death: { x: 10, y: 64, z: -30, dimension: 'nether' },
  recipesUnlocked: 3, seenCredits: false, wardenThreat: 2,
};
const STATS = { name: 'Alex', windowSeconds: 86_400, gapSeconds: 120, onlineSeconds: 5400, blocks: 1234, days: [{ date: '2026-10-14', onlineSeconds: 5400, blocks: 1234 }], dimensions: ['overworld', 'nether'], lastSeen: Math.floor(Date.now() / 1000) - 30, lastDimension: 'nether' };
const ANSWER = { id: '-9', name: 'Alex', saved: { state: 'known', asOf: new Date(Date.now() - 600_000).toISOString(), record: SAVED }, trails: { state: 'known', stats: STATS } };

function load(answer) {
  const p = page(new Map(), { files: [] });
  const { win, doc } = p;
  const made = new Map();
  for (const id of ['player', 'player-title', 'player-sub', 'player-body', 'player-said', 'player-close']) {
    made.set(id, doc.createElement(id === 'player' ? 'dialog' : id === 'player-close' ? 'button' : 'div'));
  }
  const dialog = made.get('player');
  dialog.show = () => { dialog.open = true; };
  dialog.close = () => { dialog.open = false; dialog.dispatchEvent({ type: 'close' }); };
  const byId = doc.getElementById;
  doc.getElementById = (id) => made.get(id) || byId(id);
  const asked = [];
  const went = [];
  win.fetch = async (url) => {
    asked.push(url);
    const out = typeof answer === 'function' ? answer(url) : answer;
    return { ok: out.status === undefined, status: out.status || 200, json: async () => out.body };
  };
  win.AbortController = AbortController;
  win.setInterval = setInterval;
  win.clearInterval = clearInterval;
  win.mcmap = {
    icons: { keyOf: (sort, o) => `${sort}/${o.kind || o.colour}`, picture: (key) => { const c = doc.createElement('canvas'); c.dataset.picture = key; return c; }, paint() {} },
    names: { tidy: (id) => String(id).split('_').map((w) => w[0].toUpperCase() + w.slice(1)).join(' ') },
    label: (d) => ({ overworld: 'Overworld', nether: 'Nether', end: 'The End' }[d] || d),
    go: (d, x, z) => { went.push([d, x, z]); return d !== 'end'; },
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'playerdetails.js'), 'utf8'), win, { filename: 'playerdetails.js' });
  return { win, doc, dialog, made, asked, went, app: win.mcmap };
}

const body = (t) => t.made.get('player-body');
const words = (t) => body(t).textContent;
const press = (node, label) => node.querySelectorAll('button').find((b) => b.textContent === label);

test('opens beside the map without taking the page, and asks for the player by id and name', async () => {
  const t = load({ body: ANSWER });
  assert.equal(t.app.playerDetails.open({ id: '-9', name: 'Alex & Co' }), true);
  await tick();
  assert.equal(t.dialog.open, true);
  assert.equal(t.dialog.showModal, undefined);
  assert.deepEqual(t.asked, ['api/players/-9?name=Alex%20%26%20Co']);
  assert.equal(t.made.get('player-title').textContent, 'Alex & Co');
  assert.ok(t.doc.body.classList.contains('player-shown'));
});

test('refuses an id that is not a number', async () => {
  const t = load({ body: ANSWER });
  for (const id of ['', 'abc', '1.5', '../x', 7, null]) assert.equal(t.app.playerDetails.open({ id }), false);
  assert.equal(t.asked.length, 0);
  assert.equal(t.dialog.open, undefined);
});

test('says what the save holds, with when it is from', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const text = words(t);
  for (const need of ['As of the snapshot 10 min ago', '8.5 of 10 hearts', '14 of 20', 'Full', 'Level 12, 25% to the next', 'Survival',
    'Iron Helmet · Protection IV, Enchantment 99 I · 132 of 165 uses left', 'Sting (Diamond Sword)', 'Recipes unlocked', 'Warden threatLevel 2']) {
    assert.ok(text.includes(need), `missing ${need} in ${text}`);
  }
  // Nothing absorbed is said as nothing, and the credits as not seen.
  assert.ok(text.includes('Absorption') && text.includes('None'));
  assert.ok(text.includes('Has seen the creditsNo'));
});

test('draws 36 inventory slots with the hotbar apart, and 27 of the ender chest', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const groups = body(t).querySelectorAll('div').filter((d) => d.getAttribute('role') === 'group');
  assert.deepEqual(groups.map((g) => [g.getAttribute('aria-label'), g.children.length]), [['Inventory, hotbar', 9], ['Inventory, backpack', 27], ['Ender chest', 27]]);
  const [hotbar, backpack, ender] = groups;
  assert.ok(hotbar.children[0].getAttribute('aria-label').startsWith('Cobblestone × 64'));
  assert.equal(hotbar.children[1].disabled, true);
  // Slot 20 is the twelfth of the backpack; a chest has a picture of its own.
  assert.ok(backpack.children[11].getAttribute('aria-label').startsWith('Chest × 2'));
  assert.equal(backpack.children[11].querySelectorAll('canvas')[0].dataset.picture, 'container/chest');
  assert.ok(ender.children[26].getAttribute('aria-label').startsWith('Ender Pearl × 16'));
  // An item with no picture is its initials, and a touch can read it.
  assert.equal(hotbar.children[0].querySelectorAll('span')[0].textContent, 'CO');
  hotbar.children[0].click();
  assert.ok(body(t).querySelectorAll('p').some((p) => p.textContent.startsWith('Cobblestone × 64')));
});

test('goes to where the player died, in the dimension it was in', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(words(t).includes('X 10, Y 64, Z -30 · Nether'));
  press(body(t), 'Go to').click();
  assert.deepEqual(t.went, [['nether', 10.5, -29.5]]);
});

test('with no saved record yet it says so and still shows the trails', async () => {
  const t = load({ body: { id: '5', saved: { state: 'none', asOf: new Date().toISOString() }, trails: { state: 'known', stats: STATS } } });
  t.app.playerDetails.open({ id: '5', name: 'Alex' });
  await tick();
  const text = words(t);
  assert.ok(text.includes('No saved record for this player yet'));
  assert.ok(!text.includes('Inventory'));
  assert.ok(text.includes('1,234 blocks') && text.includes('1 h 30 min') && text.includes('Overworld, Nether'));
});

test('says plainly when the map has no snapshot or no trails', async () => {
  const t = load({ body: { id: '5', saved: { state: 'pending' }, trails: { state: 'off' } } });
  t.app.playerDetails.open({ id: '5' });
  await tick();
  assert.ok(words(t).includes('has not read a snapshot'));
  assert.ok(words(t).includes('not keeping trails'));
});

test('a server without the route, or one that fails, is told in words', async () => {
  const t = load({ status: 404 });
  t.app.playerDetails.open({ id: '5' });
  await tick();
  assert.ok(words(t).includes('does not keep player details'));
  const u = load({ status: 500 });
  u.app.playerDetails.open({ id: '5' });
  await tick();
  assert.ok(words(u).includes('could not be fetched'));
});

test('puts player text in as text, never as markup', async () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const record = { ...SAVED, mainhand: { slot: 0, name: 'stick', count: 1, customName: hostile } };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: hostile });
  await tick();
  assert.equal(t.made.get('player-title').textContent, hostile);
  assert.ok(words(t).includes(hostile));
  assert.equal(body(t).querySelectorAll('img').length, 0);
});

test('shuts with its button and with Escape, and stops asking', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  t.made.get('player-close').click();
  assert.equal(t.dialog.open, false);
  assert.ok(!t.doc.body.classList.contains('player-shown'));
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  t.dialog.dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(t.dialog.open, false);
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok ${name}`);
    } catch (e) {
      failed += 1;
      console.log(`not ok ${name}\n${e.stack}`);
    }
  }
  process.exit(failed === 0 ? 0 : 1);
})();
