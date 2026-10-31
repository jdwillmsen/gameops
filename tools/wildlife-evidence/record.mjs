// Records a crew unit's camera page to an H.264 MP4 with the run's timeline as
// captions. Chrome (SwiftShader, no GPU) draws the page on a private Xvfb
// display and ffmpeg grabs that display, so the video is the page as it ran,
// at the frame rate the software renderer really reached; nothing is replayed
// or interpolated afterwards.
//
// This is the crew camera's software re-render of packets, not the pixels of a
// real Bedrock client, and the banner burned into every frame says so.
//
// usage: record.sh --out FILE.mp4 [--unit URL] [--seconds N] [options]   (--help)
import { spawn, spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { captions, parseTimeline, toAss, toSrt } from './captions.mjs';

const USAGE = `usage: record.sh --out FILE.mp4 [options]
  --unit URL          the unit's status address (default http://127.0.0.1:18151)
  --seconds N         length of the clip, 2 to 600 (default 20)
  --follow TYPE|ID    centre the orbit on this entity (gameops:tiger, tiger, or a runtime id)
  --mode MODE         first | third | orbit | top (default orbit)
  --az DEG --el DEG --dist BLOCKS   orbit angle, elevation and distance
  --spin DEG_PER_S    turn around the followed entity
  --sit               draw pack animals sitting whatever the server said (labelled in the video)
  --time TICKS|live   pin the page's clock (default 6000, noon: entities are unlit at night); live follows the server
  --radius N          sub-chunk radius the page asks for (default 6)
  --width W --height H --fps N      capture size and rate (default 1280x720 at 30)
  --title TEXT        a title burned into the top left
  --timeline FILE     timeline.jsonl whose events become captions
  --scenario ID       only the events of this scenario (and those with none)
  --run-epoch-ms MS   wall-clock time of the timeline's t_ms 0, to line captions up with the video;
                      without it the timeline is taken as starting with the clip
  --drive CMD         run this shell command DELAY seconds into the clip (--drive-at, default 2)
  --pw-dir DIR        where playwright-core resolves from (or set PW_DIR)
  --chrome PATH       Chrome binary (default /usr/bin/google-chrome)
  --wait-s N          how long to wait for the followed entity to appear (default 60)
Writes FILE.mp4, FILE.srt and FILE.json (what was measured). Prints one JSON line.`;

const flags = new Set(['sit', 'help']);
function parseArgs(argv) {
  const a = {};
  for (let i = 0; i < argv.length; i++) {
    const m = /^--(.+)$/.exec(argv[i]);
    if (!m) throw new Error(`unexpected argument: ${argv[i]}`);
    if (flags.has(m[1])) a[m[1]] = true;
    else if (i + 1 < argv.length) a[m[1]] = argv[++i];
    else throw new Error(`--${m[1]} needs a value`);
  }
  return a;
}

const fail = msg => { console.error(JSON.stringify({ error: msg })); process.exit(1); };
const sleep = ms => new Promise(r => setTimeout(r, ms));
const BANNER = 'Crew camera: software re-render from game packets (SwiftShader, no GPU), not a real client\'s pixels';

let args;
try { args = parseArgs(process.argv.slice(2)); } catch (e) { console.error(e.message + '\n' + USAGE); process.exit(2); }
if (args.help) { console.log(USAGE); process.exit(0); }
if (!args.out) { console.error('--out is required\n' + USAGE); process.exit(2); }

const num = (name, def, lo, hi) => {
  const v = args[name] === undefined ? def : Number(args[name]);
  if (!Number.isFinite(v) || v < lo || v > hi) { console.error(`--${name} must be a number from ${lo} to ${hi}\n`); process.exit(2); }
  return v;
};
const W = num('width', 1280, 320, 3840), H = num('height', 720, 240, 2160), FPS = num('fps', 30, 5, 60);
const SECONDS = num('seconds', 20, 2, 600), RADIUS = num('radius', 6, 1, 32), WAIT_S = num('wait-s', 60, 1, 600);
const DRIVE_AT = num('drive-at', 2, 0, 600);
const unit = (args.unit ?? 'http://127.0.0.1:18151').replace(/\/$/, '');
if (!/^https?:\/\/[A-Za-z0-9.\-:\[\]]+$/.test(unit)) fail('--unit must be an http(s) origin');
const out = resolve(args.out);
if (!out.endsWith('.mp4')) fail('--out must end in .mp4');
mkdirSync(dirname(out), { recursive: true });
const base = out.slice(0, -4);
const chromePath = args.chrome ?? '/usr/bin/google-chrome';

for (const bin of ['ffmpeg', 'ffprobe', 'Xvfb']) {
  if (spawnSync('which', [bin]).status !== 0) fail(`${bin} is not installed`);
}
if (!existsSync(chromePath)) fail(`no Chrome at ${chromePath}`);

function loadPlaywright() {
  const roots = [args['pw-dir'], process.env.PW_DIR, process.cwd(), dirname(new URL(import.meta.url).pathname)].filter(Boolean);
  for (const r of roots) {
    try { return createRequire(resolve(r) + '/')('playwright-core'); } catch { /* try the next place */ }
  }
  fail('playwright-core was not found; pass --pw-dir DIR (a directory with node_modules/playwright-core) or set PW_DIR');
}
const { chromium } = loadPlaywright();

const work = mkdtempSync(join(tmpdir(), 'wildlife-rec-'));
const children = [];
const stopAll = () => { for (const c of children) { try { c.kill('SIGKILL'); } catch { /* gone */ } } };
process.on('exit', stopAll);
for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, () => { stopAll(); process.exit(130); });

