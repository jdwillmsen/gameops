// Renders turntables, animation loops and contact sheets of the wildlife pack
// models in the web Blockbench, driven through a headless Chrome.
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { bake, CLIPS } from "./bake.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const { chromium } = createRequire(import.meta.url)("playwright-core");

// The page is an external app: pin the version and replay a recorded copy so
// a Blockbench release cannot silently change the renders.
const BLOCKBENCH_URL = "https://web.blockbench.net";
const BLOCKBENCH_VERSION = "5.2.2";
const VIEW = { width: 960, height: 720 };
const FPS = 24;

function parseArgs(argv) {
  const o = { animal: [], fast: false, out: null, pack: path.resolve(here, "../../packs/lab-mods-wildlife"), chrome: process.env.CHROME ?? "/opt/google/chrome/chrome" };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const value = () => argv[++i] ?? die(`${a} needs a value`);
    if (a === "--animal") o.animal.push(...value().split(","));
    else if (a === "--fast") o.fast = true;
    else if (a === "--out") o.out = value();
    else if (a === "--pack") o.pack = path.resolve(value());
    else if (a === "--help" || a === "-h") {
      console.log("usage: render.sh [--animal tiger,lion] [--fast] [--out DIR] [--pack DIR]");
      process.exit(0);
    } else throw new Error(`unknown argument ${a}`);
  }
  return o;
}

const json = (p) => JSON.parse(fs.readFileSync(p, "utf8"));
const die = (m) => { throw new Error(m); };

function runId() {
  let sha = "nogit";
  try { sha = execFileSync("git", ["-C", here, "rev-parse", "--short", "HEAD"], { encoding: "utf8" }).trim(); } catch {}
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}-${sha}`;
}

// Applies the render controller's part_visibility the way the game does for
// one variant; anything beyond a plain `query.variant == N` is refused.
function hiddenBones(rc, variant, name) {
  const hidden = [];
  for (const entry of rc.part_visibility ?? []) {
    for (const [bone, cond] of Object.entries(entry)) {
      if (bone === "*" || cond === true) continue;
      if (cond === false) { hidden.push(bone); continue; }
      const m = /^query\.variant\s*==\s*(\d+)$/.exec(String(cond));
      if (!m) die(`${name}: unsupported part_visibility condition for ${bone}: ${cond}`);
      if (Number(m[1]) !== variant) hidden.push(bone);
    }
  }
  return hidden;
}

function loadAnimals(pack, filter) {
  const rp = path.join(pack, "resource_pack");
  const animations = {};
  for (const f of fs.readdirSync(path.join(rp, "animations"))) Object.assign(animations, json(path.join(rp, "animations", f)).animations);
  const animals = [];
  for (const f of fs.readdirSync(path.join(rp, "entity")).sort()) {
    const d = json(path.join(rp, "entity", f))["minecraft:client_entity"].description;
    const name = d.identifier.split(":")[1];
    if (filter.length && !filter.includes(name)) continue;
    const geoId = d.geometry.default;
    let geo;
    for (const g of fs.readdirSync(path.join(rp, "models/entity"))) {
      const j = json(path.join(rp, "models/entity", g));
      if (j["minecraft:geometry"].some((x) => x.description.identifier === geoId)) { geo = j; break; }
    }
    if (!geo) die(`${name}: geometry ${geoId} not found`);
    const rc = Object.values(json(path.join(rp, "render_controllers", `${name}.render_controllers.json`)).render_controllers)[0];
    const skins = (rc.arrays?.textures?.["Array.skins"] ?? []).map((t) => t.replace(/^Texture\./, ""));
    const variants = Object.entries(d.textures).map(([key, tex]) => {
      if (!skins.length && Object.keys(d.textures).length > 1) die(`${name}: several textures but no skin array in the render controller`);
      const variant = skins.length ? skins.indexOf(key) : 0;
      if (variant < 0) die(`${name}: texture ${key} is not in the render controller's skin array`);
      return { key, file: path.join(rp, `${tex}.png`), hidden: hiddenBones(rc, variant, name) };
    });
    const layers = {};
    for (const [short, id] of Object.entries(d.animations)) {
      layers[short] = animations[id] ?? die(`${name}: animation ${id} not found`);
    }
    animals.push({ name, geo, geoId, variants, layers });
  }
  const missing = filter.filter((n) => !animals.some((a) => a.name === n));
  if (missing.length) die(`unknown animal: ${missing.join(", ")}`);
  return animals;
}

