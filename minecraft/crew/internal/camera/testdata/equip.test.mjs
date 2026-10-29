// Pins the equipment frames and the item rules. Run by TestEquipModule with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const eq = await import(pathToFileURL(process.env.EQUIP_JS));
const bytes = (...a) => Uint8Array.from(a.flat());
const str = s => [s.length, ...Buffer.from(s)];

test('an equipment frame gives both hands per entity', () => {
  // Same bytes as the Go layout test: id 0 holds a glinting sword, id 300 a shield.
  const f = bytes(21, 2, 0, 1, str('iron_sword'), 0, str(''), 0xac, 0x02, 0, str(''), 0, str('shield'));
  const got = eq.decodeEquipment(f);
  assert.deepEqual(got.get(0), [{ glint: true, name: 'iron_sword' }, { glint: false, name: '' }]);
  assert.deepEqual(got.get(300), [{ glint: false, name: '' }, { glint: false, name: 'shield' }]);
});

test('an armour detail frame reads trims only where flagged', () => {
  const f = bytes(22, 1, 8, 3, str('coast'), str('quartz'), 0, 0, 1);
  const got = eq.decodeArmourExtra(f).get(8);
  assert.deepEqual(got[0], { glint: true, pattern: 'coast', material: 'quartz' });
  assert.deepEqual(got[1], { glint: false, pattern: '', material: '' });
  assert.equal(got[3].glint, true);
});

test('tools are held blade first and other items upright', () => {
  for (const n of ['netherite_sword', 'wooden_pickaxe', 'bow', 'stick', 'fishing_rod']) assert.ok(eq.isHandheld(n), n);
  for (const n of ['bread', 'torch', 'totem_of_undying', 'stone', 'shield']) assert.ok(!eq.isHandheld(n), n);
  assert.ok(eq.holdPose('iron_sword').size > eq.holdPose('bread').size);
});

test('trim greys map to the eight palette steps', () => {
  assert.equal(eq.trimLevel(255), 0);
  assert.equal(eq.trimLevel(224), 0);
  assert.equal(eq.trimLevel(192), 1);
  assert.equal(eq.trimLevel(96), 4);
  assert.equal(eq.trimLevel(0), 7);
  const pal = Array.from({ length: 8 }, (_, i) => [i * 10, i * 10 + 1, i * 10 + 2]);
  const px = Uint8ClampedArray.from([192, 192, 192, 255, 9, 9, 9, 0, 96, 96, 96, 255]);
  eq.recolorTrim(px, pal);
  assert.deepEqual([...px], [10, 11, 12, 255, 9, 9, 9, 0, 40, 41, 42, 255]);
});

test('a trim in the armour own material uses the darker palette', () => {
  assert.equal(eq.trimPaletteName('netherite_helmet', 'netherite'), 'netherite_darker');
  assert.equal(eq.trimPaletteName('golden_boots', 'gold'), 'gold_darker');
  assert.equal(eq.trimPaletteName('iron_helmet', 'quartz'), 'quartz');
  assert.equal(eq.trimPaletteName('chainmail_helmet', 'iron'), 'iron');
  assert.equal(eq.trimKey('coast', true), 'coast_leggings');
});

test('items filed under another texture name are found by it', () => {
  const sheet = new Set(['bow_standby', 'iron_sword', 'bow']);
  const has = n => sheet.has(n);
  assert.equal(eq.iconName('crossbow', has), 'crossbow');
  assert.equal(eq.iconName('iron_sword', has), 'iron_sword');
  assert.equal(eq.iconName('bow', has), 'bow');
  sheet.delete('bow');
  assert.equal(eq.iconName('bow', has), 'bow_standby');
});
