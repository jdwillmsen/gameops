// Takes screenshots of a camera page once it has meshed, for fidelity audits.
//
// usage: node shots.mjs --pw-dir DIR --chrome PATH --port N --out DIR
//          [--shots name:mode:radius[:x:z:zoom],...] [--w 1280] [--h 720]
//          [--atlas PREFIX] [--mesher FILE] [--y 63]
// --atlas serves PREFIX.json and PREFIX.png in place of the unit's atlas and
// --mesher a local mesher.js, so a change can be seen on the live scene before
// it ships. x and z (and --y) pin the viewpoint to a world position
// (the unit's own updates are ignored); zoom is wheel steps in orbit mode.
import { createRequire } from 'node:module';
import { mkdirSync, readFileSync } from 'node:fs';

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
const require = createRequire(arg('pw-dir', process.cwd()) + '/');
const { chromium } = require('playwright-core');

const out = arg('out');
mkdirSync(out, { recursive: true });
const shots = arg('shots', 'first:first:4,third:third:4,orbit:orbit:6,top:top:6').split(',');
const browser = await chromium.launch({
  executablePath: arg('chrome'),
  args: ['--no-sandbox', '--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader'],
});
for (const spec of shots) {
  const [name, mode, radius, px = '', pz = '', zoom = '0'] = spec.split(':');
  const page = await browser.newPage({ viewport: { width: Number(arg('w', 1280)), height: Number(arg('h', 720)) } });
  const atlas = arg('atlas'), mesher = arg('mesher');
  if (atlas) {
    await page.route('**/v1/camera/atlas.json', r => r.fulfill({ contentType: 'application/json', body: readFileSync(`${atlas}.json`) }));
    await page.route('**/v1/camera/atlas.png', r => r.fulfill({ contentType: 'image/png', body: readFileSync(`${atlas}.png`) }));
  }
  if (mesher) await page.route('**/v1/camera/mesher.js', r => r.fulfill({ contentType: 'text/javascript', body: readFileSync(mesher) }));
  page.on('console', m => { if (process.env.SHOTS_DEBUG) console.error('console:', m.text()); }); page.on('pageerror', e => console.error('pageerror:', e.message));
  await page.goto(`http://127.0.0.1:${arg('port')}/camera?mode=${mode}&r=${radius}&hud=0${arg("q", "")}`);
  await page.waitForFunction(() => window.__stats && window.__stats.settledMs !== null, null, { timeout: 120000 }).catch(() => {});
  await page.waitForTimeout(2500);
  if (px !== '' || Number(zoom)) {
    // Pinned every frame because the unit's own position updates keep arriving.
    if (px !== '') await page.evaluate(([x, y, z]) => { const m = window.__dbg.me; setInterval(() => { m.x = x; m.y = y; m.z = z; }, 8); }, [Number(px), Number(arg('y', 63)), Number(pz)]);
    await page.mouse.move(Number(arg('w', 1280)) / 2, Number(arg('h', 720)) / 2);
    for (let i = 0; i < Number(zoom); i++) await page.mouse.wheel(0, -300);
    await page.waitForTimeout(3000);
  }
  await page.screenshot({ path: `${out}/${name}.png` });
  console.log(`${out}/${name}.png`);
  await page.close();
}
await browser.close();