// A private display on the first free number, so a run never touches the
// desktop and two recordings can run side by side.
async function startXvfb() {
  for (let n = 90; n < 140; n++) {
    if (existsSync(`/tmp/.X11-unix/X${n}`) || existsSync(`/tmp/.X${n}-lock`)) continue;
    const x = spawn('Xvfb', [`:${n}`, '-screen', '0', `${W}x${H}x24`, '-nolisten', 'tcp'], { stdio: 'ignore' });
    children.push(x);
    for (let i = 0; i < 50; i++) {
      if (existsSync(`/tmp/.X11-unix/X${n}`)) return `:${n}`;
      if (x.exitCode !== null) break;
      await sleep(100);
    }
  }
  return fail('could not start Xvfb on a free display');
}

const display = await startXvfb();
const q = new URLSearchParams({ mode: args.mode ?? 'orbit', r: String(RADIUS), hud: '0' });
for (const [k, p] of [['follow', 'follow'], ['az', 'az'], ['el', 'el'], ['dist', 'dist'], ['spin', 'spin']]) if (args[p] !== undefined) q.set(k, args[p]);
if (args.sit) q.set('sit', '1');
if (args.time !== 'live') q.set('time', String(num('time', 6000, 0, 24000)));
const pageUrl = `${unit}/camera?${q}`;
// An app window has no tabs or address bar, so the grab is the page alone.
const profile = join(work, 'profile');
const context = await chromium.launchPersistentContext(profile, {
  executablePath: chromePath,
  headless: false,
  env: { ...process.env, DISPLAY: display },
  noViewport: true,
  args: [
    '--no-sandbox', '--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader',
    `--app=${pageUrl}`, '--test-type', '--window-position=0,0', `--window-size=${W},${H}`,
    '--no-first-run', '--disable-infobars', '--noerrdialogs', '--disable-features=TranslateUI',
  ],
});
const browser = context.browser();
const page = context.pages()[0] ?? await context.newPage();
const pageErrors = [];
page.on('pageerror', e => pageErrors.push(e.message));

await page.waitForLoadState('load', { timeout: 30000 }).catch(e => fail(`cannot open ${unit}/camera: ${e.message.split('\n')[0]}`));
await page.waitForFunction(() => window.__stats?.firstFrameMs !== null && window.__stats?.firstFrameMs !== undefined, null, { timeout: 120000 })
  .catch(() => fail('the camera page never drew a first frame (is CAMERA_ENABLED on and the unit in the world?)'));

if (args.follow) {
  const f = args.follow;
  const ok = await page.waitForFunction(f => [...window.__dbg.entMeshes.values()].some(i => i.cur && (String(i.id) === f || i.type === f || i.type.endsWith(':' + f))), f, { timeout: WAIT_S * 1000 })
    .then(() => true, () => false);
  if (!ok) {
    const seen = await page.evaluate(() => [...new Set([...window.__dbg.entMeshes.values()].map(i => i.type))]);
    fail(`no entity matching "${f}" came into view within ${WAIT_S}s; in view: ${seen.join(', ') || 'nothing'}`);
  }
}
// Models, textures and the first chunks settle before the clip starts, so the
// first seconds are not a pop-in.
await page.waitForFunction(() => window.__stats.settledMs !== null && window.__dbg.entMeshes.size >= 0, null, { timeout: 60000 }).catch(() => {});
await page.waitForTimeout(1500);

