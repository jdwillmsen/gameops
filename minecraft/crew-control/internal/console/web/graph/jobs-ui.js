// The jobs view: one open job's DAG with the plain list of open jobs beside
// it. graph.js draws and interacts; this file owns jobs data — the signed-out
// and empty states, the stream updates (which patch in place, so a live state
// never re-lays out, moves focus or drops the operator's selection), the
// details panel and the deep link (?view=jobs&job=<id>).

import { jobList, jobRow, jobModel, pickJob, taskText, stepChip } from './jobs.js';
import { createDagGraph } from './graph.js';

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-')) e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const clock = iso => {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? '' : new Date(t).toLocaleTimeString(undefined, { hour12: false });
};

// The details panel may show a job older than today, so it carries the date
// and not just the time of day.
const at = iso => {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? '' : new Date(t).toLocaleString(undefined, { hour12: false });
};

// A deep link to a job outside the feed's newest 20 is fetched by id; a
// transient failure retries on this ladder, its last rung repeating.
const RETRY_MS = [1000, 2000, 4000, 8000, 15000, 30000];

// host: { jobs(), operator(), state(), sync(), api(method, path, body), setOperator(op) }
export function initJobGraph(host) {
  let mounted = false;
  let current = ''; // the job drawn now
  let waitingFor = ''; // the deep-linked job being fetched or reported missing
  let selectedStep = '';
  let seenWanted = '';
  let panel = null; // {key, dl, values} of the open details
  // Deep-linked jobs outside the feed: id -> {state, job, tries, timer}.
  // state is loading, retry (a transient failure), missing (404) or ready.
  const lookups = new Map();

  const comp = createDagGraph({
    label: 'Job graph',
    onSelect: id => openStep(id),
    onClose: () => closeDetails(),
  });
  comp.setLegend(['waiting', 'queued', 'running', 'done', 'failed', 'cancelled'].map(stepChip));

  const list = el('ul', { className: 'jv-rows' });
  const noRows = el('li', { className: 'dg-empty-row', textContent: 'no jobs yet' });
  const listBox = el('aside', { className: 'jv-list' },
    el('h2', { textContent: 'Jobs' }), list);
  const title = el('h2', { className: 'jv-title' });
  const headChip = el('span');
  const progress = el('span', { className: 'jv-progress' });
  const timing = el('span', { className: 'jv-timing' });
  const cancel = el('button', { type: 'button', className: 'jv-cancel', textContent: 'Cancel job', hidden: true });
  const cancelMsg = el('span', { className: 'jv-msg', role: 'status' });
  const empty = el('div', { className: 'dg-empty', role: 'status' });
  const shell = el('div', { className: 'jv' }, listBox,
    el('div', { className: 'jv-main' },
      el('header', { className: 'jv-head' }, title, headChip, progress, timing, cancel, cancelMsg), comp.root, empty));
  const root = el('section', { className: 'dg-view cc-root' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', 'Jobs');
  root.append(shell);

  // The feed with any fetched deep-linked job spliced in; the feed wins, so a
  // job that scrolls into the newest 20 is live again.
  const allJobs = () => {
    const feed = host.jobs();
    return [...feed, ...[...lookups.values()]
      .filter(e => e.state === 'ready' && !feed.some(j => j.id === e.job.id))
      .map(e => e.job)];
  };

  const modelOf = job => {
    const model = jobModel(job);
    for (const n of model.nodes) n.selected = n.id === selectedStep;
    return model;
  };

  function forgetLookups(except = '') {
    for (const [id, e] of lookups) {
      if (id === except) continue;
      clearTimeout(e.timer);
      lookups.delete(id);
    }
  }

  function clearGraph() {
    comp.setData({ nodes: [], edges: [], critical: [] });
    panel = null;
    comp.setContent(null);
  }

  function clearHead() {
    title.textContent = '';
    headChip.replaceChildren();
    progress.textContent = '';
    timing.textContent = '';
    cancel.hidden = true;
    cancelMsg.textContent = '';
  }

  function say(text) {
    empty.hidden = false;
    empty.textContent = text;
  }

  function update() {
    if (!mounted) return;
    const op = host.operator();
    shell.classList.toggle('signed-out', !op);
    if (!op) {
      forgetLookups();
      current = waitingFor = selectedStep = seenWanted = '';
      clearGraph();
      clearHead();
      buildList([]);
      say('Sign in to see jobs.');
      return;
    }
    const wanted = host.state().job || '';
    // Moving to another job drops what was learned about the last deep link.
    if (wanted !== seenWanted) {
      seenWanted = wanted;
      forgetLookups(wanted);
    }
    const jobs = allJobs();
    // Keep the graph on the job the operator is already reading while it is
    // still in the feed; only fall back to the newest open job when it leaves.
    // A named job that is not in hand is fetched, never swapped for another.
    const job = wanted ? jobs.find(j => j.id === wanted)
      : (jobs.find(j => j.id === current) || pickJob(jobs));
    waitingFor = wanted && !job ? wanted : '';
    const shown = job ? job.id : '';
    if (shown !== current) {
      current = shown;
      selectedStep = '';
      cancelMsg.textContent = '';
      panel = null;
      comp.setContent(null);
    }
    buildList(jobList(jobs, wanted));
    if (waitingFor) {
      clearGraph();
      clearHead();
      waitFor(waitingFor);
      return;
    }
    if (!job) {
      clearGraph();
      clearHead();
      say('no jobs yet — queue one from the Jobs panel (Shift-J)');
      return;
    }
    empty.hidden = true;
    head(job);
    comp.setData(modelOf(job));
    paintDetails(job, false);
  }

  function syncJobParam(id) {
    host.state().job = id || '';
    host.sync();
  }

  // ---- the job list: patched row by row, so focus on a row survives a tick ----

  const rowsById = new Map(); // id -> {li, btn, name, chip, count, reason}

  function makeRow(id) {
    const name = el('span', { className: 'jv-row-title' });
    const chip = el('span');
    const count = el('span', { className: 'jv-progress' });
    const reason = el('span', { className: 'jv-reason' });
    const btn = el('button', { type: 'button', className: 'jv-row' },
      el('div', { className: 'jv-row-main' }, name, chip, count), reason);
    btn.addEventListener('click', () => {
      syncJobParam(id);
      update();
    });
    return { li: el('li', { className: 'jv-li' }, btn), btn, name, chip, count, reason };
  }

  const setText = (node, text) => {
    if (node.textContent !== text) node.textContent = text;
  };

  function paintRow(job) {
    const row = jobRow(job);
    let r = rowsById.get(row.id);
    if (!r) rowsById.set(row.id, r = makeRow(row.id));
    const gone = job.fetching && lookups.get(job.id)?.state === 'missing';
    const word = row.chip && row.chip.word ? row.chip.word : (gone ? 'missing' : 'loading');
    const cls = row.chip && row.chip.word ? `dg-chip dg-${row.chip.tone}` : 'dg-chip';
    setText(r.name, gone ? 'that job is not available' : (row.title ?? 'loading…'));
    if (r.chip.className !== cls) r.chip.className = cls;
    setText(r.chip, word);
    setText(r.count, row.progress);
    r.count.hidden = !row.progress;
    setText(r.reason, row.reason);
    r.reason.hidden = !row.reason;
    r.btn.setAttribute('aria-pressed', String(row.id === (current || waitingFor)));
    return r.li;
  }

  function buildList(rows) {
    const items = rows.length ? rows.map(paintRow) : [noRows];
    const kept = new Set(rows.map(j => j.id));
    for (const id of rowsById.keys()) if (!kept.has(id)) rowsById.delete(id);
    const now = [...list.children];
    if (now.length === items.length && items.every((li, i) => now[i] === li)) return;
    // Moving a node blurs it, so hand focus back to the row that had it.
    const had = document.activeElement;
    list.replaceChildren(...items);
    if (had && list.contains(had) && document.activeElement !== had) had.focus({ preventScroll: true });
  }

  // ---- the header ----

  function head(job) {
    const model = jobModel(job);
    title.textContent = model.title;
    const chip = stepChip(job.state);
    headChip.replaceChildren(el('span', { className: `dg-chip dg-${chip.tone}`, textContent: chip.word }));
    const steps = job.steps || [];
    const done = steps.filter(s => s.state === 'done').length;
    progress.textContent = `${done}/${steps.length} steps`;
    timing.textContent = ` started ${clock(job.created)} · updated ${clock(job.updated)} · deadline ${clock(job.deadline)}`;
    cancel.hidden = !host.operator() || job.state !== 'running';
    cancel.onclick = async () => {
      cancel.disabled = true;
      cancelMsg.textContent = '';
      try {
        const r = await host.api('POST', `/v1/jobs/${encodeURIComponent(job.id)}/cancel`, {});
        if (r.status === 401) {
          cancelMsg.textContent = 'You were signed out.';
          host.setOperator?.(null);
          return;
        }
        cancelMsg.textContent = r.ok ? 'Cancelled.' : (r.data && r.data.error) || `Failed (${r.status}).`;
      } finally {
        cancel.disabled = false;
      }
    };
  }

  // ---- the details panel ----

  const detailRows = (job, step) => [
    ['Step', step.key],
    ['Task', taskText(step.template) || 'no task template'],
    ['Unit', step.unit || 'not pinned — a free unit picks it up when its dependencies finish'],
    ['State', stepChip(step.state).word],
    // Steps carry no timestamps of their own, so the panel dates the step by
    // the job's clock, which is the finest timing the API exposes.
    ['Job started', at(job.created) || '—'],
    ['Job updated', at(job.updated) || '—'],
    ['Deadline', at(job.deadline) || '—'],
    ['Work item', step.work_id ? step.work_id.slice(0, 8) : 'none yet'],
    ['Attempts', String(step.attempts ?? 0)],
    ['Reason', step.reason || '—'],
  ];

  function buildPanel(step, rows) {
    const dl = el('dl', { tabIndex: '0', 'data-autofocus': '' });
    const values = rows.map(([word, text]) => {
      const dd = el('dd', { textContent: text });
      dl.append(el('dt', { textContent: word }), dd);
      return dd;
    });
    const close = el('button', { type: 'button', textContent: 'x', title: 'Close details', 'aria-label': 'Close details' });
    close.addEventListener('click', () => closeDetails());
    panel = { key: step.key, dl, values };
    return el('div', { className: 'dg-details' },
      el('div', { className: 'dg-details-bar' }, el('h3', { textContent: step.key }), close), dl);
  }

  // Repaints the open details for the job as it is now. Only the values that
  // changed are written, into the same elements, so a data update neither
  // rebuilds the panel nor moves focus; `focus` is for the operator's own
  // action only.
  function paintDetails(job, focus) {
    const step = selectedStep && (job.steps || []).find(s => s.key === selectedStep);
    if (!step) {
      selectedStep = '';
      if (panel) {
        panel = null;
        comp.setContent(null);
      }
      return;
    }
    const rows = detailRows(job, step);
    if (!panel || panel.key !== step.key) {
      comp.setContent(buildPanel(step, rows), { focus });
      return;
    }
    rows.forEach(([, text], i) => setText(panel.values[i], text));
    if (focus) panel.dl.focus();
  }

  function repaint(focus) {
    const job = allJobs().find(j => j.id === current);
    if (!job) return;
    comp.setData(modelOf(job));
    paintDetails(job, focus);
  }

  function openStep(key) {
    selectedStep = key;
    repaint(true);
  }

  // Closing returns focus to the node the details were about, so a keyboard
  // user lands where they were. Reports whether anything was open.
  function closeDetails() {
    if (!selectedStep) return false;
    const back = selectedStep;
    selectedStep = '';
    panel = null;
    comp.setContent(null);
    repaint(false);
    comp.open(back);
    return true;
  }

  // ---- a deep link past the feed ----

  function waitFor(id) {
    let e = lookups.get(id);
    if (!e) {
      lookups.set(id, e = { state: 'loading', tries: 0, timer: null });
      fetchJob(id, e);
    }
    say(e.state === 'missing' ? 'job not found'
      : e.state === 'retry' ? 'could not load that job — trying again…' : 'loading that job…');
  }

  async function fetchJob(id, e) {
    const r = await host.api('GET', `/v1/jobs/${encodeURIComponent(id)}`);
    // The view moved on, or the session ended, while this was in flight.
    if (lookups.get(id) !== e) return;
    if (r.status === 401) {
      host.setOperator?.(null);
      return;
    }
    if (r.ok && r.data && r.data.id === id) {
      e.state = 'ready';
      e.job = r.data;
    } else if (r.status === 404) {
      e.state = 'missing';
    } else {
      // A 503, a dropped connection or an error page says nothing about the
      // job: ask again, a little later each time.
      e.state = 'retry';
      e.timer = setTimeout(() => {
        e.timer = null;
        fetchJob(id, e);
      }, RETRY_MS[Math.min(e.tries++, RETRY_MS.length - 1)]);
    }
    update();
  }

  return {
    mount(stage) {
      if (mounted) return;
      mounted = true;
      stage.replaceChildren(root);
      comp.connect();
      update();
    },
    unmount() {
      mounted = false;
      forgetLookups();
      current = waitingFor = selectedStep = seenWanted = '';
      clearGraph();
      buildList([]);
      comp.disconnect();
      if (root.parentNode) root.parentNode.removeChild(root);
    },
    onJobs: update,
    onOperator: update,
    // The view's own keys: Esc drops back to the plain overview. The graph's
    // node keys are its own and never reach here, since they are handled on
    // the focused node before the console translates anything.
    key(ev) {
      if (ev.key !== 'Escape' || !mounted || !host.operator()) return false;
      // Escape is layered: the details close first, the job selection second.
      if (closeDetails()) {
        ev.preventDefault();
        return true;
      }
      if (host.state().job) {
        syncJobParam('');
        update();
        ev.preventDefault();
        return true;
      }
      return false;
    },
  };
}
