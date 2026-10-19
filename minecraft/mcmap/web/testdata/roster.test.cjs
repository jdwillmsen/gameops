'use strict';

// The list of players, run: the script is given a page of the kind dom.cjs
// makes and a server that answers as each test says. Every name is made up.
// Run by `go test ./web/` where there is a node, or by hand:
// `node web/testdata/roster.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const hoursAgo = (h) => new Date(Date.now() - h * 3_600_000).toISOString();
const PLAYERS = [
  { id: '3', name: 'Nowonline', earlier: ['Oldname'], dimension: 'nether', lastSeen: hoursAgo(0), tags: ['online', 'operator'] },
  { id: '-1', name: 'Away <b>x</b>', lastSeen: hoursAgo(72), tags: [] },
  { id: '2', name: 'Me', lastSeen: hoursAgo(1), tags: ['you', 'made-up'] },
];

function load(answer, { compact = false, viewed = true } = {}) {
  const p = page(new Map(), { files: [], compact });
  const { win, doc } = p;
  const made = new Map();
  for (const [id, tag] of [['roster-open', 'button'], ['roster', 'dialog'], ['roster-close', 'button'], ['roster-filter', 'input'], ['roster-list', 'ul'], ['roster-said', 'p']]) {
    const node = doc.createElement(tag);
    node.id = id;
    made.set(id, node);
  }
  made.get('roster-open').hidden = true;
  const dialog = made.get('roster');
  dialog.show = () => { dialog.open = true; };
  dialog.close = () => { dialog.open = false; dialog.dispatchEvent({ type: 'close' }); };
  dialog.append(made.get('roster-close'), made.get('roster-filter'), made.get('roster-said'), made.get('roster-list'));
  doc.body.append(made.get('roster-open'), dialog);
  const byId = doc.getElementById;
  doc.getElementById = (id) => made.get(id) || byId(id);
  const asked = [];
  win.fetch = async (url) => {
    asked.push(url);
    const out = typeof answer === 'function' ? answer(url) : answer;
    return { ok: out.status === undefined, status: out.status || 200, json: async () => out.body };
  };
  win.AbortController = AbortController;
  const ticks = [];
  win.setInterval = (fn) => { ticks.push(fn); return ticks.length; };
  win.clearInterval = () => {};
  const opened = [];
  win.mcmap = {
    label: (d) => ({ overworld: 'Overworld', nether: 'Nether' }[d] || d),
    icons: { listing: () => ({ heads: { nowonline: 'v1' } }) },
    playerDetails: { open: (who) => { opened.push(who); return true; } },
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'roster.js'), 'utf8'), win, { filename: 'roster.js' });
  // The map is shown, which it is only with a session.
  if (viewed) doc.dispatchEvent({ type: 'mcmap:view' });
  return { win, doc, dialog, made, asked, opened, ticks, app: win.mcmap };
}

const rows = (t) => t.made.get('roster-list').querySelectorAll('button');
const said = (t) => t.made.get('roster-said').textContent;

test('is offered only once the server says it keeps a list', async () => {
  const kept = load({ body: { players: PLAYERS } });
  await tick();
  assert.equal(kept.made.get('roster-open').hidden, false);
  const none = load({ status: 404 });
  await tick();
  assert.equal(none.made.get('roster-open').hidden, true);
});

test('asks nothing until the map is shown past the login, and then once', async () => {
  const t = load({ body: { players: PLAYERS } }, { viewed: false });
  await tick();
  assert.deepEqual(t.asked, []);
  // The login locks the page, and says so with a view of its own.
  t.doc.body.classList.add('locked');
  t.doc.dispatchEvent({ type: 'mcmap:view' });
  await tick();
  assert.deepEqual(t.asked, []);
  t.doc.body.classList.remove('locked');
  t.doc.dispatchEvent({ type: 'mcmap:view' });
  t.doc.dispatchEvent({ type: 'mcmap:view' });
  await tick();
  assert.deepEqual(t.asked, ['api/players']);
  assert.equal(t.made.get('roster-open').hidden, false);
});

test('lists every player as the server orders them, with when and the tags it knows', async () => {
  const t = load({ body: { players: PLAYERS } });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  assert.equal(t.dialog.open, true);
  assert.equal(t.asked.at(-1), 'api/players');
  const list = rows(t);
  assert.equal(list.length, 3);
  assert.match(list[0].textContent, /^NowonlineOnline now in Nether · was OldnameOnlineOperator$/);
  assert.match(list[1].textContent, /Last seen 3 days ago/);
  assert.match(list[2].textContent, /Last seen 1 h agoYou$/);
  assert.equal(said(t), '3 players, 1 online.');
  assert.match(list[0].querySelector('img').src, /^api\/icons\/head\?name=nowonline&v=v1$/);
});

test('keeps a gamertag as text', async () => {
  const t = load({ body: { players: PLAYERS } });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  const name = rows(t)[1].querySelector('.roster-name');
  assert.equal(name.textContent, 'Away <b>x</b>');
  assert.equal(name.children.length, 0);
});

test('finds a player by a part of their gamertag, or of one they had', async () => {
  const t = load({ body: { players: PLAYERS } });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  const filter = t.made.get('roster-filter');
  filter.value = 'OLDN';
  filter.dispatchEvent({ type: 'input' });
  assert.equal(rows(t).length, 1);
  filter.value = 'nobody';
  filter.dispatchEvent({ type: 'input' });
  assert.equal(rows(t).length, 0);
  assert.match(said(t), /No player/);
});

test('a row opens that player’s details and leaves the list open beside them', async () => {
  const t = load({ body: { players: PLAYERS } });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  rows(t)[1].click();
  assert.deepEqual(t.opened.map((w) => ({ ...w })), [{ id: '-1', name: 'Away <b>x</b>' }]);
  assert.equal(t.dialog.open, true);
});

test('on a small screen a row puts the list away for the details', async () => {
  const t = load({ body: { players: PLAYERS } }, { compact: true });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  rows(t)[0].click();
  assert.equal(t.opened.length, 1);
  assert.equal(t.dialog.open, false);
});

test('says so when nobody has played and when the list cannot be read', async () => {
  const empty = load({ body: { players: [] } });
  await tick();
  empty.made.get('roster-open').click();
  await tick();
  assert.match(said(empty), /not seen anyone/);
  let fail = false;
  const broken = load(() => (fail ? { status: 500 } : { status: 404 }));
  await tick();
  fail = true;
  broken.made.get('roster-open').click();
  await tick();
  assert.match(said(broken), /could not be fetched/);
});

test('refreshes while open and stops asking once shut', async () => {
  const t = load({ body: { players: PLAYERS } });
  await tick();
  t.made.get('roster-open').click();
  await tick();
  const before = t.asked.length;
  t.ticks.at(-1)();
  await tick();
  assert.equal(t.asked.length, before + 1);
  t.dialog.dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(t.dialog.open, false);
  assert.equal(t.made.get('roster-open').getAttribute('aria-expanded'), 'false');
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`not ok ${name}\n${err.stack}`);
    }
  }
  process.exit(failed ? 1 : 0);
})();
