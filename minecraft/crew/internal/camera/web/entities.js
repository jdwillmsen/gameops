// Entity drawing for the camera page: the pack's mob models once their table
// has loaded, stand-in boxes before that and for types the table lacks.
import * as THREE from 'three';
import { compileAnimations, pose } from './molang.js';
import { holdPose, iconName, recolorTrim, trimKey, trimPaletteName } from './equip.js';

const RAD = Math.PI / 180;
const BLOCK = 1 / 16; // model units per block
const wrap = d => (((d % 360) + 540) % 360) - 180;

// Sheep wool multipliers by dye index, the game's own fleece colours.
const DYE = [
  0xffffff, 0xd98033, 0xb34dd9, 0x6699d9, 0xe6e633, 0x80cc1a, 0xf280a6, 0x4d4d4d,
  0x999999, 0x4d8099, 0x8040b3, 0x334db3, 0x664d33, 0x668033, 0x993333, 0x1a1a1a,
];

// Stand-ins: hit-box sizes in blocks and rough colours. kind picks the
// silhouette: biped is an upright body with a head on top, quad a long body
// with its head at the front, blob a single box. They are not the game's
// models.
const STAND_INS = {
  player: { w: .6, h: 1.8, color: 0x2f6fd8, head: 0xe0b48a, kind: 'biped' },
  zombie: { w: .6, h: 1.95, color: 0x3a6f9a, head: 0x5a9a4a, kind: 'biped' },
  skeleton: { w: .6, h: 1.99, color: 0xcfcfc6, head: 0xe6e6dc, kind: 'biped' },
  creeper: { w: .6, h: 1.7, color: 0x4bb23c, head: 0x5fcf4a, kind: 'biped', head_h: .5 },
  enderman: { w: .6, h: 2.9, color: 0x1c1226, head: 0x2a1a38, kind: 'biped', head_h: .5 },
  villager: { w: .6, h: 1.95, color: 0x7a5a3a, head: 0xd8a888, kind: 'biped' },
  iron_golem: { w: 1.4, h: 2.7, color: 0xb8b8b0, head: 0xd0d0c8, kind: 'biped', head_h: .7 },
  cow: { w: .9, h: 1.4, color: 0x4a3226, head: 0x5a4030, kind: 'quad', len: 1.3 },
  pig: { w: .9, h: .9, color: 0xe89a9a, head: 0xf0b0b0, kind: 'quad', len: 1.1 },
  sheep: { w: .9, h: 1.3, color: 0xe8e8e0, head: 0xc8b8a0, kind: 'quad', len: 1.2 },
  horse: { w: 1.4, h: 1.6, color: 0x8a5a30, head: 0x9a6a3a, kind: 'quad', len: 1.6 },
  wolf: { w: .6, h: .85, color: 0xbcbcb8, head: 0xd0d0cc, kind: 'quad', len: .9 },
  chicken: { w: .4, h: .7, color: 0xf2f2f2, head: 0xe8d8a0, kind: 'biped', head_h: .25 },
  spider: { w: 1.4, h: .9, color: 0x3a2f2f, head: 0x4a3a3a, kind: 'quad', len: 1.2 },
  slime: { w: .52, h: .52, color: 0x6fd05a, kind: 'blob' },
  item: { w: .25, h: .25, color: 0xd8d8e8, kind: 'blob' },
  xp_orb: { w: .25, h: .25, color: 0x9ae83a, kind: 'blob' },
  arrow: { w: .2, h: .2, color: 0x8a6a40, kind: 'blob' },
};
const DEFAULT_STAND_IN = { w: .6, h: 1.8, kind: 'biped' };

// Face corners of a box, top left, top right, bottom right, bottom left as
// the texture is laid on them, with the outward normal. Order matches the
// table: north, south, east, west, up, down. x0/x1 are the low and high x.
const FACES = [
  { n: [0, 0, -1], c: [[0, 1, 0], [1, 1, 0], [1, 0, 0], [0, 0, 0]] },
  { n: [0, 0, 1], c: [[1, 1, 1], [0, 1, 1], [0, 0, 1], [1, 0, 1]] },
  { n: [-1, 0, 0], c: [[0, 1, 1], [0, 1, 0], [0, 0, 0], [0, 0, 1]] },
  { n: [1, 0, 0], c: [[1, 1, 0], [1, 1, 1], [1, 0, 1], [1, 0, 0]] },
  { n: [0, 1, 0], c: [[0, 1, 1], [1, 1, 1], [1, 1, 0], [0, 1, 0]] },
  { n: [0, -1, 0], c: [[0, 0, 1], [1, 0, 1], [1, 0, 0], [0, 0, 0]] },
];

// The pack's rotations are the game's: degrees, turned in z, y, x order, and
// in the sense of a model whose z axis is mirrored when drawn.
function packEuler(r) {
  return new THREE.Euler(-(r?.[0] ?? 0) * RAD, (r?.[1] ?? 0) * RAD, -(r?.[2] ?? 0) * RAD, 'ZYX');
}

function boneGeometry(bone, sheet) {
  const pos = [], nor = [], uv = [], idx = [];
  const piv = bone.pv;
  for (const c of bone.c ?? []) {
    const g = c.i ?? 0;
    const lo = [c.o[0] - g, c.o[1] - g, c.o[2] - g], hi = [c.o[0] + c.sz[0] + g, c.o[1] + c.sz[1] + g, c.o[2] + c.sz[2] + g];
    const spin = c.r ? new THREE.Matrix4().makeRotationFromEuler(packEuler(c.r)) : null;
    const cp = new THREE.Vector3(...(c.pv ?? [0, 0, 0]));
    FACES.forEach((face, fi) => {
      const r = c.uv[fi];
      if (!r[0] && !r[1] && !r[2] && !r[3]) return;
      const base = pos.length / 3;
      const pts = face.c.map(k => {
        const p = new THREE.Vector3(k[0] ? hi[0] : lo[0], k[1] ? hi[1] : lo[1], k[2] ? hi[2] : lo[2]);
        if (spin) p.sub(cp).applyMatrix4(spin).add(cp);
        return p;
      });
      const n = new THREE.Vector3(...face.n);
      if (spin) n.applyMatrix4(spin);
      const u = [[r[0], r[1]], [r[2], r[1]], [r[2], r[3]], [r[0], r[3]]];
      pts.forEach((p, i) => {
        pos.push(p.x - piv[0], p.y - piv[1], p.z - piv[2]);
        nor.push(n.x, n.y, n.z);
        uv.push(u[i][0] / sheet.w, u[i][1] / sheet.h);
      });
      // Counter-clockwise seen from outside, in the model's own axes; the
      // mirrored z of the group above flips what the renderer treats as front.
      const flat = new THREE.Vector3().subVectors(pts[3], pts[0]).cross(new THREE.Vector3().subVectors(pts[2], pts[0]));
      if (flat.dot(n) >= 0) idx.push(base, base + 3, base + 2, base, base + 2, base + 1);
      else idx.push(base, base + 2, base + 3, base, base + 1, base + 2);
    });
  }
  if (!idx.length) return null;
  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.Float32BufferAttribute(pos, 3));
  geo.setAttribute('normal', new THREE.Float32BufferAttribute(nor, 3));
  geo.setAttribute('uv', new THREE.Float32BufferAttribute(uv, 2));
  geo.setIndex(idx);
  return geo;
}

