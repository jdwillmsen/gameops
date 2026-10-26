import test from 'node:test';
import assert from 'node:assert/strict';
import { readFilter, writeParams, formatFilter, toServerQuery, emptyFilter, isDefault, chips, dropChip, toggleIn, fromPublic, matchRow, sortRows, filterAndSort, mergeRows, nextSort, tailsToTop, detailText, severityOf, SEVERITIES } from './explorer.js';

const row = (id, over = {}) => ({ id, unit: 'crew-01', at: '2026-10-20T12:00:00Z', kind: 'online', severity: 'ok', payload: {}, ...over });

test('an untouched filter has a clean address and a round trip is stable', () => {
  assert.equal(formatFilter(emptyFilter()), '');
  assert.ok(isDefault(readFilter('?unit=crew-01&mode=first&view=events')));
  const f = readFilter('?f.unit=crew-01,crew-02&f.severity=warn,crit&f.q=no+pickup&f.range=1h&f.sort=unit&f.dir=asc');
  assert.deepEqual(f.unit, ['crew-01', 'crew-02']);
  assert.equal(readFilter(formatFilter(f).slice(1)).q, 'no pickup');
  assert.deepEqual(readFilter(formatFilter(f).slice(1)), f);
});

test('malformed parameters fall back instead of throwing', () => {
  const f = readFilter('?f.severity=loud,warn&f.range=forever&f.sort=colour&f.dir=up&f.since=yesterday&f.q=' + 'x'.repeat(500));
  assert.deepEqual(f.severity, ['warn']);
  assert.equal(f.range, '');
  assert.equal(f.sort, 'at');
  assert.equal(f.dir, 'desc');
  assert.equal(f.since, '');
  assert.equal(f.q.length, 200);
});

test('the server query drops the prefix and a custom start replaces the range', () => {
  const f = { ...emptyFilter(), unit: ['crew-01'], range: '1h', since: '2026-10-20T12:00:00Z' };
  const q = new URLSearchParams(toServerQuery(f, { limit: 100, cursor: '' }));
  assert.equal(q.get('unit'), 'crew-01');
  assert.equal(q.get('since'), '2026-10-20T12:00:00Z');
  assert.equal(q.get('range'), null);
  assert.equal(q.get('limit'), '100');
  assert.equal(q.has('cursor'), false);
  assert.equal(writeParams({ ...emptyFilter(), dir: 'asc', sort: 'kind' }).toString(), 'sort=kind&dir=asc');
});

test('chips can be built, dropped and toggled', () => {
  const f = { ...emptyFilter(), unit: ['a', 'b'], severity: ['crit'], q: 'oak', range: '1h' };
  const cs = chips(f);
  assert.deepEqual(cs.map(c => c.label), ['unit: a', 'unit: b', 'severity: Critical', 'text: oak', 'last 1 hour']);
  assert.deepEqual(dropChip(f, cs[0]).unit, ['b']);
  assert.equal(dropChip(f, cs[3]).q, '');
  assert.equal(dropChip(f, cs[4]).range, '');
  assert.deepEqual(toggleIn(['a'], 'b'), ['a', 'b']);
  assert.deepEqual(toggleIn(['a', 'b'], 'a'), ['b']);
});

test('every severity has an icon and a label', () => {
  for (const s of SEVERITIES) assert.ok(s.icon && s.label);
  assert.equal(severityOf('crit').label, 'Critical');
});

test('matchRow: lists OR, fields AND, text folds case over the payload', () => {
  const r = row(1, { unit: 'crew-02', kind: 'task_failed', severity: 'warn', task: 't1', payload: { text: 'No pickup after 3 tries' } });
  const f = o => ({ ...emptyFilter(), ...o });
  assert.ok(matchRow(r, f({})));
  assert.ok(matchRow(r, f({ unit: ['crew-01', 'crew-02'], kind: ['task_failed'] })));
  assert.ok(!matchRow(r, f({ unit: ['crew-01'] })));
  assert.ok(matchRow(r, f({ q: 'NO PICKUP' })));
  assert.ok(!matchRow(r, f({ q: 'oak' })));
  assert.ok(matchRow(r, f({ task: 't1' })));
  assert.ok(!matchRow(r, f({ task: 't2' })));
  assert.ok(!matchRow(r, f({ severity: ['crit'] })));
});

test('matchRow: ranges are relative to now and bounds are inclusive', () => {
  const now = Date.parse('2026-10-20T12:30:00Z');
  const r = row(1);
  assert.ok(matchRow(r, { ...emptyFilter(), range: '1h' }, now));
  assert.ok(!matchRow(r, { ...emptyFilter(), range: '15m' }, now));
  assert.ok(matchRow(r, { ...emptyFilter(), since: '2026-10-20T12:00:00Z', until: '2026-10-20T12:00:00Z' }, now));
  assert.ok(!matchRow(r, { ...emptyFilter(), until: '2026-10-20T11:59:59Z' }, now));
});

test('sortRows: key, then time, then id, in one direction; severity ranks by gravity', () => {
  const rows = [
    row(1, { severity: 'crit', unit: 'b' }),
    row(2, { severity: 'ok', unit: 'a' }),
    row(3, { severity: 'warn', unit: 'a', at: '2026-10-20T12:00:05Z' }),
    row(4, { severity: 'warn', unit: 'a' }),
  ];
  const ids = f => sortRows(rows, f).map(r => r.id);
  assert.deepEqual(ids({ ...emptyFilter(), sort: 'severity' }), [1, 3, 4, 2]);
  assert.deepEqual(ids({ ...emptyFilter(), sort: 'severity', dir: 'asc' }), [2, 4, 3, 1]);
  assert.deepEqual(ids({ ...emptyFilter(), sort: 'unit', dir: 'asc' }), [2, 4, 3, 1]);
  assert.deepEqual(ids(emptyFilter()), [3, 4, 2, 1]);
  assert.deepEqual(filterAndSort(rows, { ...emptyFilter(), unit: ['a'] }).map(r => r.id), [3, 4, 2]);
});

test('public feed events become rows of the stored shape', () => {
  const r = fromPublic({ seq: 7, at: 't', unit: 'crew-01', name: 'Agate', kind: 'died', level: 'crit', text: 'died' });
  assert.deepEqual(r, { id: 7, unit: 'crew-01', at: 't', kind: 'died', severity: 'crit', payload: { text: 'died' } });
  assert.equal(detailText(r), 'died');
  assert.equal(detailText(row(1, { payload: { n: 1 } })), '{"n":1}');
  assert.equal(detailText(row(1)), '');
});

test('mergeRows ignores repeats, puts new rows first and caps', () => {
  const a = [row(2), row(1)];
  assert.deepEqual(mergeRows(a, [row(2)]).added, []);
  const m = mergeRows(a, [row(4), row(3), row(2)], 4);
  assert.deepEqual(m.rows.map(r => r.id), [4, 3, 2, 1]);
  assert.deepEqual(mergeRows(a, [row(9)], 2).rows.map(r => r.id), [9, 2]);
});

test('nextSort flips a column, starts text columns ascending and time newest first', () => {
  assert.equal(nextSort(emptyFilter(), 'at').dir, 'asc');
  const u = nextSort(emptyFilter(), 'unit');
  assert.deepEqual([u.sort, u.dir], ['unit', 'asc']);
  assert.equal(nextSort(u, 'unit').dir, 'desc');
  assert.equal(nextSort(u, 'severity').dir, 'desc');
  assert.ok(tailsToTop(emptyFilter()));
  assert.ok(!tailsToTop(u));
});
