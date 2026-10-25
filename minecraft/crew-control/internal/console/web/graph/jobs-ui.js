// The jobs view: one open job's DAG with the plain list of open jobs beside
// it. graph.js draws and interacts; this file owns jobs data — the signed-out
// and empty states, the stream updates (which patch in place through the
// component's structure diff, so a live state never re-lays out), the details
// panel and the deep link (?view=jobs&job=<id>).

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

// host: { jobs(), operator(), state(), sync(), api(method, path, body), setOperator(op) }
export function initJobGraph(host) {
  let mounted = false;
  const extra = new Map(); // id -> job fetched for a deep link past the feed

  const comp = createDagGraph({
    label: 'Job graph',
    onSelect: id => showStep(id),
    onClose: () => {
      selectedStep = '';
      comp.setContent(null);
    },
  });
  comp.setLegend(['waiting', 'queued', 'running', 'done', 'failed', 'cancelled'].map(stepChip));

  const list = el('ul', { className: 'jv-rows' });
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

  // Normalize the list with any deep-linked job spliced in.
  const allJobs = () => [...host.jobs(), ...extra.values()
    .filter(j => !host.jobs().some(h => h.id === j.id))];

  let current = '';
  let selectedStep = '';
  // A deep link can name a job the server no longer has. Fetch it once and
  // remember the refusal instead of asking again on every stream update.
  const broken = new Set();

  function clearGraph() {
    comp.setData({ nodes: [], edges: [], critical: [] });
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

  function update() {
    const op = host.operator();
    if (mounted) shell.classList.toggle('signed-out', !op);
    if (!op) {
      current = '';
      selectedStep = '';
      clearGraph();
      clearHead();
      buildList([]);
      empty.hidden = false;
      empty.textContent = 'Sign in to see jobs.';
      return;
    }
    const jobs = allJobs();
    const wanted = host.state().job || '';
    // Keep the graph on the job the operator is already reading while it is
    // still in the feed; only fall back to the newest open job when it leaves.
    const job = wanted ? pickJob(jobs, wanted) : (jobs.find(j => j.id === current) || pickJob(jobs, wanted));
    buildList(jobList(jobs, wanted));
    if (!job) {
      current = '';
      selectedStep = '';
      clearGraph();
      clearHead();
      empty.hidden = false;
      empty.textContent = 'no jobs yet — queue one from the Jobs panel (Shift-J)';
      return;
    }
    if (job.fetching) {
      clearGraph();
      clearHead();
      empty.hidden = false;
      empty.textContent = broken.has(job.id) ? 'that job is not available.' : 'loading that job…';
      if (!broken.has(job.id)) scheduleFetch(job.id);
      return;
    }
    empty.hidden = true;
    if (current !== job.id) {
      current = job.id;
      selectedStep = '';
      cancelMsg.textContent = '';
      comp.setContent(null);
    }
    head(job);
    comp.setData(jobModel(job));
    if (selectedStep) showStep(selectedStep);
  }

  function syncJobParam(id) {
    host.state().job = id || '';
    host.sync();
  }

  function buildList(rows) {
    // Rebuild on every update: at most twenty rows, and the selection must
    // track the stream's changes anyway.
    list.replaceChildren(...(rows.length ? rows.map(renderRow) : [el('li', { className: 'dg-empty-row', textContent: 'no jobs yet' })]));
  }

  function renderRow(job) {
    const row = jobRow(job);
    const picked = row.id === current;
    const gone = job.fetching && broken.has(job.id);
    const chip = row.chip && row.chip.word
      ? el('span', { className: `dg-chip dg-${row.chip.tone}`, textContent: row.chip.word })
      : el('span', { className: 'dg-chip', textContent: gone ? 'missing' : 'loading' });
    const titleText = gone ? 'that job is not available' : (row.title ?? 'loading…');
    const box = el('div', { className: 'jv-row-main' },
      el('span', { className: 'jv-row-title', textContent: titleText }), chip,
      row.progress ? el('span', { className: 'jv-progress', textContent: row.progress }) : '');
    const btn = el('button', { type: 'button', className: 'jv-row' }, box);
    btn.setAttribute('aria-pressed', String(picked));
    if (row.reason) btn.append(el('span', { className: 'jv-reason', textContent: row.reason }));
    btn.addEventListener('click', () => {
      syncJobParam(row.id);
      update();
    });
    return el('li', { className: 'jv-li' }, btn);
  }

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

  function showStep(stepKey) {
    const job = allJobs().find(j => j.id === current);
    const step = job && (job.steps || []).find(s => s.key === stepKey);
    selectedStep = step ? stepKey : '';
    if (!step) {
      comp.setContent(null);
      return;
    }
    const term = (word, text) => [el('dt', { textContent: word }), el('dd', { textContent: text })];
    const dl = el('dl', { tabIndex: '0', 'data-autofocus': '' });
    dl.append(
      ...term('Step', step.key),
      ...term('Task', taskText(step.template) || 'no task template'),
      ...term('Unit', step.unit || 'not pinned — a free unit picks it up when its dependencies finish'),
      ...term('State', stepChip(step.state).word),
      // Steps carry no timestamps of their own, so the panel dates the step by
      // the job's clock, which is the finest timing the API exposes.
      ...term('Job started', at(job.created) || '—'),
      ...term('Job updated', at(job.updated) || '—'),
      ...term('Deadline', at(job.deadline) || '—'),
      ...term('Work item', step.work_id ? step.work_id.slice(0, 8) : 'none yet'),
      ...term('Attempts', String(step.attempts ?? 0)),
      ...term('Reason', step.reason || '—'),
    );
    const close = el('button', { type: 'button', textContent: 'x', title: 'Close details', 'aria-label': 'Close details' });
    close.addEventListener('click', () => {
      selectedStep = '';
      comp.setContent(null);
    });
    comp.setContent(el('div', { className: 'dg-details' },
      el('div', { className: 'dg-details-bar' }, el('h3', { textContent: step.key }), close), dl));
  }

  // A deep link names a job past the feed's newest 20 (or before the first
  // jobs event arrives); fetch it once and cache it.
  const infetch = new Set();
  function scheduleFetch(id) {
    if (infetch.has(id) || broken.has(id)) return;
    infetch.add(id);
    host.api('GET', `/v1/jobs/${encodeURIComponent(id)}`).then(r => {
      infetch.delete(id);
      if (r.status === 401) {
        host.setOperator?.(null);
        return;
      }
      if (r.ok && r.data && r.data.id) {
        broken.delete(id);
        extra.set(r.data.id, r.data);
        update();
      } else {
        broken.add(id);
        update();
      }
    });
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
      comp.disconnect();
      if (root.parentNode) root.parentNode.removeChild(root);
    },
    onJobs() {
      if (mounted) update();
    },
    onOperator() {
      if (mounted) update();
    },
    // The view's own keys: Esc drops back to the plain overview. The graph's
    // node keys are its own and never reach here, since they are handled on
    // the focused node before the console translates anything.
    key(ev) {
      if (ev.key !== 'Escape' || !mounted || !host.operator()) return false;
      // Escape is layered: the details close first, the job selection second.
      // Without the early return, update() reopens details from selectedStep.
      if (selectedStep) {
        selectedStep = '';
        comp.setContent(null);
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
