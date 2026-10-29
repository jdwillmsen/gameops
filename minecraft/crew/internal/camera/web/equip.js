// What entities hold and wear, apart from drawing it: frame decoding and the
// rules for placing and colouring items. Kept free of three.js so node can
// check it.

function reader(u8, p) {
  return {
    get p() { return p; },
    u8: () => { if (p >= u8.length) throw new RangeError('short frame'); return u8[p++]; },
    uvarint() { let v = 0, shift = 1; for (;;) { if (p >= u8.length) throw new RangeError('short frame'); const b = u8[p++]; v += (b & 127) * shift; if (b < 128) return v; shift *= 128; } },
    text() { if (p >= u8.length) throw new RangeError('short frame'); const n = u8[p++]; const s = new TextDecoder().decode(u8.subarray(p, p + n)); p += n; return s; },
  };
}

export const GLINT = 1, TRIM = 2;

// Equipment frame, after its type byte: id -> [main hand, off hand], each
// {name, glint}.
export function decodeEquipment(u8, start = 1) {
  const r = reader(u8, start), out = new Map();
  for (let n = r.uvarint(); n > 0; n--) {
    const id = r.uvarint(), hands = [];
    for (let i = 0; i < 2; i++) { const flags = r.u8(); hands.push({ glint: (flags & GLINT) !== 0, name: r.text() }); }
    out.set(id, hands);
  }
  return out;
}

// Armour detail frame, after its type byte: id -> four {glint, pattern,
// material}, helmet first. A slot without a trim has empty strings.
export function decodeArmourExtra(u8, start = 1) {
  const r = reader(u8, start), out = new Map();
  for (let n = r.uvarint(); n > 0; n--) {
    const id = r.uvarint(), slots = [];
    for (let i = 0; i < 4; i++) {
      const flags = r.u8(), s = { glint: (flags & GLINT) !== 0, pattern: '', material: '' };
      if (flags & TRIM) { s.pattern = r.text(); s.material = r.text(); }
      slots.push(s);
    }
    out.set(id, slots);
  }
  return out;
}

// Items the game holds out blade first, angled up and forward; everything
// else is held upright.
const HANDHELD = /(_sword|_axe|_pickaxe|_shovel|_hoe|_spear|_rod)$|^(stick|shears|bow|crossbow|trident|mace|fishing_rod|carrot_on_a_stick|warped_fungus_on_a_stick|brush|spyglass|flint_and_steel)$/;
export const isHandheld = name => HANDHELD.test(name);

// Items whose picture is filed under another name in the item sheet; the sheet
// is keyed by texture, and these are the held items whose id differs from it.
const ICON_ALIASES = {
  bow: 'bow_standby', crossbow: 'crossbow_standby', totem_of_undying: 'totem', golden_apple: 'apple_golden',
  golden_carrot: 'carrot_golden', cooked_beef: 'beef_cooked', cooked_porkchop: 'porkchop_cooked',
  cooked_chicken: 'chicken_cooked', cooked_mutton: 'mutton_cooked', cooked_cod: 'fish_cooked', beef: 'beef_raw',
  porkchop: 'porkchop_raw', chicken: 'chicken_raw', mutton: 'mutton_raw', cod: 'fish_raw', compass: 'compass_item',
  clock: 'clock_item', water_bucket: 'bucket_water', lava_bucket: 'bucket_lava', milk_bucket: 'bucket_milk',
  map: 'map_empty', filled_map: 'map_filled', book: 'book_normal', written_book: 'book_written',
  writable_book: 'book_writable', enchanted_book: 'book_enchanted', firework_rocket: 'fireworks',
  potion: 'potion_bottle_drinkable', splash_potion: 'potion_bottle_splash',
};
// The sheet key to look an item up under; `has` says whether a key is in it.
export const iconName = (name, has) => has(name) || !ICON_ALIASES[name] || !has(ICON_ALIASES[name]) ? name : ICON_ALIASES[name];

// Placement of a held icon in the hand's frame, in model units with forward
// +z and up +y: its size and how far its centre sits from the grip. A tool's
// diagonal runs up and forward; anything else is a smaller, upright picture.
export function holdPose(name) {
  return isHandheld(name)
    ? { size: 13.6, forward: 3.5, up: 3 }
    : { size: 8.8, forward: 1.5, up: 2 };
}

// Trim textures are grey in steps of 32 and the palette is eight colours,
// lightest first.
export const trimLevel = g => Math.max(0, Math.min(7, Math.floor((255 - g) / 32)));

// Recolours trim pixels (RGBA bytes) in place from a palette of eight [r, g, b].
export function recolorTrim(rgba, palette) {
  for (let i = 0; i < rgba.length; i += 4) {
    if (rgba[i + 3] === 0) continue;
    const c = palette[trimLevel(rgba[i])];
    rgba[i] = c[0]; rgba[i + 1] = c[1]; rgba[i + 2] = c[2];
  }
  return rgba;
}

// The material a piece of armour is made of, as a trim material is named, so
// a trim in its own material can use the darker palette the game does.
const ARMOUR_MATERIAL = [[/^iron_/, 'iron'], [/^golden_/, 'gold'], [/^diamond_/, 'diamond'], [/^netherite_/, 'netherite'], [/^copper_/, 'copper']];
export function trimPaletteName(armour, material) {
  for (const [re, m] of ARMOUR_MATERIAL) if (re.test(armour) && m === material) return material + '_darker';
  return material;
}

// The table key of a trim's picture: leggings have their own.
export const trimKey = (pattern, leggings) => leggings ? pattern + '_leggings' : pattern;