function bounds(geo, geoId) {
  const g = geo["minecraft:geometry"].find((x) => x.description.identifier === geoId);
  const lo = [1e9, 1e9, 1e9], hi = [-1e9, -1e9, -1e9];
  for (const b of g.bones) for (const c of b.cubes ?? []) for (let k = 0; k < 3; k++) {
    lo[k] = Math.min(lo[k], c.origin[k]);
    hi[k] = Math.max(hi[k], c.origin[k] + c.size[k]);
  }
  return { center: lo.map((v, k) => (v + hi[k]) / 2), extent: Math.max(...hi.map((v, k) => v - lo[k])) };
}

async function openModel(ctx, animal, variant) {
  const page = await ctx.newPage();
  await page.goto(BLOCKBENCH_URL, { waitUntil: "load" });
  await page.waitForFunction(() => typeof Blockbench !== "undefined" && typeof Formats !== "undefined" && typeof Animator !== "undefined", null, { timeout: 60000 });
  const version = await page.evaluate(() => Blockbench.version);
  if (version !== BLOCKBENCH_VERSION) die(`Blockbench ${version} loaded, tooling is pinned to ${BLOCKBENCH_VERSION}`);
  const tex = fs.readFileSync(variant.file).toString("base64");
  const info = await page.evaluate(({ geo, tex }) => {
    Formats.bedrock.new();
    Codecs.bedrock.parse(geo, "model.geo.json");
    new Texture({ name: "tex.png" }).fromDataURL("data:image/png;base64," + tex).add();
    return { cubes: Cube.all.length, groups: Group.all.map((g) => g.name), textures: Texture.all.length };
  }, { geo: animal.geo, tex });
  if (!info.cubes || info.textures !== 1) die(`${animal.name}: model did not load (${JSON.stringify(info)})`);
  // Confirm would reset the UV size to 16x16 and crush the model.
  const cancel = page.getByRole("button", { name: "Cancel", exact: true });
  await cancel.waitFor({ timeout: 10000 });
  await cancel.click();
  await page.waitForTimeout(500);
  await page.addStyleTag({ content: `.preview_menu, .orbit_gizmo, .preview_perspective_label { display: none !important; }` });
  const hiddenBones = await page.evaluate((names) => {
    const missing = names.filter((n) => !Group.all.some((g) => g.name === n));
    if (missing.length) return { missing };
    // Group visibility alone does not hide the meshes in this Blockbench.
    for (const g of Group.all) if (names.includes(g.name)) g.forEachChild((c) => { c.visibility = false; }, Cube);
    Canvas.updateVisibility();
    return { ok: true };
  }, variant.hidden);
  if (!hiddenBones.ok) die(`${animal.name}: part_visibility names missing bones ${hiddenBones.missing}`);
  const hidden = await page.evaluate(({ w, h }) => {
    unselectAll();
    let n = 0;
    Canvas.scene.traverse((o) => { if (o.name === "grid_group") { o.visible = false; n++; } });
    return n;
  }, { w: VIEW.width, h: VIEW.height });
  if (!hidden) die("Blockbench scene has no grid_group to hide, the page API changed");
  return { page, info };
}

async function frameBox(page) {
  const size = { w: VIEW.width, h: VIEW.height };
  await page.evaluate(({ w, h }) => { const p = Preview.selected;
    p.renderer.setSize(w, h, false);
    p.renderer.setClearColor(0x6b7685, 1);
    p.camera.aspect = w / h;
    p.camera.updateProjectionMatrix();
    p.render(); }, size);
  const box = await page.evaluate(() => {
    const c = Preview.selected.canvas;
    return { width: c.width, height: c.height };
  });
  if (box.width !== VIEW.width || box.height !== VIEW.height) die(`preview canvas is ${box.width}x${box.height}, layout override failed`);
  return box;
}

