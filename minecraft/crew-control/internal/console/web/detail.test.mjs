import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ageText, freshness, roman, effectLabel, effectTime, enchantLabel, enchantText,
  durability, slotLabel, contentsText, slotTitle, iconLabel, armourSlots, slotGrid, inventoryRows, armorRow, chestState, nextSlot,
  iconRect, iconStyle, ENDER_SLOTS, HOTBAR_SLOTS, MAIN_SLOTS, ARMOR_SLOTS, ARMOUR_NAMES, ARMOUR_CAPTIONS, LIVE_STALE_MS,
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

test('freshness: a live unit keeps its save age; the save states are each spelled out', () => {
  const live = asOf => ({ state: 'known', asOf });
  // Armour, held items, xp, air, mode, dimension and the chest are the save's,
  // so "live" alone would overstate how fresh they are.
  assert.deepEqual(freshness({ live: live(ago(0, 1)), save: { state: 'known', asOf: ago(3 * 60) } }, NOW), { kind: 'live', text: 'live · save 3h ago' });
  assert.equal(freshness({ live: live(ago(0, 1)), save: { state: 'known', asOf: ago(0, 3) } }, NOW).text, 'live · save just now');
  assert.equal(freshness({ live: live(ago(0, 1)), save: { state: 'known', asOf: 'garbled' } }, NOW).text, 'live · save age unknown');
  // With no save to speak of, the live word stands alone.
  assert.equal(freshness({ live: live(ago(0, 1)), save: { state: 'off' } }, NOW).text, 'live');
  assert.equal(freshness({ live: live(ago(0, 1)), save: { state: 'none' } }, NOW).text, 'live');
  assert.equal(freshness({ live: { state: 'none' }, save: { state: 'known', asOf: ago(4) } }, NOW).text, 'from the save, 4 min ago');
  assert.equal(freshness({ save: { state: 'none' } }, NOW).text, "this unit's record is not saved yet");
  assert.equal(freshness({ save: { state: 'off' } }, NOW).text, 'world save not connected');
  assert.equal(freshness({ save: { state: 'pending' } }, NOW).text, 'reading the world save');
  assert.equal(freshness(null, NOW).kind, 'unknown');
  // A scanned-but-missing record still says not saved: the scan time is no excuse.
  assert.equal(freshness({ save: { state: 'none', asOf: ago(2) } }, NOW).kind, 'none');
});

test('freshness: an unreadable save time is not "just now"', () => {
  assert.equal(freshness({ save: { state: 'known', asOf: '' } }, NOW).text, 'from the save, age unknown');
  assert.equal(freshness({ save: { state: 'known' } }, NOW).text, 'from the save, age unknown');
  assert.equal(freshness({ save: { state: 'known', asOf: ago(0, 2) } }, NOW).text, 'from the save, just now');
});

test('freshness: a stalled poller is marked, not passed off as live', () => {
  const stalledFor = ms => new Date(NOW - ms).toISOString();
  const edge = freshness({ live: { state: 'known', asOf: stalledFor(LIVE_STALE_MS) }, save: { state: 'known', asOf: ago(60) } }, NOW);
  assert.equal(edge.kind, 'live', 'right at the limit is still live');
  const stale = freshness({ live: { state: 'known', asOf: stalledFor(5 * 60_000) }, save: { state: 'known', asOf: ago(60) } }, NOW);
  assert.deepEqual(stale, { kind: 'stale', text: 'last heard 5 min ago · save 60 min ago' });
  assert.equal(freshness({ live: { state: 'known', asOf: stalledFor(5 * 60_000) }, save: { state: 'off' } }, NOW).text, 'last heard 5 min ago');
  // A live answer with no readable time cannot be called stale.
  assert.equal(freshness({ live: { state: 'known' }, save: { state: 'off' } }, NOW).kind, 'live');
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
  // The answer omits a zero damage, so a pristine tool arrives without one.
  assert.equal(durability({ maxDamage: 250 }), 1);
});

test('a pristine tool says all of its durability is left, never NaN', () => {
  assert.equal(slotLabel(item(0, { name: 'iron_pickaxe', maxDamage: 250 })), 'iron_pickaxe 250/250 durability left');
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

test('slotLabel leads with the slot name when the place is part of the answer', () => {
  assert.equal(slotLabel(null, { slotName: 'off hand' }), 'off hand: empty');
  assert.equal(slotLabel(item(0, { name: 'iron_helmet' }), { slotName: 'head' }), 'head: iron_helmet');
  assert.equal(slotLabel(item(0, { name: 'iron_helmet' })), 'iron_helmet');
});

test('a box tells each inner stack in full, in its label for the hover and not only a count', () => {
  const box = item(0, { name: 'shulker_box', contents: [
    item(0, { name: 'diamond_sword', customName: 'Sting', enchantments: [{ id: 9, name: 'sharpness', level: 5 }, { id: 1, name: 'unbreaking', level: 3 }] }),
    item(1, { name: 'iron_ingot', count: 64 }),
  ] });
  assert.equal(contentsText(box), ' — inside: "Sting" diamond_sword sharpness V, unbreaking III; iron_ingot x64');
  assert.equal(slotTitle(box), 'shulker_box holds 2 stacks — inside: "Sting" diamond_sword sharpness V, unbreaking III; iron_ingot x64');
  assert.equal(slotLabel(box), 'shulker_box holds 2 stacks', 'the spoken label stays short');
  assert.equal(contentsText(item(0)), '');
  assert.equal(slotTitle(item(0, { count: 2 })), 'oak_log x2');
});

// A missing picture falls back to text, and the text must tell iron_ingot from iron_pickaxe.
test('iconLabel keeps the words that tell items apart', () => {
  assert.notEqual(iconLabel('iron_ingot'), iconLabel('iron_pickaxe'));
  assert.equal(iconLabel('minecraft:iron_ingot'), 'iron\ningot');
  assert.equal(iconLabel('diamond_chestplate'), 'diamo\nchestp');
  assert.equal(iconLabel('enchanted_golden_apple'), 'encha\napple');
  assert.equal(iconLabel('apple'), 'apple');
  assert.equal(iconLabel('netherite'), 'nether');
  assert.equal(iconLabel(''), '');
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

test('armourSlots is always six cells; a missing main hand keeps the off hand in its own place', () => {
  const offOnly = armourSlots({ armor: [item(0, { name: 'helmet' })], offhand: item(0, { name: 'shield' }) });
  assert.equal(offOnly.length, 6);
  assert.equal(offOnly[0].name, 'helmet');
  assert.equal(offOnly[4], null, 'no main hand: the cell stays empty');
  assert.equal(offOnly[5].name, 'shield');
  const both = armourSlots({ mainhand: item(0, { name: 'sword' }), offhand: item(0, { name: 'shield' }) });
  assert.deepEqual([both[4].name, both[5].name], ['sword', 'shield']);
  assert.deepEqual(armourSlots({}), [null, null, null, null, null, null]);
  assert.deepEqual(armourSlots(null), [null, null, null, null, null, null]);
  assert.deepEqual(ARMOUR_NAMES, ['head', 'chest', 'legs', 'feet', 'main hand', 'off hand']);
  assert.equal(ARMOUR_CAPTIONS.length, ARMOUR_NAMES.length);
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