// Where each box of the standard player layout starts in a 64-wide skin or
// armour texture; a left limb reuses the right one's, turned over.
const BODY_UV = {
  head: { u: 0, v: 0 }, body: { u: 16, v: 16 },
  rightarm: { u: 40, v: 16 }, leftarm: { u: 40, v: 16, mirror: true },
  rightleg: { u: 0, v: 16 }, leftleg: { u: 0, v: 16, mirror: true },
};
// Which bones each worn piece covers, and how far it stands off the body, in
// model units. Leggings cover the hips and legs from the second texture, the
// rest the first.
const ARMOUR_PARTS = [
  { head: 1 },
  { body: 1.01, rightarm: 1, leftarm: 1 },
  { body: .5, rightleg: .49, leftleg: .49 },
  { rightleg: 1, leftleg: 1 },
];
const ARMOUR_TEXTURES = [
  [/^leather_/, 'cloth'], [/^chainmail_/, 'chain'], [/^iron_/, 'iron'], [/^golden_/, 'gold'],
  [/^diamond_/, 'diamond'], [/^netherite_/, 'netherite'], [/^copper_/, 'copper'], [/^turtle_/, 'turtle'],
];
// Leather has no colour of its own in the sheet; this is the game's undyed brown.
const LEATHER = 0xa06540;

// The six face rects of a box laid out from (u, v) in the pack's box order:
// north, south, east, west, up, down. A mirrored box swaps its sides and turns
// every face over, which a rect marks by u0 > u1.
function boxFaces(u, v, w, h, d, mirror) {
  const n = [u + d, v + d, u + d + w, v + d + h], s = [u + 2 * d + w, v + d, u + 2 * d + 2 * w, v + d + h];
  let e = [u, v + d, u + d, v + d + h], wst = [u + d + w, v + d, u + 2 * d + w, v + d + h];
  const up = [u + d, v, u + d + w, v + d], down = [u + d + w, v, u + d + 2 * w, v + d];
  if (!mirror) return [n, s, e, wst, up, down];
  [e, wst] = [wst, e];
  return [n, s, e, wst, up, down].map(r => [r[2], r[1], r[0], r[3]]);
}

// A skin has its own texture, so the table's rects (which are in sheet
// pixels, with the player's texture placed somewhere on the sheet) are moved
// to the texture's own 64-wide grid. A slim skin has three-wide arms.
function skinBone(b, off, slim) {
  const arm = /arm|sleeve/i.test(b.n);
  const cubes = (b.c ?? []).map(c => {
    const uv = c.uv.map(r => (r[0] || r[1] || r[2] || r[3]) ? [r[0] - off[0], r[1] - off[1], r[2] - off[0], r[3] - off[1]] : r);
    if (!(slim && arm)) return { ...c, uv };
    const d = c.sz[2], o = [...c.o];
    if (/^right/i.test(b.n)) o[0] += 1;
    return { ...c, o, sz: [3, c.sz[1], d], uv: boxFaces(uv[2][0], uv[2][1] - d, 3, c.sz[1], d, false) };
  });
  return { ...b, c: cubes };
}

// Bones named like a limb swing with ground speed. Each entry is the swing
// direction: diagonal pairs of a quadruped share one, a biped's arm opposes
// its own side's leg.
const LEG_SWING = {
  leg0: 1, leg1: -1, leg2: -1, leg3: 1,
  rightleg: 1, leftleg: -1, rightarm: -1, leftarm: 1, arm0: -1, arm1: 1,
  backlegl: 1, backlegr: -1, frontlegl: -1, frontlegr: 1,
};
const BIPED_LEGS = new Set(['rightleg', 'leftleg', 'rightarm', 'leftarm', 'arm0', 'arm1']);
// Poses the pack sets with a default animation instead of in the geometry:
// a spider's legs are modelled straight.
const SPIDER_POSE = [[0, 45, -45], [0, -45, 45], [0, 22.5, -33.3], [0, -22.5, 33.3], [0, -22.5, -33.3], [0, 22.5, 33.3], [0, -45, -45], [0, 45, 45]];
const ARMS_FORWARD = /zombie|husk|drowned|zombie_pigman|zombified_piglin/;
const SPIDER_TYPE = /spider$/;
const SPIDER_LEG = /^leg[0-7]$/;

// A cheap fingerprint of a downloaded body, to tell a repeat of the same
// sheet from a changed one without keeping the sheet.
function fingerprint(bytes) {
  let h = 2166136261;
  for (let i = 0; i < bytes.length; i++) h = Math.imul(h ^ bytes[i], 16777619);
  return bytes.length + ':' + (h >>> 0);
}

// The table and its picture are fetched together and returned together, so a
// load can never pair one build's table with another's sheet.
async function fetchSheet(jsonUrl, pngUrl) {
  const [jsonRes, pngRes] = await Promise.all([fetch(jsonUrl), fetch(pngUrl)]);
  if (!jsonRes.ok || !pngRes.ok) throw new Error('sheet not available');
  const [jsonBytes, pngBytes] = await Promise.all([jsonRes.arrayBuffer(), pngRes.arrayBuffer()]);
  const doc = JSON.parse(new TextDecoder().decode(jsonBytes));
  const url = URL.createObjectURL(new Blob([pngBytes], { type: 'image/png' }));
  try {
    const img = await new Promise((ok, no) => { const i = new Image(); i.onload = () => ok(i); i.onerror = no; i.src = url; });
    const tex = new THREE.Texture(img);
    tex.magFilter = tex.minFilter = THREE.NearestFilter;
    tex.generateMipmaps = false; tex.flipY = false; tex.colorSpace = THREE.SRGBColorSpace;
    tex.needsUpdate = true;
    return { doc, tex, key: fingerprint(new Uint8Array(jsonBytes)) + '/' + fingerprint(new Uint8Array(pngBytes)) };
  } finally {
    URL.revokeObjectURL(url);
  }
}

