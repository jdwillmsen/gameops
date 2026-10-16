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
const COUNTERS = {
  state: 'known', since: '2026-10-01T12:00:00Z', kills: { zombie: 12, spider: 3, creeper: 1 }, deaths: { entityAttack: 2, fall: 1 },
  broken: 40, brokenTypes: { stone: 25, dirt: 10 }, placed: 17, placedTypes: { oak_planks: 12 }, joins: 4, leaves: 3, onlineSeconds: 7200,
};
const LIVE = { health: 9, maxHealth: 20, level: 31, dimension: 'overworld', at: new Date(Date.now() - 2000).toISOString() };
const ANSWER = { id: '-9', name: 'Alex', counters: COUNTERS, saved: { state: 'known', asOf: new Date(Date.now() - 600_000).toISOString(), record: SAVED }, trails: { state: 'known', stats: STATS } };

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
  doc.body.append(dialog);
  dialog.append(made.get('player-body'), made.get('player-close'));
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
  // The minute's refresh is run by hand.
  const ticks = [];
  win.setInterval = (fn) => { ticks.push(fn); return ticks.length; };
  win.clearInterval = () => {};
  win.mcmap = {
    icons: { keyOf: (sort, o) => `${sort}/${o.kind || o.colour}`, picture: (key, o = {}) => {
      const c = doc.createElement('canvas');
      c.dataset.picture = key;
      if (o.side) c.dataset.side = String(o.side);
      if (o.glint) c.dataset.glint = '1';
      return c;
    },
    paint() {},
  },
    names: { tidy: (id) => String(id).split('_').map((w) => w[0].toUpperCase() + w.slice(1)).join(' ') },
    label: (d) => ({ overworld: 'Overworld', nether: 'Nether', end: 'The End' }[d] || d),
    go: (d, x, z) => { went.push([d, x, z]); return d !== 'end'; },
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'playerdetails.js'), 'utf8'), win, { filename: 'playerdetails.js' });
  return { win, doc, dialog, made, asked, went, ticks, app: win.mcmap };
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
    'Worn and held', 'Recipes unlocked', 'Warden threatLevel 2']) {
    assert.ok(text.includes(need), `missing ${need} in ${text}`);
  }
  // Nothing absorbed is said as nothing, and the credits as not seen.
  assert.ok(text.includes('Absorption') && text.includes('None'));
  assert.ok(text.includes('Has seen the creditsNo'));
});

test('draws 36 inventory slots as one block, the backpack over the hotbar, and 27 of the ender chest', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const groups = body(t).querySelectorAll('div').filter((d) => d.getAttribute('role') === 'group');
  assert.deepEqual(groups.map((g) => [g.getAttribute('aria-label'), g.children.length]),
    [['Worn', 4], ['Held', 2], ['Inventory, backpack', 27], ['Inventory, hotbar', 9], ['Ender chest', 27]]);
  const [, , backpack, hotbar, ender] = groups;
  // The game's layout: the three rows of the backpack, then the hotbar
  // directly under them, in one block of the page that the ender chest is not.
  assert.equal(backpack.parentNode, hotbar.parentNode);
  assert.equal(backpack.parentNode.childNodes.indexOf(hotbar), backpack.parentNode.childNodes.indexOf(backpack) + 1);
  assert.notEqual(ender.parentNode, hotbar.parentNode);
  assert.ok(hotbar.children[0].getAttribute('aria-label').startsWith('Cobblestone × 64'));
  assert.equal(hotbar.children[1].disabled, true);
  // Slot 20 is the twelfth of the backpack; a chest has a picture of its own.
  assert.ok(backpack.children[11].getAttribute('aria-label').startsWith('Chest × 2'));
  assert.equal(backpack.children[11].querySelectorAll('canvas')[0].dataset.picture, 'container/chest');
  assert.ok(ender.children[26].getAttribute('aria-label').startsWith('Ender Pearl × 16'));
  // An item with a picture is that, over its initials, which stand in
  // while the picture is not there and are what a touch screen reads.
  assert.equal(hotbar.children[0].querySelectorAll('span')[0].textContent, 'CO');
  hotbar.children[0].click();
  assert.ok(body(t).querySelectorAll('p').some((p) => p.textContent.startsWith('Cobblestone × 64')));
});