async function setCamera(page, b, yaw, pitch) {
  await page.evaluate(({ b, yaw, pitch }) => {
    const p = Preview.selected;
    const r = b.extent * 1.25;
    const y = (yaw * Math.PI) / 180, e = (pitch * Math.PI) / 180;
    p.controls.target.set(...b.center);
    // Bedrock models face -Z: yaw 0 looks at the nose, 90 at the model's left.
    p.camera.position.set(b.center[0] - Math.sin(y) * Math.cos(e) * r, b.center[1] + Math.sin(e) * r, b.center[2] - Math.cos(y) * Math.cos(e) * r);
    p.camera.lookAt(...b.center);
    p.controls.update?.();
    p.render();
  }, { b, yaw, pitch });
}

async function shot(page, box, file) {
  // Read the WebGL canvas in the same task as the render, before the buffer clears.
  const url = await page.evaluate(() => { const p = Preview.selected; p.render(); return p.canvas.toDataURL("image/png"); });
  fs.writeFileSync(file, Buffer.from(url.split(",")[1], "base64"));
}

function ffmpeg(args) {
  execFileSync("ffmpeg", ["-y", "-loglevel", "error", ...args], { stdio: ["ignore", "inherit", "inherit"] });
}

function encode(framesDir, out) {
  ffmpeg(["-framerate", String(FPS), "-i", path.join(framesDir, "%04d.png"), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "20", "-movflags", "+faststart", out]);
}

const sha = (f) => crypto.createHash("sha1").update(fs.readFileSync(f)).digest("hex");

