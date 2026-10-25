import test from 'node:test';
import assert from 'node:assert/strict';
import { STEP_STATES, STEP_TONE, STEP_ICON, stepChip, isOpen, taskText, jobModel, jobList, pickJob, stepsDone, jobRow } from './jobs.js';

const job = over => ({ id: 'job-1', kind: 'plan', spec: { template: 'parallel-demo' }, state: 'running', ...over });

test('jobModel: one node per step keyed by the step key', () => {
  const m = jobModel(job({
    steps: [
      { key: 'mine', state: 'done', template: { template: 'gather_and_deposit', item: 'coal', qty: 4 } },
      { key: 'smelt', state: 'running', depends_on: ['mine'], template: { template: 'craft', item: 'furnace', count: 1 } },
    ],
  }));
  assert.deepEqual(m.nodes.map(n => n.id), ['mine', 'smelt']);
  assert.deepEqual(m.edges, [{ from: 'mine', to: 'smelt' }]);
  assert.equal(m.nodes[0].sub, 'gather coal x4');
  assert.equal(m.nodes[1].sub, 'craft furnace x1');
});

test('jobModel: a step carries its failure reason as the node note', () => {
  const m = jobModel(job({ steps: [{ key: 'mine', state: 'failed', reason: 'no pickup after 3 breaks' }, { key: 'hold', state: 'waiting' }] }));
  assert.equal(m.nodes[0].note, 'no pickup after 3 breaks');
  assert.equal(m.nodes[1].note, '');
});

test('jobModel: a dependency on no listed step is skipped, not drawn', () => {
  const m = jobModel(job({
    steps: [
      { key: 'mine', state: 'queued' },
      { key: 'hold', state: 'waiting', depends_on: ['mine', 'ghost'] },
    ],
  }));
  assert.deepEqual(m.edges, [{ from: 'mine', to: 'hold' }]);
});

test('jobModel: a job without steps answers empty columns, not a crash', () => {
  const m = jobModel(job({ steps: undefined }));
  assert.deepEqual(m.nodes, []);
  assert.deepEqual(m.edges, []);
  assert.equal(m.title, 'parallel-demo');
});

test('taskText: the known templates get a readable line, anything else its own word', () => {
  assert.equal(taskText({ template: 'gather_and_deposit', item: 'oak_log', qty: 16 }), 'gather oak_log x16');
  assert.equal(taskText({ template: 'haul', items: { oak_log: 8, coal: 2 } }), 'haul oak_log x8, coal x2');
  assert.equal(taskText({ template: 'inspect', chest: { x: 1, y: 2, z: 3 } }), 'inspect 1 2 3');
  assert.equal(taskText({ template: 'inspect' }), 'inspect');
  assert.equal(taskText({ template: 'mine_shaft' }), 'dig a shaft');
  assert.equal(taskText({ template: 'rotate', angle: 90 }), 'rotate');
  assert.equal(taskText({ template: 3 }), '');
  assert.equal(taskText('haul'), '');
  assert.equal(taskText(undefined), '');
});

test('stepChip: every state maps to a tone and a signature shape', () => {
  for (const s of STEP_STATES) {
    const chip = stepChip(s);
    const paths = Array.isArray(chip.icon) ? chip.icon : [chip.icon];
    assert.ok(paths.every(p => typeof p === 'string' && p), s + ' icon is a path');
    assert.ok(STEP_TONE[s]);
    assert.equal(chip.word, s);
  }
});

test('stepChip: a state the console has not met yet reads as itself, drawn plain', () => {
  const chip = stepChip('paused');
  assert.equal(chip.word, 'paused');
  assert.equal(chip.tone, 'plain');
  assert.deepEqual(chip.icon, STEP_ICON.waiting.icon);
});

test('stepChip: a missing state still gets a word, never an empty chip', () => {
  assert.equal(stepChip('').word, 'unknown');
  assert.equal(stepChip('').tone, 'plain');
});

test('jobList: running first, then the rest in feed order; duplicates and the wanted id collapse', () => {
  const rows = jobList([
    job({ id: 'b', state: 'queued' }),
    job({ id: 'a', state: 'running' }),
    job({ id: 'c', state: 'failed' }),
    job({ id: 'a', state: 'running' }),
  ]);
  assert.deepEqual(rows.map(r => r.id), ['a', 'b', 'c']);
  // A deep link to an old job stands in as a stub until it is fetched.
  const linked = jobList(rows, 'old-1');
  assert.deepEqual(linked.map(r => r.id), ['a', 'b', 'c', 'old-1']);
  assert.equal(linked.at(-1).fetching, true);
  // ...but not when the feed already carries it.
  assert.deepEqual(jobList(rows, 'b').filter(r => r.fetching), []);
});

test('pickJob: the linked job wins, else the running one, else the first on the list', () => {
  const rows = [job({ id: 'a', state: 'done' }), job({ id: 'b', state: 'running' })];
  assert.equal(pickJob(rows, 'a').id, 'a');
  assert.equal(pickJob(rows).id, 'b');
  assert.equal(pickJob(rows, 'nope').id, 'b');
  assert.equal(pickJob([job({ id: 'c', state: 'done' })]).id, 'c');
  assert.equal(pickJob([], 'nope'), null);
});

test('stepsDone and jobRow: a fetching stub answers progress, no title, no chip', () => {
  const full = job({ reason: 'no pickup', steps: [
    { key: 'a', state: 'done' }, { key: 'b', state: 'done' }, { key: 'c', state: 'failed' },
  ] });
  assert.deepEqual(stepsDone(full), { done: 2, total: 3 });
  const row = jobRow(full);
  assert.equal(row.title, 'parallel-demo');
  assert.equal(row.reason, 'no pickup');
  assert.equal(row.progress, '2/3');
  const stub = jobRow({ id: 'x', fetching: true });
  assert.equal(stub.title, null);
  assert.equal(stub.chip, null);
  assert.equal(stub.progress, '');
});

test('isOpen: only a running job counts as open (what the list groups first)', () => {
  assert.equal(isOpen(job({ state: 'running' })), true);
  assert.equal(isOpen(job({ state: 'failed' })), false);
});
