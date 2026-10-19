// Entity drawing for the camera page: the pack's mob models once their table
// has loaded, stand-in boxes before that and for types the table lacks.
import * as THREE from 'three';

const RAD = Math.PI / 180;
const BLOCK = 1 / 16; // model units per block
const wrap = d => ((d + 540) % 360) - 180;

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

export function createEntities(scene) {
  const lights = [new THREE.AmbientLight(0xffffff, 1.6), new THREE.DirectionalLight(0xffffff, 1.4)];
  lights[1].position.set(.4, 1, .6);
  for (const l of lights) scene.add(l);

  const meshes = new Map(); // entity id -> instance
  const looks = new Map(); // entity id -> {baby, sheared, color, item}
  const names = new Map();
  let table = null, sheet = null, modelTex = null;
  let items = null; // {doc, tex}
  const boneGeos = new Map(); // table bone -> geometry
  const materials = new Map();
  const boxGeo = new THREE.BoxGeometry(1, 1, 1);
  const noseMat = new THREE.MeshLambertMaterial({ color: 0x20140c });

  function material(color, glass) {
    const key = color + (glass ? 'g' : '');
    let m = materials.get(key);
    if (!m) {
      m = glass
        ? new THREE.MeshLambertMaterial({ map: modelTex, alphaTest: .02, transparent: true, depthWrite: false, color })
        // Both sides, as the game draws cutout mobs: a chicken's legs are
        // painted only on the inside of each leg box's back face.
        : new THREE.MeshLambertMaterial({ map: modelTex, alphaTest: .5, color, side: THREE.DoubleSide });
      materials.set(key, m);
    }
    return m;
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
  function buildModel(model, look, type) {
    const baby = look.baby;
    let layers = model.d, size = model.s || 1;
    if (baby) {
      if (model.b) { layers = model.b; size = 1; } else size *= .5;
    }
    if (look.sheared && model.sh && !(baby && model.b)) layers = model.sh;
    const root = new THREE.Group();
    const scale = size * BLOCK;
    root.scale.set(scale, scale, -scale);
    const bones = [];
    for (const layer of layers) {
      const tint = layer.tint ? DYE[look.color & 15] : 0xffffff;
      const byName = new Map();
      for (const b of layer.bones) {
        const g = new THREE.Group();
        const bind = new THREE.Group();
        g.add(bind);
        const spiderLeg = /spider$/.test(type) && /^leg[0-7]$/.test(b.n) ? SPIDER_POSE[+b.n[3]] : null;
        if (b.r || spiderLeg) g.rotation.copy(packEuler(spiderLeg ?? b.r));
        if (b.bp) bind.rotation.copy(packEuler(b.bp));
        let geo = boneGeos.get(b);
        if (geo === undefined) { geo = boneGeometry(b, sheet); boneGeos.set(b, geo); }
        if (geo) bind.add(new THREE.Mesh(geo, material(tint, layer.glass)));
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
        bones.push({ name: b.n.toLowerCase(), g, base: g.rotation.clone() });
      }
    }
    const group = new THREE.Group();
    group.add(root);
    const head = bones.find(b => b.name === 'head') ?? null;
    return { group, head: null, headBone: head, bones, height: model.h * (baby ? (model.b ? 1 : .5) : 1), owned: [] };
  }

  function itemRect(name) {
    return items.doc.items['minecraft:' + name] ?? items.doc.placeholder;
  }

  // A dropped item is its icon on a plane that spins and bobs.
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

  function lookOf(id) { return looks.get(id) ?? { baby: false, sheared: false, color: 0, item: '' }; }

  function typeKey(type) {
    return type === 'player' ? 'minecraft:player' : type;
  }

  function build(inst) {
    const look = lookOf(inst.id), key = typeKey(inst.type);
    const model = table?.models[key];
    let built;
    if (model && modelTex) built = buildModel(model, look, key);
    else if (key === 'minecraft:item' && items && look.item) built = buildItem(look.item);
    else built = buildStandIn(inst.type);
    if (inst.body) { inst.root.remove(inst.body); disposeBody(inst); }
    inst.body = built.group; inst.built = built;
    inst.stamp = stamp(inst);
    inst.stride = .5 + .5 * built.height;
    inst.root.add(built.group);
    if (inst.label) inst.label.position.y = built.height + .4;
  }

  function stamp(inst) {
    const l = lookOf(inst.id);
    return [typeKey(inst.type), !!table, !!items, l.baby, l.sheared, l.color, l.item].join('|');
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
      cur: null, from: null, to: null, t0: 0, dur: 50, lastAt: 0, phase: 0, amp: 0, seed: Math.random() * 100 };
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

  function animate(inst, now, dt) {
    const c = inst.cur;
    const a = Math.min(1, (now - inst.t0) / inst.dur);
    const prevX = c.x, prevZ = c.z;
    c.x = inst.from.x + (inst.to.x - inst.from.x) * a;
    c.y = inst.from.y + (inst.to.y - inst.from.y) * a;
    c.z = inst.from.z + (inst.to.z - inst.from.z) * a;
    c.yaw = lerpAngle(inst.from.yaw, inst.to.yaw, a);
    c.head = lerpAngle(inst.from.head, inst.to.head, a);
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
    inst.phase += dist / inst.stride * Math.PI * 2;
    const speed = dt > 0 ? dist / dt : 0;
    inst.amp += (Math.min(1, speed / 1.6) - inst.amp) * Math.min(1, dt * 10);
    const swing = Math.cos(inst.phase) * inst.amp * .85;
    const t = now / 1000 + inst.seed;
    const headYaw = wrap(c.head - c.yaw) * RAD, pitch = Math.max(-1, Math.min(1, c.pitch * RAD));
    if (built.headBone) {
      const h = built.headBone;
      h.g.rotation.set(h.base.x - pitch + Math.sin(t * .9) * .02, h.base.y + Math.max(-1.3, Math.min(1.3, headYaw)), h.base.z, h.base.order);
    } else if (built.head) {
      built.head.rotation.y = -headYaw;
      built.head.rotation.x = pitch;
    }
    const raised = ARMS_FORWARD.test(inst.type);
    const spider = /spider/.test(inst.type);
    for (const b of built.bones) {
      const dir = LEG_SWING[b.name];
      const rest = b.base;
      if (dir !== undefined && !spider) {
        let x = rest.x + dir * swing;
        if (BIPED_LEGS.has(b.name) && b.name.includes('arm')) {
          if (raised) x = rest.x + Math.PI / 2 + dir * swing * .15;
          else x += Math.sin(t * 1.3 + (dir > 0 ? 0 : 2)) * .04 * (1 - inst.amp);
        }
        b.g.rotation.set(x, rest.y, rest.z, rest.order);
      } else if (spider && /^leg\d$/.test(b.name)) {
        const n = +b.name[3], s = Math.cos(inst.phase * 2 + (n % 2 ? Math.PI : 0) + n * .6) * inst.amp * .35;
        b.g.rotation.set(rest.x, rest.y + s, rest.z, rest.order);
      }
    }
  }

  return {
    meshes,
    get count() { return meshes.size; },
    get modelsReady() { return !!table; },

    async loadModels() {
      try {
        const doc = await fetch('/v1/camera/entities.json').then(r => { if (!r.ok) throw 0; return r.json(); });
        const img = await new Promise((ok, no) => { const i = new Image(); i.onload = () => ok(i); i.onerror = no; i.src = '/v1/camera/entities.png'; });
        const tex = new THREE.Texture(img);
        tex.magFilter = tex.minFilter = THREE.NearestFilter;
        tex.generateMipmaps = false; tex.flipY = false; tex.colorSpace = THREE.SRGBColorSpace;
        tex.needsUpdate = true;
        modelTex = tex; sheet = { w: doc.width, h: doc.height }; table = doc;
        boneGeos.clear(); materials.clear();
        for (const inst of meshes.values()) build(inst);
      } catch (e) {
        console.warn('entity models unavailable, drawing stand-ins', e);
      }
    },

    async loadItems() {
      if (items) return;
      try {
        const doc = await fetch('/v1/camera/items.json').then(r => { if (!r.ok) throw 0; return r.json(); });
        const img = await new Promise((ok, no) => { const i = new Image(); i.onload = () => ok(i); i.onerror = no; i.src = '/v1/camera/items.png'; });
        const tex = new THREE.Texture(img);
        tex.magFilter = tex.minFilter = THREE.NearestFilter;
        tex.generateMipmaps = false; tex.flipY = false; tex.colorSpace = THREE.SRGBColorSpace;
        tex.needsUpdate = true;
        items = { doc, tex };
        for (const inst of meshes.values()) if (inst.type === 'minecraft:item') build(inst);
      } catch { /* dropped items stay boxes */ }
    },

    update,

    setLook(id, flags, color, item) {
      looks.set(id, { baby: (flags & 1) !== 0, sheared: (flags & 2) !== 0, color, item });
      const inst = meshes.get(id);
      if (inst && inst.stamp !== stamp(inst)) build(inst);
      if (item && !items) this.loadItems();
    },

    setName(id, name) {
      names.set(id, name);
      const inst = meshes.get(id);
      if (inst) applyLabel(inst);
    },

    remove(id) {
      names.delete(id); looks.delete(id);
      const inst = meshes.get(id);
      if (!inst) return;
      scene.remove(inst.root);
      disposeBody(inst);
      if (inst.label) { inst.label.material.map.dispose(); inst.label.material.dispose(); }
      meshes.delete(id);
    },

    clear() {
      for (const id of [...meshes.keys()]) this.remove(id);
      names.clear(); looks.clear();
    },

    frame(now = performance.now()) {
      for (const inst of meshes.values()) {
        if (!inst.cur) continue;
        animate(inst, now, Math.min((now - (inst.lastFrame ?? now)) / 1000, .1));
        inst.lastFrame = now;
      }
    },
  };
}