async function main() {
  const opt = parseArgs(process.argv.slice(2));
  const animals = loadAnimals(opt.pack, opt.animal);
  const id = runId();
  const out = path.resolve(opt.out ?? path.join(os.homedir(), "evidence/wildlife", id, "models"));
  fs.mkdirSync(out, { recursive: true });
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "model-preview-"));
  const harDir = path.join(process.env.XDG_CACHE_HOME ?? path.join(os.homedir(), ".cache"), "model-preview");
  fs.mkdirSync(harDir, { recursive: true });
  const har = path.join(harDir, `blockbench-${BLOCKBENCH_VERSION}.har`);
  const record = !fs.existsSync(har);

  const browser = await chromium.launch({ executablePath: opt.chrome, headless: true, args: ["--no-sandbox"] });
  let ctx;
  let ok = false;
  const manifest = [];
  const turnFrames = opt.fast ? 8 : 72;
  const clipNames = opt.fast ? ["walk"] : Object.keys(CLIPS);
  try {
    ctx = await browser.newContext({ viewport: VIEW });
    await ctx.routeFromHAR(har, { url: /blockbench\.net/, update: record, updateContent: "embed", notFound: "abort" });
    for (const animal of animals) {
      const b = bounds(animal.geo, animal.geoId);
      const stills = [];
      for (const variant of animal.variants) {
        const label = `${animal.name}_${variant.key}`;
        console.log(`== ${label}`);
        const { page } = await openModel(ctx, animal, variant);
        const box = await frameBox(page);
        const fdir = path.join(work, label);
        const still = (suffix) => path.join(work, `${label}.${suffix}.png`);

        // turntable
        fs.rmSync(fdir, { recursive: true, force: true });
        fs.mkdirSync(fdir, { recursive: true });
        for (let i = 0; i < turnFrames; i++) {
          await setCamera(page, b, (360 * i) / turnFrames, 14);
          await shot(page, box, path.join(fdir, String(i + 1).padStart(4, "0") + ".png"));
        }
        encode(fdir, path.join(out, `${label}.turntable.mp4`));
        const row = [0, 0.25, 0.5, 0.75].map((f) => path.join(fdir, String(Math.floor(f * turnFrames) + 1).padStart(4, "0") + ".png"));
        const rowCopies = row.map((f, i) => { const c = still(`turn${i}`); fs.copyFileSync(f, c); return c; });
        manifest.push({ animal: animal.name, variant: variant.key, clip: "turntable", file: `${label}.turntable.mp4`, frames: turnFrames });

        for (const clip of clipNames) {
          const def = CLIPS[clip];
          const baked = bake({
            name: `clip.${clip}`,
            layers: def.layers.map((l) => animal.layers[l] ?? die(`${animal.name}: no ${l} animation`)),
            length: def.length,
            fps: FPS,
            context: def.context,
          });
          await setCamera(page, b, def.camera.yaw, def.camera.pitch);
          const restFile = path.join(work, "rest.png");
          await shot(page, box, restFile);
          const restHash = sha(restFile);
          const loaded = await page.evaluate(({ baked, clip }) => {
            Animator.loadFile({ name: "clip.json", path: "clip.json", content: JSON.stringify(baked) }, [`clip.${clip}`]);
            const a = Animation.all.find((x) => x.name === `clip.${clip}`);
            if (!a) return null;
            a.select();
            return { bones: Object.keys(a.animators).length, length: a.length };
          }, { baked, clip });
          if (!loaded || !loaded.bones) die(`${label}: animation ${clip} did not load into Blockbench`);
          fs.rmSync(fdir, { recursive: true, force: true });
          fs.mkdirSync(fdir, { recursive: true });
          const n = Math.round(def.length * FPS);
          const frameCount = opt.fast ? Math.min(n, 6) : n;
          const hashes = new Set();
          for (let i = 0; i < frameCount; i++) {
            await page.evaluate((t) => { Timeline.setTime(t); Animator.preview(); Preview.selected.render(); }, i / FPS);
            const f = path.join(fdir, String(i + 1).padStart(4, "0") + ".png");
            await shot(page, box, f);
            hashes.add(sha(f));
          }
          // A static pose (sit) must still differ from the unposed model.
          if (hashes.size === 1 && hashes.has(restHash)) die(`${label}: ${clip} left the model in its rest pose, the animation did not pose it`);
          if (hashes.size < 2 && !def.static) die(`${label}: ${clip} produced identical frames, the animation did not move the model`);
          encode(fdir, path.join(out, `${label}.${clip}.mp4`));
          const mid = path.join(fdir, String(Math.max(1, Math.floor(clip === "attack" ? n * 0.25 : n / 4)) + 1).padStart(4, "0") + ".png");
          const sc = still(clip);
          fs.copyFileSync(fs.existsSync(mid) ? mid : path.join(fdir, "0001.png"), sc);
          manifest.push({ animal: animal.name, variant: variant.key, clip, file: `${label}.${clip}.mp4`, frames: frameCount, distinct_frames: hashes.size });
          await page.evaluate(() => { Animation.selected?.unselect?.(); Animator.preview(); });
        }
        stills.push({ label, files: [...rowCopies, ...clipNames.map((c) => still(c))] });
        await page.close();
      }
      // contact sheet: one row per variant
      const inputs = stills.flatMap((s) => s.files);
      const cols = stills[0].files.length;
      const rows = stills.length;
      const filter = inputs.map((_, i) => `[${i}:v]scale=320:-1[s${i}]`).join(";") +
        ";" + inputs.map((_, i) => `[s${i}]`).join("") + `xstack=inputs=${inputs.length}:layout=${inputs.map((_, i) => `${(i % cols) * 320}_${Math.floor(i / cols) * 240}`).join("|")}`;
      ffmpeg([...inputs.flatMap((f) => ["-i", f]), "-filter_complex", filter, "-frames:v", "1", path.join(out, `${animal.name}.contact-sheet.png`)]);
      manifest.push({ animal: animal.name, clip: "contact-sheet", file: `${animal.name}.contact-sheet.png`, rows, cols });
    }
    ok = true;
  } finally {
    await ctx?.close();
    await browser.close();
    fs.rmSync(work, { recursive: true, force: true });
    // A recording from a failed first run may be partial or the wrong version.
    if (record && !ok) fs.rmSync(har, { force: true });
  }
  fs.writeFileSync(path.join(out, "manifest.json"), JSON.stringify({ run_id: id, blockbench: BLOCKBENCH_VERSION, fps: FPS, files: manifest }, null, 2));
  console.log(`done: ${manifest.length} files in ${out}`);
}

main().catch((e) => { console.error(`render failed: ${e.message}`); process.exit(1); });