test('an item is drawn with the picture of its own, asked for at 32 pixels, and its initials under it', async () => {
  const record = {
    ...SAVED,
    inventory: [
      { slot: 0, name: 'diamond_pickaxe', count: 1 }, { slot: 1, name: 'chest', count: 1 }, { slot: 2, name: 'pig_spawn_egg', count: 1 },
      { slot: 3, name: 'light_blue_shulker_box', count: 1 }, { slot: 4, name: 'Not-An-Id', count: 1 }, { slot: 5, name: 'golden_apple', count: 3 },
      { slot: 6, name: 'light_blue_bed', count: 1 }, { slot: 7, name: 'straw_bed', count: 1 }, { slot: 8, name: 'mauve_shulker_box', count: 1 },
    ],
  };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const hotbar = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Inventory, hotbar');
  const picture = (n) => hotbar.children[n].querySelectorAll('canvas')[0];
  assert.equal(picture(0).dataset.picture, 'item/diamond_pickaxe');
  assert.equal(picture(0).dataset.side, '32');
  assert.equal(picture(5).dataset.picture, 'item/golden_apple');
  // What the map already draws for a thing is what it is drawn with here.
  assert.equal(picture(1).dataset.picture, 'container/chest');
  assert.equal(picture(2).dataset.picture, 'mob/pig');
  assert.equal(picture(3).dataset.picture, 'container/shulker');
  // Two-word colours are colours too, and not the item of a bed that is not there.
  assert.equal(picture(6).dataset.picture, 'bed/light_blue');
  // A name that is not a colour is an item like any other, not a bed or box of a colour that is not there.
  assert.equal(picture(7).dataset.picture, 'item/straw_bed');
  assert.equal(picture(8).dataset.picture, 'item/mauve_shulker_box');
  // An id that is not one is asked for nothing.
  assert.equal(picture(4), undefined);
  assert.equal(hotbar.children[0].querySelectorAll('span')[0].textContent, 'DP');
  assert.equal(picture(0).dataset.glint, undefined);
});

test('an enchanted item is marked on its picture and not only on its border, and its enchantments stay in the words', async () => {
  const record = {
    ...SAVED,
    inventory: [{ slot: 0, name: 'diamond_pickaxe', count: 1, enchantments: [{ id: 15, name: 'Efficiency', level: 5 }] }, { slot: 1, name: 'diamond_axe', count: 1 }],
  };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const hotbar = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Inventory, hotbar');
  const [enchanted, plain] = hotbar.children;
  assert.equal(enchanted.querySelectorAll('canvas')[0].dataset.glint, '1');
  assert.ok(enchanted.classList.contains('enchanted'));
  assert.equal(plain.querySelectorAll('canvas')[0].dataset.glint, undefined);
  assert.ok(!plain.classList.contains('enchanted'));
  assert.equal(enchanted.title, 'Diamond Pickaxe · Efficiency V');
  // The head slot of the fixture holds an enchanted helmet too.
  const worn = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Worn');
  assert.equal(worn.children[0].querySelectorAll('button')[0].querySelectorAll('canvas')[0].dataset.glint, '1');
});

