// Bakes the pack's Molang animation expressions into plain Bedrock keyframes
// so Blockbench's own animation loader and sampler pose the bones.
const rad = Math.PI / 180;
// Molang trig is in degrees.
const math = {
  cos: (d) => Math.cos(d * rad),
  sin: (d) => Math.sin(d * rad),
  clamp: (v, lo, hi) => Math.min(hi, Math.max(lo, v)),
  abs: Math.abs,
  min: Math.min,
  max: Math.max,
  pi: Math.PI,
};

const compiled = new Map();
function evalExpr(expr, ctx) {
  if (typeof expr === "number") return expr;
  let fn = compiled.get(expr);
  if (!fn) {
    fn = new Function("math", "query", "variable", `"use strict"; return (${expr});`);
    compiled.set(expr, fn);
  }
  const v = fn(math, ctx.query, ctx.variable);
  if (!Number.isFinite(v)) throw new Error(`animation expression is not finite: ${expr}`);
  return v;
}

const CHANNELS = ["rotation", "position", "scale"];

// Sums every layer's channels per bone, the way the game layers the entity's
// `animate` list, at `frames` evenly spaced times over `length` seconds.
export function bake({ name, layers, length, fps, context }) {
  const frames = Math.round(length * fps);
  const bones = {};
  for (let i = 0; i < frames; i++) {
    const t = i / fps;
    const ctx = context(t);
    for (const layer of layers) {
      for (const [bone, channels] of Object.entries(layer.bones ?? {})) {
        for (const ch of CHANNELS) {
          const spec = channels[ch];
          if (!spec) continue;
          const vec = Array.isArray(spec) ? spec : [spec, spec, spec];
          const v = vec.map((e) => evalExpr(e, ctx));
          const kf = ((bones[bone] ??= {})[ch] ??= {});
          const key = t.toFixed(4);
          kf[key] = kf[key] ? kf[key].map((x, k) => x + v[k]) : v;
        }
      }
    }
  }
  return {
    format_version: "1.8.0",
    animations: { [name]: { loop: true, animation_length: frames / fps, bones } },
  };
}

// What the game feeds each animation: the clips below pin the runtime queries
// to plain values, so a clip shows the animation, not a locomotion simulation.
export const CLIPS = {
  walk: {
    layers: ["walk", "idle"],
    length: 1.6, // two leg cycles: the tail swings at half the leg frequency
    context: (t) => ({
      query: { modified_distance_moved: t * 15 / 0.8, modified_move_speed: 1, life_time: t, target_x_rotation: 0, target_y_rotation: 0 },
      variable: { attack_time: 0 },
    }),
    camera: { yaw: 90, pitch: 8 },
  },
  idle: {
    layers: ["idle"],
    length: 3, // one tail sway period
    context: (t) => ({
      query: { modified_distance_moved: 0, modified_move_speed: 0, life_time: t, target_x_rotation: 12 * Math.sin(t * 120 * rad), target_y_rotation: 35 * Math.sin(t * 120 * rad) },
      variable: { attack_time: 0 },
    }),
    camera: { yaw: 35, pitch: 12 },
  },
  sit: {
    static: true, // the sit pose is constant; only the idle tail sway moves
    layers: ["sit", "idle"],
    length: 3,
    context: (t) => ({
      query: { modified_distance_moved: 0, modified_move_speed: 0, life_time: t, target_x_rotation: 0, target_y_rotation: 0 },
      variable: { attack_time: 0 },
    }),
    camera: { yaw: 70, pitch: 10 },
  },
  attack: {
    layers: ["attack", "idle"],
    length: 1,
    context: (t) => ({
      query: { modified_distance_moved: 0, modified_move_speed: 0, life_time: t, target_x_rotation: 0, target_y_rotation: 0 },
      variable: { attack_time: t },
    }),
    camera: { yaw: 70, pitch: 10 },
  },
};