const raw = join(work, 'raw.mkv');
const ff = spawn('ffmpeg', ['-hide_banner', '-loglevel', 'info', '-y', '-f', 'x11grab', '-framerate', String(FPS), '-video_size', `${W}x${H}`,
  '-draw_mouse', '0', '-i', `${display}.0`, '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '17', '-pix_fmt', 'yuv420p', raw], { stdio: ['pipe', 'ignore', 'pipe'] });
children.push(ff);
let ffLog = '';
const firstFrame = new Promise(res => ff.stderr.on('data', d => { ffLog = (ffLog + d).slice(-4000); if (/frame=\s*\d+/.test(ffLog)) res(); }));
await Promise.race([firstFrame, sleep(10000)]);
const startEpoch = Date.now();

const frames0 = await page.evaluate(() => window.__stats.frames);
const samples = [];
let driven = null;
const t0 = performance.now();
let last = { at: t0, frames: frames0 };
while (performance.now() - t0 < SECONDS * 1000) {
  await sleep(1000);
  const now = performance.now();
  if (args.drive && !driven && (now - t0) / 1000 >= DRIVE_AT) {
    driven = spawn('sh', ['-c', args.drive], { stdio: 'inherit' });
  }
  const frames = await page.evaluate(() => window.__stats.frames);
  samples.push((frames - last.frames) / ((now - last.at) / 1000));
  last = { at: now, frames };
}
const wallMs = performance.now() - t0;
const renderedFrames = last.frames - frames0;
ff.stdin.write('q');
await new Promise(res => { ff.on('exit', res); setTimeout(() => { ff.kill('SIGKILL'); res(); }, 15000); });
if (driven && driven.exitCode === null) await new Promise(res => { driven.on('exit', res); setTimeout(res, 5000); });
const stats = await page.evaluate(() => ({ ...window.__stats, entities: window.__dbg.entMeshes.size }));
const gl = await page.evaluate(() => {
  const c = document.createElement('canvas').getContext('webgl2') ?? document.createElement('canvas').getContext('webgl');
  const ext = c?.getExtension('WEBGL_debug_renderer_info');
  return ext ? c.getParameter(ext.UNMASKED_RENDERER_WEBGL) : 'unknown';
});
const version = browser.version();
await context.close();
stopAll();

if (!existsSync(raw)) fail(`ffmpeg wrote nothing: ${ffLog.split('\n').slice(-3).join(' | ')}`);

// Captions: the timeline's t_ms counts from the run's start; the clip began at
// startEpoch (the first grabbed frame, to within a frame or two).
const probe = JSON.parse(spawnSync('ffprobe', ['-v', 'error', '-count_frames', '-select_streams', 'v:0', '-show_entries', 'stream=nb_read_frames,avg_frame_rate,r_frame_rate:format=duration', '-of', 'json', raw], { encoding: 'utf8' }).stdout || '{}');
const durationMs = Math.round(Number(probe.format?.duration ?? SECONDS) * 1000);
let events = [];
let originMs = 0;
if (args.timeline) {
  if (!existsSync(args.timeline)) fail(`no timeline at ${args.timeline}`);
  events = parseTimeline(readFileSync(args.timeline, 'utf8'));
  if (args['run-epoch-ms']) originMs = startEpoch - Number(args['run-epoch-ms']);
}
const caps = captions(events, { scenario: args.scenario, originMs, durationMs });
const title = [args.title, args.sit ? '(sitting pose forced by the recorder: the server did not send the flag)' : ''].filter(Boolean).join('  ');
writeFileSync(`${base}.srt`, toSrt(caps));
const ass = join(work, 'c.ass');
writeFileSync(ass, toAss(caps, { width: W, height: H, banner: BANNER, title, durationMs }));
const enc = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-i', 'raw.mkv', '-vf', 'ass=c.ass', '-c:v', 'libx264', '-preset', 'medium', '-crf', '20',
  '-pix_fmt', 'yuv420p', '-movflags', '+faststart', '-an', out], { cwd: work, encoding: 'utf8' });
if (enc.status !== 0) fail(`encode failed: ${enc.stderr.split('\n').slice(0, 3).join(' | ')}`);

const final = JSON.parse(spawnSync('ffprobe', ['-v', 'error', '-select_streams', 'v:0', '-show_entries', 'stream=codec_name,profile,width,height,pix_fmt,avg_frame_rate,nb_frames:format=duration,size', '-of', 'json', out], { encoding: 'utf8' }).stdout);
const sorted = [...samples].sort((a, b) => a - b);
const mean = samples.reduce((s, v) => s + v, 0) / (samples.length || 1);
const metrics = {
  file: out,
  banner: BANNER,
  video: { codec: final.streams[0].codec_name, profile: final.streams[0].profile, pix_fmt: final.streams[0].pix_fmt, width: final.streams[0].width, height: final.streams[0].height,
    frames: Number(final.streams[0].nb_frames), duration_s: Number(final.format.duration), bytes: Number(final.format.size), capture_fps_target: FPS },
  render: {
    renderer: gl, browser: version,
    page_frames: renderedFrames, wall_s: +(wallMs / 1000).toFixed(2),
    fps_mean: +(renderedFrames / (wallMs / 1000)).toFixed(1),
    fps_min: +(sorted[0] ?? 0).toFixed(1), fps_p10: +(sorted[Math.floor(sorted.length * 0.1)] ?? 0).toFixed(1), fps_max: +(sorted.at(-1) ?? 0).toFixed(1),
    per_second: samples.map(v => +v.toFixed(1)),
    frame_work_ms_mean: +(stats.workMs / Math.max(stats.frames, 1)).toFixed(1), frame_work_ms_max: +stats.workMaxMs.toFixed(1),
    entities_in_view: stats.entities,
  },
  captions: caps.length, page_errors: pageErrors.slice(0, 5), mean_fps_samples: +mean.toFixed(1),
  url: pageUrl,
};
writeFileSync(`${base}.json`, JSON.stringify(metrics, null, 2) + '\n');
rmSync(work, { recursive: true, force: true });
console.log(JSON.stringify({ file: out, fps_rendered: metrics.render.fps_mean, fps_min: metrics.render.fps_min, size: `${metrics.video.width}x${metrics.video.height}`, seconds: metrics.video.duration_s, captions: caps.length }));