export function createEntities(scene) {
  const LIGHT_DAY = [1.6, 1.4];
  const lights = [new THREE.AmbientLight(0xffffff, LIGHT_DAY[0]), new THREE.DirectionalLight(0xffffff, LIGHT_DAY[1])];
  lights[1].position.set(.4, 1, .6);
  for (const l of lights) scene.add(l);

  const meshes = new Map(); // entity id -> instance
  const looks = new Map(); // entity id -> {baby, sheared, color, item}
  const names = new Map();
  let table = null, sheet = null, modelTex = null;
  // The pack's own entities (JDW wildlife and the like) have their own sheet,
  // so they draw even when the vanilla one could not be fetched.
  const packSrc = { tex: null, sheet: null, geos: new Map(), mats: new Map(), models: null, key: null };
  const vanillaSrc = { get tex() { return modelTex; }, get sheet() { return sheet; }, geos: null, mats: null };
  let packLoading = null, packAgain = false;
  const animCache = new WeakMap(); // pack model -> compiled animations
  let items = null; // {doc, tex}
  let modelsKey = null, modelsLoading = null, modelsAgain = false;
  let itemsLoading = null, itemsFailed = false;
  const boneGeos = new Map(); // table bone -> geometry
  vanillaSrc.geos = boneGeos;
  const skinGeos = new Map(); // table bone -> [wide, slim] geometry on a skin's grid
  const armourGeos = new Map(); // table bone -> Map("slot|texture", geometry)
  const skins = new Map(); // entity id -> {slim, tex, key}
  const worn = new Map(); // entity id -> [helmet, chestplate, leggings, boots]
  const mods = new Map(); // entity id -> 4 x {glint, pattern, material}, as worn
  const held = new Map(); // entity id -> [main hand, off hand], each {name, glint}
  const glintTime = { value: 0 }; // one clock for every glinting material
  const trimGeos = new Map(); // table bone -> Map("slot|pattern") -> geometry
  const trimLooks = new Map(); // "pattern|palette|legs" -> {tex, mat}
  const iconGeos = new Map(); // item name -> held icon geometry
  const heldMats = {}; // shared materials of held items, over the item sheet
  let shieldGeos = null;
  const skinOffsets = new WeakMap(); // model -> where its player texture sits on the sheet
  const materials = new Map();
  vanillaSrc.mats = materials;
  const boxGeo = new THREE.BoxGeometry(1, 1, 1);
  const noseMat = new THREE.MeshLambertMaterial({ color: 0x20140c });

  function material(color, glass, src = vanillaSrc) {
    const key = color + (glass ? 'g' : '');
    const mats = src.mats;
    let m = mats.get(key);
    if (!m) {
      m = glass
        ? new THREE.MeshLambertMaterial({ map: src.tex, alphaTest: .02, transparent: true, depthWrite: false, color })
        // Both sides, as the game draws cutout mobs: a chicken's legs are
        // painted only on the inside of each leg box's back face.
        : new THREE.MeshLambertMaterial({ map: src.tex, alphaTest: .5, color, side: THREE.DoubleSide });
      mats.set(key, m);
    }
    return m;
  }

  // The skin's texture lies where the model's first layer has its lowest
  // rects: the head's side starts at u 0 and its top at v 0.
  function skinOffset(model) {
    let off = skinOffsets.get(model);
    if (!off) {
      off = [Infinity, Infinity];
      for (const b of model.d[0].bones) for (const c of b.c ?? []) for (const r of c.uv) {
        if (!(r[0] || r[1] || r[2] || r[3])) continue;
        off = [Math.min(off[0], r[0], r[2]), Math.min(off[1], r[1], r[3])];
      }
      skinOffsets.set(model, off);
    }
    return off;
  }

  function skinGeometry(b, off, slim) {
    let pair = skinGeos.get(b);
    if (!pair) skinGeos.set(b, pair = [undefined, undefined]);
    const i = slim ? 1 : 0;
    if (pair[i] === undefined) pair[i] = boneGeometry(skinBone(b, off, slim), { w: 64, h: 64 });
    return pair[i];
  }

  // One bone's box with a worn layer's picture laid over it: the body part's
  // own box stood off by `inflate`, its faces taken from rect r of a texture
  // that lays its parts out like a 64-wide skin.
  function pieceGeometry(b, r, inflate, dims) {
    const base = (b.c ?? []).find(c => !c.i);
    const spec = BODY_UV[b.n.toLowerCase()];
    if (!r || !base || !spec) return null;
    const scale = (r[2] - r[0]) / 64;
    const faces = boxFaces(spec.u, spec.v, base.sz[0], base.sz[1], base.sz[2], spec.mirror)
      .map(f => [r[0] + f[0] * scale, r[1] + f[1] * scale, r[0] + f[2] * scale, r[1] + f[3] * scale]);
    return boneGeometry({ pv: b.pv, c: [{ o: base.o, sz: base.sz, i: inflate, uv: faces }] }, dims);
  }

  // The worn piece's box on one bone, with the armour texture laid over it.
  function armourGeometry(b, slot, texKey, inflate) {
    const k = `${slot}|${texKey}`;
    let m = armourGeos.get(b);
    if (!m) armourGeos.set(b, m = new Map());
    if (!m.has(k)) m.set(k, pieceGeometry(b, table.armor?.[texKey], inflate, sheet));
    return m.get(k);
  }

  // A trim stands a hair off the armour so the two never fight for the pixel.
  function trimGeometry(b, slot, pattern, inflate) {
    const k = `${slot}|${pattern}`;
    let m = trimGeos.get(b);
    if (!m) trimGeos.set(b, m = new Map());
    if (!m.has(k)) m.set(k, pieceGeometry(b, [0, 0, 64, 32], inflate + .1, { w: 64, h: 32 }));
    return m.get(k);
  }

  // The trim picture recoloured to its material, made once per combination
  // from the sheet's grey original; null when the pack has no such trim.
  function trimLook(pattern, material, legs, armourName) {
    const palName = trimPaletteName(armourName, material);
    const key = `${pattern}|${palName}|${legs}`;
    if (trimLooks.has(key)) return trimLooks.get(key);
    const r = table?.trims?.[trimKey(pattern, legs)], pal = table?.trim_palettes?.[palName] ?? table?.trim_palettes?.[material];
    let look = null;
    if (r && pal && modelTex?.image) {
      const c = document.createElement('canvas');
      c.width = 64; c.height = 32;
      const ctx = c.getContext('2d', { willReadFrequently: true });
      ctx.drawImage(modelTex.image, r[0], r[1], 64, 32, 0, 0, 64, 32);
      const px = ctx.getImageData(0, 0, 64, 32);
      recolorTrim(px.data, pal);
      ctx.putImageData(px, 0, 0);
      const tex = new THREE.CanvasTexture(c);
      tex.magFilter = tex.minFilter = THREE.NearestFilter;
      tex.generateMipmaps = false; tex.flipY = false; tex.colorSpace = THREE.SRGBColorSpace;
      const mat = new THREE.MeshLambertMaterial({ map: tex, alphaTest: .5, polygonOffset: true, polygonOffsetFactor: -1, polygonOffsetUnits: -1 });
      look = { tex, mat };
    }
    trimLooks.set(key, look);
    return look;
  }

  // The enchantment shimmer: purple bands that drift across whatever the
  // overlay covers. It reads only the picture's alpha, so it follows the
  // shape of the item, and it is computed from the position on the model, so
  // one material serves every glinting item that shares a picture. Its colour
  // is written as it is, not converted, so it adds to the lit picture in the
  // same encoded space and stays a faint wash.
  function glintMaterial(map, key, side) {
    let m = materials.get(key);
    if (!m) {
      m = new THREE.ShaderMaterial({
        uniforms: { map: { value: map }, time: glintTime },
        vertexShader: `varying vec2 vUv; varying vec3 vP;
          void main() { vUv = uv; vP = position; gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }`,
        fragmentShader: `uniform sampler2D map; uniform float time; varying vec2 vUv; varying vec3 vP;
          void main() {
            if (texture2D(map, vUv).a < .5) discard;
            float a = (vP.x * .7 + vP.y + vP.z * .7) * .11, b = (vP.z * .9 - vP.x * .5 + vP.y * .4) * .17;
            float g = smoothstep(.55, 1., sin(a * 6.2832 + time * 1.1) * .5 + .5) * .6 + smoothstep(.6, 1., sin(b * 6.2832 - time * .8) * .5 + .5) * .4;
            gl_FragColor = vec4(vec3(.5, .25, 1.) * (.05 + g * .4), 1.);
          }`,
        blending: THREE.AdditiveBlending, transparent: true, depthWrite: false, side,
        polygonOffset: true, polygonOffsetFactor: -2, polygonOffsetUnits: -2, toneMapped: false,
      });
      materials.set(key, m);
    }
    return m;
  }

  // What a worn item is drawn with: the texture's name stem and its tint.
  function armourLook(name) {
    for (const [re, stem] of ARMOUR_TEXTURES) if (re.test(name)) return { stem, tint: stem === 'cloth' ? LEATHER : 0xffffff };
    return null;
  }

  function part(w, h, d, mat, x, y, z) {
    const m = new THREE.Mesh(boxGeo, mat);
    m.scale.set(w, h, d); m.position.set(x, y, z);
    return m;
  }

  function standInDef(type) {
    const name = type.replace(/^minecraft:/, '');
    const d = STAND_INS[name];
    if (d) return d;
    let h = 0; for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
    const color = new THREE.Color().setHSL((h % 360) / 360, .6, .5).getHex();
    return { ...DEFAULT_STAND_IN, color, head: new THREE.Color(color).offsetHSL(0, 0, .12).getHex() };
  }

  // Feet-origin group facing +z, so the body yaw is the group's own rotation.
  // `head` is a pivot at the neck that the head yaw and pitch turn.
  function buildStandIn(type) {
    const def = standInDef(type), g = new THREE.Group();
    const body = new THREE.MeshLambertMaterial({ color: def.color });
    const headMat = new THREE.MeshLambertMaterial({ color: def.head ?? def.color });
    const head = new THREE.Group();
    head.rotation.order = 'YXZ';
    if (def.kind === 'biped') {
      const hs = def.head_h ?? Math.min(.5, def.h * .26), bh = def.h - hs;
      g.add(part(def.w, bh, def.w * .6, body, 0, bh / 2, 0));
      head.position.set(0, bh, 0);
      head.add(part(hs, hs, hs, headMat, 0, hs / 2, 0), part(hs * .3, hs * .25, hs * .25, noseMat, 0, hs * .45, hs / 2));
    } else if (def.kind === 'quad') {
      const len = def.len ?? def.w * 1.2, hs = def.h * .36;
      g.add(part(def.w * .7, def.h * .45, len, body, 0, def.h * .55, 0), part(def.w * .45, def.h * .3, len * .6, body, 0, def.h * .15, 0));
      head.position.set(0, def.h * .75, len / 2);
      head.add(part(hs, hs, hs, headMat, 0, 0, hs * .4), part(hs * .3, hs * .25, hs * .25, noseMat, 0, -hs * .1, hs * .9));
    } else {
      g.add(part(def.w, def.h, def.w, body, 0, def.h / 2, 0), part(def.w * .3, def.h * .25, def.w * .25, noseMat, 0, def.h * .6, def.w / 2));
    }
    g.add(head);
    return { group: g, head, bones: [], height: def.h, owned: [body, headMat] };
  }

  // A model's layers as a bone tree. Table coordinates are the pack's: the
  // group's mirrored z turns the model's north-facing front toward +z.
  function buildModel(model, look, type, id, src = vanillaSrc) {
    const baby = look.baby;
    let layers = model.v?.[look.variant] ?? model.d, size = model.s || 1;
    if (baby) {
      if (model.b) { layers = model.b; size = 1; } else size *= .5;
    }
    if (look.sheared && model.sh && !(baby && model.b)) layers = model.sh;
    const root = new THREE.Group();
    const scale = size * BLOCK;
    root.scale.set(scale, scale, -scale);
    const bones = [], owned = [];
    const sk = type === 'minecraft:player' ? skins.get(id) : null;
    const skinMat = sk && new THREE.MeshLambertMaterial({ map: sk.tex, alphaTest: .5, side: THREE.DoubleSide });
    if (skinMat) owned.push(skinMat);
    const wearing = worn.get(id), wmods = mods.get(id), hands = held.get(id);
    const off = sk ? skinOffset(model) : null;
    layers.forEach((layer, layerIndex) => {
      const tint = layer.tint ? DYE[look.color & 15] : 0xffffff;
      const byName = new Map();
      for (const b of layer.bones) {
        const g = new THREE.Group();
        const bind = new THREE.Group();
        g.add(bind);
        const spiderLeg = SPIDER_TYPE.test(type) && SPIDER_LEG.test(b.n) ? SPIDER_POSE[+b.n[3]] : null;
        if (b.r || spiderLeg) g.rotation.copy(packEuler(spiderLeg ?? b.r));
        if (b.bp) bind.rotation.copy(packEuler(b.bp));
        if (skinMat && layerIndex === 0 && !layer.glass) {
          const sg = skinGeometry(b, off, sk.slim);
          if (sg) bind.add(new THREE.Mesh(sg, skinMat));
        } else {
          let geo = src.geos.get(b);
          if (geo === undefined) { geo = boneGeometry(b, src.sheet); src.geos.set(b, geo); }
          if (geo) bind.add(new THREE.Mesh(geo, material(tint, layer.glass, src)));
        }
        if (wearing && layerIndex === 0) {
          wearing.forEach((item, slot) => {
            const inflate = ARMOUR_PARTS[slot][b.n.toLowerCase()];
            const look = inflate && item ? armourLook(item) : null;
            if (!look) return;
            const geo = armourGeometry(b, slot, `${look.stem}_${slot === 2 ? 2 : 1}`, inflate);
            if (!geo) return;
            bind.add(new THREE.Mesh(geo, material(look.tint, false)));
            const mod = wmods?.[slot];
            if (mod?.pattern && mod.material) {
              const tl = trimLook(mod.pattern, mod.material, slot === 2, item);
              const tg = tl && trimGeometry(b, slot, mod.pattern, inflate);
              if (tg) bind.add(new THREE.Mesh(tg, tl.mat));
            }
            if (mod?.glint) bind.add(new THREE.Mesh(geo, glintMaterial(modelTex, 'glint', THREE.FrontSide)));
          });
        }
        byName.set(b.n, { b, g });
      }
      for (const { b, g } of byName.values()) {
        const parent = byName.get(b.p);
        if (parent && parent.b !== b) {
          g.position.set(b.pv[0] - parent.b.pv[0], b.pv[1] - parent.b.pv[1], b.pv[2] - parent.b.pv[2]);
          parent.g.add(g);
        } else {
          g.position.set(...b.pv);
          root.add(g);
        }
        const name = b.n.toLowerCase();
        if (layerIndex === 0 && hands) {
          const hand = name === 'rightitem' ? hands[0] : name === 'leftitem' ? hands[1] : null;
          if (hand?.name) holdItem(g, hand);
        }
        // Yaw then pitch, as the stand-ins turn, so a head never rolls.
        if (name === 'head') g.rotation.reorder('YXZ');
        bones.push({
          name, g, base: g.rotation.clone(), baseDeg: b.r ?? [0, 0, 0], basePos: g.position.clone(), head: name === 'head',
          swing: LEG_SWING[name], arm: BIPED_LEGS.has(name) && name.includes('arm'),
          spiderLeg: SPIDER_TYPE.test(type) && SPIDER_LEG.test(name) ? +name[3] : -1,
        });
      }
    });
    const group = new THREE.Group();
    group.add(root);
    let anim = null;
    if (model.an) {
      if (!animCache.has(model)) animCache.set(model, compileAnimations(model.an));
      anim = animCache.get(model);
    }
    return { group, head: null, bones, height: model.h * (baby ? (model.b ? 1 : .5) : 1), owned, anim };
  }

  // The icon of a held item as a flat plane in model units, its picture taken
  // from the item sheet. Built once per item and shared by everyone holding it.
  function iconGeometry(name) {
    let geo = iconGeos.get(name);
    if (!geo) {
      const r = itemRect(name), size = holdPose(name).size;
      geo = new THREE.PlaneGeometry(size, size);
      const uv = geo.getAttribute('uv');
      uv.setXY(0, r[0], r[1]); uv.setXY(1, r[2], r[1]); uv.setXY(2, r[0], r[3]); uv.setXY(3, r[2], r[3]);
      iconGeos.set(name, geo);
    }
    return geo;
  }

  // The shield: the pack's own model and texture when the table has them,
  // otherwise a plain board of the same size.
  function shieldParts() {
    const layer = table?.models?.['attachable:shield']?.d?.[0];
    if (layer) {
      shieldGeos ??= layer.bones.map(b => boneGeometry(b, sheet)).filter(Boolean);
      return { geos: shieldGeos, mat: material(0xffffff, false), glint: glintMaterial(modelTex, 'glint', THREE.FrontSide), at: [0, -9.5, 0] };
    }
    heldMats.board ??= new THREE.MeshLambertMaterial({ color: 0x8a6a40 });
    return { box: true, mat: heldMats.board, at: [0, 3, -3.5] };
  }

  // Puts what a hand holds on its item bone. The bone's frame is the model's:
  // units of 1/16 block with z mirrored, so tools sit in a holder that undoes
  // the mirror and are then placed forward and up from the grip.
  function holdItem(g, item) {
    if (item.name === 'shield') {
      const s = shieldParts(), grp = new THREE.Group();
      grp.position.set(...s.at);
      if (s.box) grp.add(part(12, 22, 1, s.mat, 0, 0, 0));
      else for (const geo of s.geos) {
        grp.add(new THREE.Mesh(geo, s.mat));
        if (item.glint) grp.add(new THREE.Mesh(geo, s.glint));
      }
      g.add(grp);
      return;
    }
    if (!items) return;
    heldMats.icon ??= new THREE.MeshLambertMaterial({ map: items.tex, alphaTest: .5, side: THREE.DoubleSide });
    const pose = holdPose(item.name), geo = iconGeometry(item.name);
    const holder = new THREE.Group(), icon = new THREE.Mesh(geo, heldMats.icon);
    holder.scale.set(1, 1, -1);
    icon.rotation.y = -Math.PI / 2;
    icon.position.set(0, pose.up, pose.forward);
    holder.add(icon);
    if (item.glint) {
      const shimmer = new THREE.Mesh(geo, glintMaterial(items.tex, 'glint-item', THREE.DoubleSide));
      shimmer.rotation.copy(icon.rotation); shimmer.position.copy(icon.position);
      holder.add(shimmer);
    }
    g.add(holder);
  }

  function itemRect(name) {
    const key = iconName(name, n => 'minecraft:' + n in items.doc.items);
    return items.doc.items['minecraft:' + key] ?? items.doc.placeholder;
  }

  // A flat icon rather than a model: the item sheet has one picture per item
  // and nothing to extrude a block from.
  function buildItem(name) {
    const r = itemRect(name);
    const geo = new THREE.PlaneGeometry(.4, .4);
    const uv = geo.getAttribute('uv');
    // Plane uv runs bottom-left first with v up; the sheet is top-down.
    uv.setXY(0, r[0], r[1]); uv.setXY(1, r[2], r[1]); uv.setXY(2, r[0], r[3]); uv.setXY(3, r[2], r[3]);
    const mat = new THREE.MeshBasicMaterial({ map: items.tex, alphaTest: .5, side: THREE.DoubleSide });
    const mesh = new THREE.Mesh(geo, mat);
    mesh.position.y = .25;
    const group = new THREE.Group();
    group.add(mesh);
    return { group, head: null, bones: [], height: .5, item: mesh, owned: [geo, mat] };
  }

  function lookOf(id) { return looks.get(id) ?? { baby: false, sheared: false, sitting: false, variant: 0, color: 0, item: '' }; }

  function typeKey(type) {
    return type === 'player' ? 'minecraft:player' : type;
  }

  function build(inst) {
    const look = lookOf(inst.id), key = typeKey(inst.type);
    const model = table?.models[key];
    const own = !model && packSrc.models?.[key];
    let built;
    if (model && modelTex) built = buildModel(model, look, key, inst.id);
    else if (own && packSrc.tex) built = buildModel(own, look, key, inst.id, packSrc);
    else if (key === 'minecraft:item' && items && look.item) built = buildItem(look.item);
    else built = buildStandIn(inst.type);
    if (inst.body) { inst.root.remove(inst.body); disposeBody(inst); }
    inst.body = built.group; inst.built = built;
    inst.stamp = stamp(inst);
    inst.stride = .5 + .5 * built.height;
    inst.root.add(built.group);
    if (inst.label) inst.label.position.y = built.height + .4;
  }

  const modsKey = id => mods.get(id)?.map(m => `${+m.glint}${m.pattern}.${m.material}`).join(',');
  const handsKey = id => held.get(id)?.map(h => `${h.name}${+h.glint}`).join(',');

  function stamp(inst) {
    const l = lookOf(inst.id);
    const sk = skins.get(inst.id);
    return [typeKey(inst.type), !!table, !!packSrc.models, !!items, l.baby, l.sheared, l.color, l.variant, l.item, sk?.key, sk?.slim, worn.get(inst.id)?.join(','), modsKey(inst.id), handsKey(inst.id)].join('|');
  }

  function disposeBody(inst) {
    for (const o of inst.built.owned) o.dispose();
  }

  function makeLabel(text) {
    const c = document.createElement('canvas');
    const ctx = c.getContext('2d');
    const font = '28px "Minecraft", monospace';
    ctx.font = font;
    c.width = Math.ceil(ctx.measureText(text).width) + 16; c.height = 36;
    ctx.font = font; ctx.textBaseline = 'middle';
    ctx.fillStyle = 'rgba(0,0,0,.35)'; ctx.fillRect(0, 0, c.width, c.height);
    ctx.fillStyle = '#fff'; ctx.fillText(text, 8, c.height / 2 + 1);
    const tex = new THREE.CanvasTexture(c);
    tex.colorSpace = THREE.SRGBColorSpace;
    const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, transparent: true, fog: false, depthWrite: false }));
    s.scale.set(.25 * c.width / c.height, .25, 1);
    return s;
  }

  function applyLabel(inst) {
    const name = names.get(inst.id);
    if (inst.labelled === name) return;
    if (inst.label) { inst.root.remove(inst.label); inst.label.material.map.dispose(); inst.label.material.dispose(); inst.label = null; }
    inst.labelled = name;
    if (!name) return;
    inst.label = makeLabel(name);
    inst.label.position.y = inst.built.height + .4;
    inst.root.add(inst.label);
  }

  function create(id, type) {
    const inst = { id, type, root: new THREE.Group(), body: null, built: null, label: null, labelled: undefined,
      cur: null, from: null, to: null, t0: 0, dur: 50, lastAt: 0, phase: 0, amp: 0, seed: Math.random() * 100,
      born: performance.now(), dist: 0, mspeed: 0, attackAt: -1e9,
      raised: ARMS_FORWARD.test(type), spider: SPIDER_TYPE.test(type) };
    scene.add(inst.root);
    meshes.set(id, inst);
    build(inst);
    applyLabel(inst);
    return inst;
  }

  const lerpAngle = (a, b, t) => a + wrap(b - a) * t;

  function update(e, now = performance.now()) {
    let inst = meshes.get(e.id);
    if (!inst) inst = create(e.id, e.type);
    else if (inst.stamp !== stamp(inst)) build(inst);
    const to = { x: e.x, y: e.y, z: e.z, yaw: e.yaw, pitch: e.pitch, head: e.head };
    if (!inst.cur) {
      inst.cur = { ...to }; inst.from = { ...to }; inst.to = to; inst.t0 = now; inst.lastAt = now;
      return;
    }
    // Move on from where the entity is drawn now, over about the time between
    // updates, so a batch every tick looks continuous.
    inst.from = { ...inst.cur };
    inst.to = to;
    inst.dur = Math.min(Math.max(now - inst.lastAt, 40), 200);
    inst.lastAt = now; inst.t0 = now;
  }

  // A pack entity is posed by its own animations. The queries are fed from
  // what the camera knows: the distance walked, a speed from 0 to 1 (a full
  // stride is about 5 blocks a second), the head's turn against the body, and
  // the sitting flag and attack swing the server sent.
  const ATTACK_MS = 400;
  // ?sit=1 shows every pack animal sitting whatever the server said, to look at
  // the pose where the game would not send the flag (a console event does not).
  const forceSit = new URLSearchParams(location.search).get('sit') === '1';
  function animatePack(inst, built, now, dt, dist) {
    const c = inst.cur;
    inst.dist += dist;
    const speed = dt > 0 ? dist / dt : 0;
    inst.mspeed += (Math.min(1, speed / 5) - inst.mspeed) * Math.min(1, dt * 10);
    const swing = (now - inst.attackAt) / ATTACK_MS;
    const ctx = {
      query: {
        modified_distance_moved: inst.dist, modified_move_speed: inst.mspeed, life_time: (now - inst.born) / 1000,
        target_x_rotation: c.pitch, target_y_rotation: wrap(c.head - c.yaw),
        is_sitting: forceSit || lookOf(inst.id).sitting, variant: lookOf(inst.id).variant,
      },
      variable: { attack_time: swing >= 0 && swing < 1 ? swing : 0 },
    };
    let posed;
    try { posed = pose(built.anim, ctx); } catch { return; }
    for (const b of built.bones) {
      const p = posed.get(b.name);
      const r = p ? [b.baseDeg[0] + p.rot[0], b.baseDeg[1] + p.rot[1], b.baseDeg[2] + p.rot[2]] : b.baseDeg;
      b.g.rotation.copy(packEuler(r));
      if (b.head) b.g.rotation.reorder('YXZ');
      b.g.position.copy(b.basePos);
      if (p) b.g.position.add({ x: p.pos[0], y: p.pos[1], z: p.pos[2] });
    }
  }

  function animate(inst, now, dt) {
    const c = inst.cur;
    const a = Math.min(1, (now - inst.t0) / inst.dur);
    const prevX = c.x, prevZ = c.z;
    c.x = inst.from.x + (inst.to.x - inst.from.x) * a;
    c.y = inst.from.y + (inst.to.y - inst.from.y) * a;
    c.z = inst.from.z + (inst.to.z - inst.from.z) * a;
    // Normalised after each step so repeated turns cannot accumulate whole
    // revolutions.
    c.yaw = wrap(lerpAngle(inst.from.yaw, inst.to.yaw, a));
    c.head = wrap(lerpAngle(inst.from.head, inst.to.head, a));
    c.pitch = inst.from.pitch + (inst.to.pitch - inst.from.pitch) * a;
    inst.root.position.set(c.x, c.y, c.z);
    inst.root.rotation.y = -c.yaw * RAD;

    const built = inst.built;
    if (built.item) {
      built.item.rotation.y = now / 700 + inst.seed;
      built.item.position.y = .25 + Math.sin(now / 400 + inst.seed) * .06;
      return;
    }
    const dist = Math.hypot(c.x - prevX, c.z - prevZ);
    if (built.anim) { animatePack(inst, built, now, dt, dist); return; }
    inst.phase += dist / inst.stride * Math.PI * 2;
    const speed = dt > 0 ? dist / dt : 0;
    inst.amp += (Math.min(1, speed / 1.6) - inst.amp) * Math.min(1, dt * 10);
    const swing = Math.cos(inst.phase) * inst.amp * .85;
    const t = now / 1000 + inst.seed;
    const headYaw = wrap(c.head - c.yaw) * RAD, pitch = Math.max(-1, Math.min(1, c.pitch * RAD));
    if (built.bones.length) {
      const yaw = Math.max(-1.3, Math.min(1.3, headYaw));
      for (const h of built.bones) {
        if (h.head) h.g.rotation.set(h.base.x - pitch + Math.sin(t * .9) * .02, h.base.y + yaw, h.base.z, h.base.order);
      }
    } else if (built.head) {
      built.head.rotation.y = -headYaw;
      built.head.rotation.x = pitch;
    }
    const { raised, spider } = inst;
    for (const b of built.bones) {
      const dir = b.swing;
      const rest = b.base;
      if (dir !== undefined && !spider) {
        let x = rest.x + dir * swing;
        if (b.arm) {
          if (raised) x = rest.x + Math.PI / 2 + dir * swing * .15;
          else x += Math.sin(t * 1.3 + (dir > 0 ? 0 : 2)) * .04 * (1 - inst.amp);
        }
        b.g.rotation.set(x, rest.y, rest.z, rest.order);
      } else if (spider && b.spiderLeg >= 0) {
        const n = b.spiderLeg, s = Math.cos(inst.phase * 2 + (n % 2 ? Math.PI : 0) + n * .6) * inst.amp * .35;
        b.g.rotation.set(rest.x, rest.y + s, rest.z, rest.order);
      }
    }
  }

  return {
    meshes,
    get count() { return meshes.size; },
    get modelsReady() { return !!table; },

    // Frame 12 arrives on every attach and reconnect, so a load of what is
    // already held does nothing, and a load asked for while another runs is
    // run once more after it rather than alongside it.
    loadModels() {
      this.loadPack();
      if (modelsLoading) { modelsAgain = true; return modelsLoading; }
      modelsLoading = (async () => {
        try {
          const loaded = await fetchSheet('v1/camera/entities.json', 'v1/camera/entities.png');
          if (loaded.key === modelsKey) { loaded.tex.dispose(); return; }
          const old = {
            tex: modelTex, mats: [...materials.values()],
            geos: [...boneGeos.values(), ...[...skinGeos.values()].flat(), ...[...armourGeos.values()].flatMap(m => [...m.values()]),
            ...[...trimGeos.values()].flatMap(m => [...m.values()]), ...(shieldGeos ?? [])],
            trims: [...trimLooks.values()],
          };
          modelsKey = loaded.key; modelTex = loaded.tex; table = loaded.doc; sheet = { w: loaded.doc.width, h: loaded.doc.height };
          boneGeos.clear(); skinGeos.clear(); armourGeos.clear(); trimGeos.clear(); trimLooks.clear(); materials.clear(); shieldGeos = null;
          for (const inst of meshes.values()) build(inst);
          old.tex?.dispose();
          for (const g of old.geos) g?.dispose();
          for (const m of old.mats) m.dispose();
          for (const t of old.trims) if (t) { t.tex.dispose(); t.mat.dispose(); }
          itemsFailed = false;
        } catch (e) {
          console.warn('entity models unavailable, drawing stand-ins', e);
        } finally {
          modelsLoading = null;
          if (modelsAgain) { modelsAgain = false; this.loadModels(); }
        }
      })();
      return modelsLoading;
    },

    // The pack's own entities. A server with none answers 404, which leaves
    // everything as it was.
    loadPack() {
      if (packLoading) { packAgain = true; return packLoading; }
      packLoading = (async () => {
        try {
          const loaded = await fetchSheet('v1/camera/pack.json', 'v1/camera/pack.png');
          if (loaded.key === packSrc.key) { loaded.tex.dispose(); return; }
          const old = { tex: packSrc.tex, mats: [...packSrc.mats.values()], geos: [...packSrc.geos.values()] };
          packSrc.key = loaded.key; packSrc.tex = loaded.tex; packSrc.models = loaded.doc.models;
          packSrc.sheet = { w: loaded.doc.width, h: loaded.doc.height };
          packSrc.geos.clear(); packSrc.mats.clear();
          for (const inst of meshes.values()) build(inst);
          old.tex?.dispose();
          for (const g of old.geos) g?.dispose();
          for (const m of old.mats) m.dispose();
        } catch {
          // No pack entities on this server, or the request failed; boxes stay.
        } finally {
          packLoading = null;
          if (packAgain) { packAgain = false; this.loadPack(); }
        }
      })();
      return packLoading;
    },

    loadItems() {
      if (items || itemsFailed) return Promise.resolve();
      if (itemsLoading) return itemsLoading;
      itemsLoading = (async () => {
        try {
          const loaded = await fetchSheet('v1/camera/items.json', 'v1/camera/items.png');
          items = { doc: loaded.doc, tex: loaded.tex };
          for (const inst of meshes.values()) if (inst.stamp !== stamp(inst)) build(inst);
        } catch {
          // Remembered, so every look update does not repeat the request; a
          // later models frame clears it.
          itemsFailed = true;
        } finally {
          itemsLoading = null;
        }
      })();
      return itemsLoading;
    },

    update,

    setLook(id, flags, color, item) {
      looks.set(id, { baby: (flags & 1) !== 0, sheared: (flags & 2) !== 0, sitting: (flags & 4) !== 0, variant: color >> 4, color: color & 15, item });
      const inst = meshes.get(id);
      if (inst && inst.stamp !== stamp(inst)) build(inst);
      if (item && !items) this.loadItems();
    },

    // The entity swings at something; animations that read attack_time play.
    attack(id, now = performance.now()) {
      const inst = meshes.get(id);
      if (inst) inst.attackAt = now;
    },

    // A player's skin, a PNG on the standard layout. The texture is made
    // before the body is rebuilt, so the default skin shows until it is ready.
    clearSkin(id) {
      const old = skins.get(id);
      if (!old) return;
      skins.delete(id);
      const inst = meshes.get(id);
      if (inst) build(inst);
      old.tex.dispose();
    },
    async setSkin(id, slim, bytes) {
      const url = URL.createObjectURL(new Blob([bytes], { type: 'image/png' }));
      try {
        const img = await new Promise((ok, no) => { const i = new Image(); i.onload = () => ok(i); i.onerror = no; i.src = url; });
        const tex = new THREE.Texture(img);
        tex.magFilter = tex.minFilter = THREE.NearestFilter;
        tex.generateMipmaps = false; tex.flipY = false; tex.colorSpace = THREE.SRGBColorSpace;
        tex.needsUpdate = true;
        const old = skins.get(id);
        skins.set(id, { slim, tex, key: fingerprint(bytes) });
        const inst = meshes.get(id);
        if (inst) build(inst);
        old?.tex.dispose();
      } catch (e) {
        console.warn('skin unavailable, drawing the default', e);
      } finally {
        URL.revokeObjectURL(url);
      }
    },

    // What an entity wears, helmet first; "" is an empty slot.
    setArmour(id, slots) {
      worn.set(id, slots);
      const inst = meshes.get(id);
      if (inst && inst.stamp !== stamp(inst)) build(inst);
    },

    // The enchantment and trim of each worn piece, helmet first.
    setArmourMods(id, slots) {
      mods.set(id, slots);
      const inst = meshes.get(id);
      if (inst && inst.stamp !== stamp(inst)) build(inst);
    },

    // What an entity holds, main hand then off hand.
    setHeld(id, hands) {
      held.set(id, hands);
      if (!items && hands.some(h => h.name && h.name !== 'shield')) this.loadItems();
      const inst = meshes.get(id);
      if (inst && inst.stamp !== stamp(inst)) build(inst);
    },

    setName(id, name) {
      names.set(id, name);
      const inst = meshes.get(id);
      if (inst) applyLabel(inst);
    },

    remove(id) {
      names.delete(id); looks.delete(id);
      // The unit's own body is removed and re-added as the mode changes, and
      // keeps what it wears.
      if (id !== -1) { worn.delete(id); mods.delete(id); held.delete(id); skins.get(id)?.tex.dispose(); skins.delete(id); }
      const inst = meshes.get(id);
      if (!inst) return;
      scene.remove(inst.root);
      disposeBody(inst);
      if (inst.label) { inst.label.material.map.dispose(); inst.label.material.dispose(); }
      meshes.delete(id);
    },

    clear() {
      for (const id of [...meshes.keys()]) this.remove(id);
      names.clear(); looks.clear(); worn.clear(); mods.clear(); held.clear();
      for (const sk of skins.values()) sk.tex.dispose();
      skins.clear();
    },

    // Entities carry no baked light, so the time of day dims them as a whole:
    // k is 1 at noon and falls towards the night floor.
    setAmbient(k) {
      lights[0].intensity = LIGHT_DAY[0] * k;
      lights[1].intensity = LIGHT_DAY[1] * k;
    },

    frame(now = performance.now()) {
      glintTime.value = now / 1000;
      for (const inst of meshes.values()) {
        if (!inst.cur) continue;
        animate(inst, now, Math.min((now - (inst.lastFrame ?? now)) / 1000, .1));
        inst.lastFrame = now;
      }
    },
  };
}
