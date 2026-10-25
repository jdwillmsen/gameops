import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ageText, freshness, roman, effectLabel, effectTime, enchantLabel, enchantText,
  durability, slotLabel, slotGrid, inventoryRows, armorRow, chestState, nextSlot,
  iconRect, iconStyle, ENDER_SLOTS, HOTBAR_SLOTS, MAIN_SLOTS, ARMOR_SLOTS,
} from './detail.js';

const NOW = Date.parse('2026-10-24T12:00:00Z');
const ago = (mins, secs = 0) => new Date(NOW - mins * 60_000 - secs * 1000).toISOString();
const item = (slot, extra = {}) => ({ slot, name: 'oak_log', count: 1, ...extra });

// ---- ages and freshness ----

test('ageText coarse-words a save age; a clock a little ahead is still just now', () => {
  assert.equal(ageText(ago(0, 2), NOW), 'just now');
  assert.equal(ageText(ago(0, 40), NOW), '40s ago');
  assert.equal(ageText(ago(4), NOW), '4 min ago');
  assert.equal(ageText(ago(90), NOW), '1h ago');
  assert.equal(ageText(ago(20 * 60), NOW), '20h ago');
  assert.equal(ageText(ago(30 * 60), NOW), '1d ago');
  assert.equal(ageText(ago(30 * 1440), NOW), '30d ago');
  assert.equal(ageText('not a time', NOW), '');
  assert.equal(ageText('2026-10-24T13:00:00Z', NOW), 'just now');
});

test('freshness: the live poll wins; the save states are each spelled out', () => {
  assert.deepEqual(freshness({ live: { state: 'known' }, save: { state: 'known', asOf: ago(4) } }, NOW), { kind: 'live', text: 'live' });
  assert.equal(freshness({ live: { state: 'none' }, save: { state: 'known', asOf: ago(4) } }, NOW).text, 'from the save, 4 min ago');
  assert.equal(freshness({ save: { state: 'known', asOf: '' } }, NOW).text, 'from the save, just now');
  assert.equal(freshness({ save: { state: 'none' } }, NOW).text, "this unit's record is not saved yet");
  assert.equal(freshness({ save: { state: 'off' } }, NOW).text, 'world save not connected');
  assert.equal(freshness({ save: { state: 'pending' } }, NOW).text, 'reading the world save');
  assert.equal(freshness(null, NOW).kind, 'unknown');
  // A scanned-but-missing record still says not saved: the scan time is no excuse.
  assert.equal(freshness({ save: { state: 'none', asOf: ago(2) } }, NOW).kind, 'none');
});

// ---- naming ----

test('roman covers the game levels and falls back to figures past X', () => {
  assert.equal(roman(1), 'I');
  assert.equal(roman(9), 'IX');
  assert.equal(roman(10), 'X');
  assert.equal(roman(11), '11');
});

test('effects: a known name with its level, an unknown one by id', () => {
  assert.equal(effectLabel({ id: 18, name: 'invisibility', amplifier: 1 }), 'invisibility II');
  assert.equal(effectLabel({ id: 25, name: '', amplifier: 0 }), 'effect 25');
  assert.equal(effectTime(90), '0:05');
  assert.equal(effectTime(20 * 60), '1:00');
  assert.equal(effectTime(-1), '∞');
  assert.equal(effectTime('nope'), '');
});

test('enchantments spell name and level; a missing name keeps the id', () => {
  assert.equal(enchantLabel({ id: 9, name: 'sharpness', level: 4 }), 'sharpness IV');
  assert.equal(enchantLabel({ id: 99, level: 2 }), 'enchantment 99 II');
  assert.equal(enchantText({ enchantments: [{ id: 9, name: 'sharpness', level: 4 }] }), 'sharpness IV');
  assert.equal(enchantText({}), '');
});

test('durability is a fraction of what is left, null without a max', () => {
  assert.equal(durability({ damage: 100, maxDamage: 400 }), 0.75);
  assert.equal(durability({ damage: 0, maxDamage: 271 }), 1);
  assert.equal(durability({}), null);
});

// The slot label is everything the tooltip tells: a custom name first, the
// count, the enchantments, the durability and the box contents.
test('slotLabel says the whole story; an empty slot is an empty word', () => {
  assert.equal(slotLabel(null), 'empty');
  assert.equal(slotLabel(item(0)), 'oak_log');
  assert.equal(slotLabel(item(0, { count: 32 })), 'oak_log x32');
  assert.equal(slotLabel(item(0, { name: 'diamond_pickaxe', customName: 'Old Faithful', damage: 91, maxDamage: 1561, enchantments: [{ id: 1, name: 'unbreaking', level: 3 }] })),
    '"Old Faithful" diamond_pickaxe unbreaking III 1470/1561 durability left');
  assert.equal(slotLabel(item(0, { name: 'shulker_box', contents: [item(0), item(1)] })), 'shulker_box holds 2 stacks');
  const empty = 'nothing in here';
  assert.equal(slotLabel(null, { emptyLabel: empty }), empty);
});

