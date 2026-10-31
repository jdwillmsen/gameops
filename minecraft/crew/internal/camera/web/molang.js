// A small evaluator for the Molang expressions resource packs use to animate
// bones: arithmetic, comparisons, && || ?:, math.* functions and
// query.* / variable.* reads. No code is ever generated from the text; an
// expression is parsed into closures, and anything unrecognised is an error
// (compile returns null) or reads as 0, so a pack cannot run script in the page.

const DEG = Math.PI / 180;

// Molang's trigonometry is in degrees.
const FUNCS = {
  'math.sin': x => Math.sin(x * DEG),
  'math.cos': x => Math.cos(x * DEG),
  'math.abs': Math.abs,
  'math.sqrt': Math.sqrt,
  'math.floor': Math.floor,
  'math.ceil': Math.ceil,
  'math.round': Math.round,
  'math.trunc': Math.trunc,
  'math.min': Math.min,
  'math.max': Math.max,
  'math.pow': Math.pow,
  'math.mod': (a, b) => (b === 0 ? 0 : a % b),
  'math.clamp': (v, lo, hi) => Math.min(Math.max(v, lo), hi),
  'math.lerp': (a, b, t) => a + (b - a) * t,
};

const MAX_DEPTH = 32;

function tokenize(src) {
  const out = [];
  const re = /\s*(?:(\d+\.?\d*|\.\d+)|([A-Za-z_][A-Za-z0-9_.]*)|(&&|\|\||==|!=|<=|>=|[-+*/%<>!?:(),]))/y;
  let at = 0;
  while (at < src.length) {
    re.lastIndex = at;
    const m = re.exec(src);
    if (!m) {
      if (/^\s*$/.test(src.slice(at))) break;
      return null;
    }
    at = re.lastIndex;
    if (m[1] !== undefined) out.push({ n: parseFloat(m[1]) });
    else if (m[2] !== undefined) out.push({ id: m[2].toLowerCase() });
    else out.push({ op: m[3] });
  }
  return out;
}

// compile returns a function of a context ({query, variable}) or null for text
// that is not a valid expression.
export function compile(src) {
  if (typeof src !== 'string' || src.length > 512) return null;
  const toks = tokenize(src);
  if (!toks || toks.length === 0) return null;
  let i = 0;
  const peek = () => toks[i];
  const op = o => toks[i]?.op === o && (i++, true);
  const fail = () => { throw new SyntaxError('bad expression'); };

  function ternary(depth) {
    const c = or(depth);
    if (!op('?')) return c;
    const a = ternary(depth + 1);
    if (!op(':')) fail();
    const b = ternary(depth + 1);
    return ctx => (c(ctx) ? a(ctx) : b(ctx));
  }
  function binary(next, ops) {
    return depth => {
      let l = next(depth);
      for (;;) {
        const t = peek();
        const f = t?.op !== undefined && ops[t.op];
        if (!f) return l;
        i++;
        const r = next(depth), lf = l;
        l = ctx => f(lf(ctx), rf(r, ctx));
      }
    };
  }
  const rf = (r, ctx) => r(ctx);
  const or = depth => {
    let l = and(depth);
    while (op('||')) { const r = and(depth), lf = l; l = ctx => (lf(ctx) || r(ctx) ? 1 : 0); }
    return l;
  };
  const and = depth => {
    let l = equality(depth);
    while (op('&&')) { const r = equality(depth), lf = l; l = ctx => (lf(ctx) && r(ctx) ? 1 : 0); }
    return l;
  };
  const equality = binary(depth => compare(depth), { '==': (a, b) => +(a === b), '!=': (a, b) => +(a !== b) });
  const compare = binary(depth => sum(depth), { '<': (a, b) => +(a < b), '>': (a, b) => +(a > b), '<=': (a, b) => +(a <= b), '>=': (a, b) => +(a >= b) });
  const sum = binary(depth => product(depth), { '+': (a, b) => a + b, '-': (a, b) => a - b });
  const product = binary(depth => unary(depth), { '*': (a, b) => a * b, '/': (a, b) => (b === 0 ? 0 : a / b), '%': (a, b) => (b === 0 ? 0 : a % b) });

  function unary(depth) {
    if (depth > MAX_DEPTH) fail();
    if (op('-')) { const e = unary(depth + 1); return ctx => -e(ctx); }
    if (op('+')) return unary(depth + 1);
    if (op('!')) { const e = unary(depth + 1); return ctx => (e(ctx) ? 0 : 1); }
    return primary(depth);
  }
  function primary(depth) {
    const t = toks[i++];
    if (!t) fail();
    if (t.n !== undefined) return () => t.n;
    if (t.op === '(') {
      const e = ternary(depth + 1);
      if (!op(')')) fail();
      return e;
    }
    if (t.id === undefined) fail();
    if (op('(')) {
      // Own keys only: "__proto__" and "constructor" are not functions of ours.
      const f = Object.hasOwn(FUNCS, t.id) ? FUNCS[t.id] : null;
      if (!f) fail();
      const args = [];
      if (!op(')')) {
        do args.push(ternary(depth + 1)); while (op(','));
        if (!op(')')) fail();
      }
      return ctx => f(...args.map(a => a(ctx)));
    }
    if (t.id === 'true') return () => 1;
    if (t.id === 'false') return () => 0;
    if (t.id === 'math.pi') return () => Math.PI;
    const dot = t.id.indexOf('.');
    const scope = t.id.slice(0, dot), name = t.id.slice(dot + 1);
    if (scope !== 'query' && scope !== 'variable' && scope !== 'q' && scope !== 'v') fail();
    const group = scope[0] === 'q' ? 'query' : 'variable';
    return ctx => {
      const v = ctx?.[group]?.[name];
      return typeof v === 'number' && Number.isFinite(v) ? v : (v === true ? 1 : 0);
    };
  }

  try {
    const e = ternary(0);
    return i === toks.length ? e : null;
  } catch {
    return null;
  }
}

// compileAnimations turns a model's animation set into what the page runs each
// frame: for every entry of the animate script, a condition and the bones it
// moves. Entries whose text does not parse are dropped.
export function compileAnimations(set) {
  const runs = [];
  for (const r of set?.run ?? []) {
    const def = set.defs?.[r.n];
    if (!def) continue;
    const when = r.w ? compile(r.w) : null;
    if (r.w && !when) continue;
    const bones = new Map();
    for (const [name, b] of Object.entries(def.bones ?? {})) {
      const vec = v => { const f = v?.map(compile); return f && f.every(Boolean) ? f : null; };
      const rot = vec(b.r), pos = vec(b.p);
      if (rot || pos) bones.set(name.toLowerCase(), { rot, pos });
    }
    runs.push({ name: r.n, when, bones });
  }
  return runs;
}

// pose adds every active animation's rotation and position to each bone and
// returns Map(lowercase bone name -> {rot: [x, y, z] degrees, pos: [x, y, z]}).
export function pose(runs, ctx) {
  const out = new Map();
  for (const run of runs) {
    if (run.when && !run.when(ctx)) continue;
    for (const [name, b] of run.bones) {
      let p = out.get(name);
      if (!p) out.set(name, p = { rot: [0, 0, 0], pos: [0, 0, 0] });
      if (b.rot) for (let k = 0; k < 3; k++) p.rot[k] += b.rot[k](ctx);
      if (b.pos) for (let k = 0; k < 3; k++) p.pos[k] += b.pos[k](ctx);
    }
  }
  return out;
}