test('worn and held items are slots, each under the place it is in, and say the place and the wear', async () => {
  const record = {
    ...SAVED,
    armor: [
      { slot: 0, name: 'iron_helmet', count: 1, damage: 33, maxDamage: 165, enchantments: [{ id: 0, name: 'Protection', level: 4 }] },
      { slot: 2, name: 'diamond_leggings', count: 1 },
    ],
    mainhand: { slot: 0, name: 'diamond_sword', count: 1, customName: 'Sting' },
    offhand: { slot: 1, name: 'shield', count: 1, damage: 10, maxDamage: 336 },
  };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const worn = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Worn');
  const held = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Held');
  const captions = (group) => group.children.map((c) => c.querySelectorAll('span').find((s) => s.classList.contains('place')).textContent);
  assert.deepEqual(captions(worn), ['Head', 'Chest', 'Legs', 'Feet']);
  assert.deepEqual(captions(held), ['Main hand', 'Off hand']);
  const box = (group, n) => group.children[n].querySelectorAll('button')[0];
  assert.equal(box(worn, 0).getAttribute('aria-label'), 'Head: Iron Helmet · Protection IV · 132 of 165 uses left');
  assert.equal(box(worn, 0).querySelectorAll('canvas')[0].dataset.picture, 'item/iron_helmet');
  // The wear is the bar a slot of the grid has.
  assert.equal(box(worn, 0).querySelectorAll('span').filter((s) => s.classList.contains('wear')).length, 1);
  assert.equal(box(worn, 1).getAttribute('aria-label'), 'Chest: Empty');
  assert.equal(box(worn, 1).disabled, true);
  assert.equal(box(worn, 2).getAttribute('aria-label'), 'Legs: Diamond Leggings');
  assert.equal(box(worn, 3).disabled, true);
  assert.equal(box(held, 0).getAttribute('aria-label'), 'Main hand: Sting (Diamond Sword)');
  assert.equal(box(held, 1).getAttribute('aria-label'), 'Off hand: Shield · 326 of 336 uses left');
  assert.equal(box(held, 1).querySelectorAll('canvas')[0].dataset.picture, 'item/shield');
  // Nothing in either hand is an empty slot and not the word for it.
  const bare = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record: { ...SAVED, mainhand: undefined, offhand: undefined, armor: [] } } } });
  bare.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const empty = body(bare).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Held');
  assert.deepEqual(empty.children.map((c) => c.querySelectorAll('button')[0].getAttribute('aria-label')), ['Main hand: Empty', 'Off hand: Empty']);
  // A touch reads one by choosing it, and it is read again from a new answer.
  box(worn, 0).click();
  assert.ok(body(t).querySelectorAll('p').some((p) => p.textContent === 'Iron Helmet · Protection IV · 132 of 165 uses left'));
});

const marksOf = (t) => body(t).querySelectorAll('.marks').map((row) => row.children.map((mark) => mark.querySelectorAll('canvas').map((c) => c.dataset.picture)));

test('health, hunger and absorption are drawn as the game draws them, full, half and empty, and the words stay', async () => {
  const record = { ...SAVED, absorption: { current: 5, max: 16 } };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const [health, hunger, absorption] = marksOf(t);
  // 17 of 20: eight whole hearts, a half and an empty container.
  assert.equal(health.length, 10);
  assert.deepEqual(health[0], ['ui/heart_container', 'ui/heart']);
  assert.deepEqual(health[7], ['ui/heart_container', 'ui/heart']);
  assert.deepEqual(health[8], ['ui/heart_container', 'ui/heart_half']);
  assert.deepEqual(health[9], ['ui/heart_container']);
  // 14 of 20: seven drumsticks and three empty.
  assert.deepEqual(hunger.map((m) => m.length), [2, 2, 2, 2, 2, 2, 2, 1, 1, 1]);
  assert.ok(hunger.slice(0, 7).every((m) => m[1] === 'ui/hunger') && hunger[7][0] === 'ui/hunger_container');
  // 5 absorbed: two whole hearts and a half, in gold, and no empty ones.
  assert.deepEqual(absorption, [['ui/heart_container', 'ui/absorption'], ['ui/heart_container', 'ui/absorption'], ['ui/heart_container', 'ui/absorption_half']]);
  // Each is two of the picture's pixels to one, and is for the eye alone.
  const row = body(t).querySelectorAll('.marks')[0];
  assert.equal(row.getAttribute('aria-hidden'), 'true');
  assert.ok(row.querySelectorAll('canvas').every((c) => c.dataset.side === '18'));
  // The words say the same, and the bar is there for when the marks are not.
  const text = words(t);
  assert.ok(text.includes('8.5 of 10 hearts') && text.includes('14 of 20') && text.includes('2.5 hearts'));
  const gauge = row.parentNode;
  assert.ok(gauge.classList.contains('gauge'));
  assert.equal(gauge.querySelectorAll('.meter').length, 1);
  assert.equal(gauge.querySelectorAll('.meter')[0].getAttribute('role'), 'meter');
});