// ---- grids ----

test('slotGrid fills the gaps; a slot past the window is dropped', () => {
  const g = slotGrid([item(2, { name: 'a' }), item(0, { name: 'b' })], 4);
  assert.deepEqual(g.map(x => (x ? x.name : '-')).join(''), 'b-a-');
  assert.deepEqual(slotGrid([item(3, { name: 'x' })], 3), [null, null, null]);
  assert.deepEqual(slotGrid(null, 3), [null, null, null]);
});

test('inventoryRows splits hotbar from the main 27 as the screens draw it', () => {
  const { main, hotbar } = inventoryRows([item(0, { name: 'h' }), item(9, { name: 'm' }), item(35, { name: 'z' })]);
  assert.equal(main.length, MAIN_SLOTS);
  assert.equal(hotbar.length, HOTBAR_SLOTS);
  assert.equal(hotbar[0].name, 'h');
  assert.equal(main[0].name, 'm');
  assert.equal(main[MAIN_SLOTS - 1].name, 'z');
  assert.equal(main[1], null);
});

test('armorRow is the four worn slots head to feet', () => {
  assert.deepEqual(armorRow([item(3, { name: 'boots' }), item(1, { name: 'chestplate' })]), [
    null, item(1, { name: 'chestplate' }), null, item(3, { name: 'boots' }),
  ]);
});

test('chestState counts what is filled and calls an empty chest empty', () => {
  const empty = chestState(new Array(ENDER_SLOTS).fill(null));
  assert.deepEqual(empty, { kind: 'empty', filled: 0, total: ENDER_SLOTS, chest: empty.chest });
  const chest = new Array(ENDER_SLOTS).fill(null);
  chest[0] = item(0);
  chest[26] = item(26);
  assert.equal(chestState(chest).filled, 2);
  assert.deepEqual(chestState(null), { kind: 'unsaved' });
});

test('nextSlot walks rows and stops at the ends: a grid is not a ring', () => {
  assert.equal(nextSlot(0, HOTBAR_SLOTS, 'ArrowRight'), 1);
  assert.equal(nextSlot(8, HOTBAR_SLOTS, 'ArrowRight'), 8);
  assert.equal(nextSlot(0, HOTBAR_SLOTS, 'ArrowLeft'), 0);
  assert.equal(nextSlot(5, MAIN_SLOTS + HOTBAR_SLOTS, 'ArrowDown'), 14);
  assert.equal(nextSlot(30, MAIN_SLOTS + HOTBAR_SLOTS, 'ArrowDown'), 30);
  assert.equal(nextSlot(3, MAIN_SLOTS + HOTBAR_SLOTS, 'ArrowUp'), 3);
  assert.equal(nextSlot(10, 36, 'Home'), null);
});

// ---- the item sheet ----

test('iconRect looks the game name up and keeps the placeholder as the floor', () => {
  const doc = { items: { 'minecraft:apple': [0, .5, .25, 1] }, placeholder: [.75, .75, 1, 1] };
  assert.deepEqual(iconRect(doc, 'apple'), [0, .5, .25, 1]);
  assert.deepEqual(iconRect(doc, 'nothing'), [.75, .75, 1, 1]);
  assert.equal(iconRect(null, 'apple'), null);
});

// The crop is percentage arithmetic: a style must show exactly the rect,
// scale-independently. A rect a quarter of the sheet wide is a tile four
// sheets wide, and its offsets put the tile's own edges at the element's.
test('iconStyle scales and shifts so the rect lands on the element', () => {
  const st = iconStyle([0, .5, .25, 1]);
  // A quarter of the width and half of the height: four sheets across,
  // two down.
  assert.equal(st.backgroundSize, '400% 200%');
  assert.equal(st.backgroundPosition, '0% 0%');
  // Second column down the middle, upper half of the sheet (v up).
  const mid = iconStyle([.25, .75, .5, 1]);
  assert.equal(mid.backgroundSize, '400% 400%');
  assert.equal(mid.backgroundPosition, '33% 0%');
  // Bottom-left tile of the sheet: from the top, last tile down.
  assert.equal(iconStyle([0, 0, .25, .25]).backgroundPosition, '0% 100%');
  // A rect the whole sheet is: nothing to crop, no shift is even possible.
  assert.deepEqual(iconStyle([0, 0, 1, 1]), { backgroundSize: '100% 100%', backgroundPosition: '0% 0%' });
  assert.equal(iconStyle(null), null);
});

test('armour slot names are head to feet in order', () => {
  assert.deepEqual(ARMOR_SLOTS, ['head', 'chest', 'legs', 'feet']);
});
