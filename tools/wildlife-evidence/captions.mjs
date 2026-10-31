// Turns a run's timeline.jsonl into captions for one clip. The recorder burns
// them into the MP4 and writes the same text beside it as .srt.
//
// A timeline line is {t_ms, source, scenario, event, ...}; t_ms counts from the
// start of the run. `originMs` is the t_ms the clip's first frame falls on.

const MIN_MS = 1500;
const MAX_MS = 6000;

export function parseTimeline(text) {
  const out = [];
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    try {
      const e = JSON.parse(line);
      if (Number.isFinite(e?.t_ms) && typeof e.event === 'string') out.push(e);
    } catch {
      // A half-written last line of a run still in progress.
    }
  }
  return out.sort((a, b) => a.t_ms - b.t_ms);
}

export function describe(e) {
  const { t_ms, source, scenario, event, ...rest } = e;
  const detail = Object.entries(rest)
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`)
    .join(' ');
  return `[${source ?? '?'}] ${event}${detail ? ' ' + detail : ''}`;
}

// captions picks the events of a scenario (or all, if none is named) that land
// inside the clip and gives each a start and end in clip milliseconds.
export function captions(events, { scenario, originMs = 0, durationMs }) {
  const mine = events.filter(e => !scenario || !e.scenario || e.scenario === scenario);
  const inClip = mine
    .map(e => ({ at: e.t_ms - originMs, text: describe(e) }))
    .filter(c => c.at < durationMs && c.at > -MAX_MS);
  return inClip.map((c, i) => {
    const start = Math.max(0, c.at);
    const end = Math.min(durationMs, Math.max(start + MIN_MS, Math.min(inClip[i + 1]?.at ?? Infinity, start + MAX_MS)));
    return { start, end, text: c.text };
  });
}

const pad = (n, w = 2) => String(n).padStart(w, '0');
function stamp(ms, sep) {
  const h = Math.floor(ms / 3600000), m = Math.floor(ms / 60000) % 60, s = Math.floor(ms / 1000) % 60;
  return `${pad(h)}:${pad(m)}:${pad(s)}${sep}${pad(Math.floor(ms % 1000), 3)}`;
}

export function toSrt(caps) {
  return caps.map((c, i) => `${i + 1}\n${stamp(c.start, ',')} --> ${stamp(c.end, ',')}\n${c.text}\n`).join('\n');
}

const assTime = ms => {
  const h = Math.floor(ms / 3600000), m = Math.floor(ms / 60000) % 60, s = Math.floor(ms / 1000) % 60;
  return `${h}:${pad(m)}:${pad(s)}.${pad(Math.floor((ms % 1000) / 10))}`;
};
const assText = s => s.replace(/[{}\\]/g, ' ').replace(/\n/g, '\\N');

// toAss lays out a permanent banner (what the picture is) at the top, an
// optional title and the timeline captions at the bottom. ASS rather than SRT
// for the burn-in, because it needs no font file and carries the styling.
export function toAss(caps, { width, height, banner, title, durationMs }) {
  const fs = Math.round(height / 28), bs = Math.round(height / 38);
  const head = `[Script Info]
ScriptType: v4.00+
PlayResX: ${width}
PlayResY: ${height}
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Cap,DejaVu Sans,${fs},&H00FFFFFF,&H000000FF,&H00000000,&H99000000,0,0,0,0,100,100,0,0,3,2,0,2,20,20,18,1
Style: Banner,DejaVu Sans,${bs},&H0000E5FF,&H000000FF,&H00000000,&H99000000,1,0,0,0,100,100,0,0,3,2,0,8,20,20,8,1
Style: Title,DejaVu Sans,${Math.round(fs * 1.15)},&H00FFFFFF,&H000000FF,&H00000000,&H99000000,1,0,0,0,100,100,0,0,3,2,0,7,20,20,${Math.round(bs * 2.6)},1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`;
  const lines = [`Dialogue: 0,${assTime(0)},${assTime(durationMs)},Banner,,0,0,0,,${assText(banner)}`];
  if (title) lines.push(`Dialogue: 0,${assTime(0)},${assTime(durationMs)},Title,,0,0,0,,${assText(title)}`);
  for (const c of caps) lines.push(`Dialogue: 1,${assTime(c.start)},${assTime(c.end)},Cap,,0,0,0,,${assText(c.text)}`);
  return head + lines.join('\n') + '\n';
}