test('live health is drawn as marks too, and a half point is half a heart', async () => {
  const t = load({ body: { ...ANSWER, live: { ...LIVE, health: 9 } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const [health] = marksOf(t);
  assert.deepEqual(health.map((m) => m[1] || ''), ['ui/heart', 'ui/heart', 'ui/heart', 'ui/heart', 'ui/heart_half', '', '', '', '', '']);
  const barely = load({ body: { ...ANSWER, live: { ...LIVE, health: 0.5 } } });
  barely.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.deepEqual(marksOf(barely)[0][0], ['ui/heart_container', 'ui/heart_half']);
});

test('a gauge too large to be a row of the game’s marks stays a bar, and so does one with nothing to count', async () => {
  const record = { ...SAVED, health: { current: 150, max: 200 }, hunger: { current: 3, max: 0 }, absorption: { current: 0, max: 16 } };
  const t = load({ body: { ...ANSWER, saved: { ...ANSWER.saved, record } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.equal(body(t).querySelectorAll('.marks').length, 0);
  assert.ok(words(t).includes('75 of 100 hearts'));
  // The health is the bar, with the words the marks would have had beside it,
  // and the hunger that has no ceiling is not drawn at all.
  const meters = body(t).querySelectorAll('.meter');
  const health = meters.find((m) => m.getAttribute('aria-label') === 'Health');
  assert.ok(health, 'no bar for the health');
  assert.equal(health.getAttribute('role'), 'meter');
  assert.equal(health.getAttribute('aria-valuetext'), '75 of 100 hearts');
  assert.equal(health.parentNode.querySelectorAll('.marks').length, 0);
  assert.equal(meters.some((m) => m.getAttribute('aria-label') === 'Hunger'), false);
  assert.ok(words(t).includes('None'));
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
  // The name is read from the slot, and is text wherever it is put.
  const held = body(t).querySelectorAll('div').find((d) => d.getAttribute('aria-label') === 'Held');
  const stick = held.children[0].querySelectorAll('button')[0];
  assert.ok(stick.getAttribute('aria-label').includes(hostile) && stick.title.includes(hostile));
  stick.click();
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

test('a refresh with nothing new leaves the panel as it is, and one with news keeps focus and the item being read', async () => {
  let n = 0;
  const t = load(() => ({ body: n === 0 ? ANSWER : { ...ANSWER, trails: { ...ANSWER.trails, stats: { ...STATS, blocks: 99 + n } } } }));
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const slotAt = () => body(t).querySelectorAll('button').find((b) => b.dataset.pos === 'Inventory|20');
  slotAt().focus();
  slotAt().click();
  assert.equal(t.doc.activeElement, slotAt());
  const before = slotAt();
  // The same answer: nothing is rebuilt.
  await t.ticks[0]();
  assert.equal(slotAt(), before);
  assert.equal(t.doc.activeElement, before);
  // A new one: drawn again, with focus on the same slot and its details read.
  n = 1;
  await t.ticks[0]();
  assert.notEqual(slotAt(), before);
  assert.equal(t.doc.activeElement, slotAt());
  assert.ok(body(t).querySelectorAll('p').some((p) => p.textContent.startsWith('Chest × 2')));
  assert.ok(words(t).includes('100 blocks'));
  // Focus on something that has gone stays in the panel, so Escape works.
  const gone = load(() => ({ body: n === 0 ? ANSWER : { ...ANSWER, saved: { ...ANSWER.saved, record: { ...SAVED, inventory: [] } } } }));
  n = 0;
  gone.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  gone.doc.getElementById('player-body').querySelectorAll('button').find((b) => b.dataset.pos === 'Inventory|20').focus();
  n = 1;
  await gone.ticks[0]();
  assert.equal(gone.doc.activeElement, gone.made.get('player-close'));
});

test('shows what was counted under combat, building and sessions, since when', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const text = words(t);
  for (const need of ['Combat', 'Building', 'Sessions', 'Counting since 1 Oct 2026 (UTC)', '16mobs killed', '3deaths', 'Zombie12', 'Spider3', 'Entity attack2', 'Fall1',
    '40blocks broken', '17blocks placed', 'Stone25', 'Oak Planks12', '4joins', '2 honline']) {
    assert.ok(text.includes(need), `missing ${need} in ${text}`);
  }
  // The biggest first.
  assert.ok(text.indexOf('Zombie12') < text.indexOf('Spider3'));
});

test('long lists are cut and say how many were left off', async () => {
  const many = Object.fromEntries(Array.from({ length: 14 }, (_, i) => [`mob_${String.fromCharCode(97 + i)}`, 20 - i]));
  const t = load({ body: { ...ANSWER, counters: { ...COUNTERS, kills: many } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(words(t).includes('and 4 more'));
  assert.ok(!words(t).includes('Mob N'));
});

test('says so when nothing is counted yet, and says nothing when the map keeps no counters', async () => {
  const none = load({ body: { ...ANSWER, counters: { state: 'none' } } });
  none.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(words(none).includes('Nothing has been counted for this player yet'));
  assert.ok(!words(none).includes('Combat'));
  const off = load({ body: { ...ANSWER, counters: { state: 'off' } } });
  off.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(!words(off).includes('counted') && !words(off).includes('Combat'));
  const old = load({ body: { ...ANSWER, counters: undefined } });
  old.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(words(old).includes('From the world save') && !words(old).includes('Combat'));
});

test('live health and level replace the saved ones, and the panel says which is which', async () => {
  const t = load({ body: { ...ANSWER, live: LIVE } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const text = words(t);
  assert.ok(text.includes('Live: the health and level below are from the game right now'));
  assert.ok(text.includes('4.5 of 10 hearts'), 'live health');
  assert.ok(!text.includes('8.5 of 10 hearts'), 'saved health');
  assert.ok(text.includes('Level 31') && !text.includes('Level 12'));
  // The rest of the vitals are still the save's.
  assert.ok(text.includes('14 of 20'));
});

test('without a live sample the vitals are the save’s and are not labelled live', async () => {
  const t = load({ body: ANSWER });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.ok(words(t).includes('8.5 of 10 hearts') && !words(t).includes('Live:'));
});

test('an offline player with no saved record has no empty live section', async () => {
  for (const live of [undefined, {}, { dimension: 'overworld' }]) {
    const t = load({ body: { id: '5', saved: { state: 'none', asOf: new Date().toISOString() }, trails: { state: 'off' }, counters: { state: 'off' }, live } });
    t.app.playerDetails.open({ id: '5', name: 'Alex' });
    await tick();
    assert.ok(!words(t).includes('Right now') && !words(t).includes('Absorption'), words(t));
    assert.ok(words(t).includes('No saved record'));
  }
});

test('an online player’s answer that differs only in its age is not drawn again, but the age moves', async () => {
  let at = Date.now() - 1000;
  const t = load(() => ({ body: { ...ANSWER, live: { ...LIVE, at: new Date(at).toISOString() } } }));
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const node = body(t).children[0];
  assert.ok(words(t).includes('(a moment ago)'));
  at = Date.now() - 300_000;
  await t.ticks[1]();
  await tick();
  assert.equal(body(t).children[0], node, 'redrawn for a new age');
  assert.ok(words(t).includes('(5 min ago)'), words(t));
});

test('a player with no saved record yet still shows the live vitals', async () => {
  const t = load({ body: { id: '5', saved: { state: 'none', asOf: new Date().toISOString() }, trails: { state: 'off' }, counters: { state: 'off' }, live: LIVE } });
  t.app.playerDetails.open({ id: '5', name: 'Alex' });
  await tick();
  assert.ok(words(t).includes('Right now') && words(t).includes('4.5 of 10 hearts') && words(t).includes('Level 31'));
});

test('keeps asking quickly while the player is online, through the same unchanged-skip drawing', async () => {
  const t = load({ body: { ...ANSWER, live: LIVE } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  // The first timer is the slow one every player has; the second is quick.
  assert.equal(t.ticks.length, 2);
  const before = t.asked.length;
  const node = body(t).children[0];
  await t.ticks[1]();
  assert.equal(t.asked.length, before + 1, 'online: asked again');
  assert.equal(body(t).children[0], node, 'an unchanged answer was drawn again');

  const quiet = load({ body: ANSWER });
  quiet.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  const asked = quiet.asked.length;
  await quiet.ticks[1]();
  assert.equal(quiet.asked.length, asked, 'offline: asked on the quick timer');
  await quiet.ticks[0]();
  assert.equal(quiet.asked.length, asked + 1, 'offline: not asked on the slow timer');
});

test('counter names are put in as text, never as markup', async () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const t = load({ body: { ...ANSWER, counters: { ...COUNTERS, kills: { [hostile]: 2 }, deaths: { [hostile]: 1 } } } });
  t.app.playerDetails.open({ id: '-9', name: 'Alex' });
  await tick();
  assert.equal(body(t).querySelectorAll('img').length, 0);
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
