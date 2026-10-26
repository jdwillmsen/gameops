// The "Why" panel body: the explainer's answer as sentences, the chain of
// events behind each, and the rule that produced it. Everything here is
// derived by fixed rules, and the panel says so.

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-')) e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

// The unexplained bucket is a row like any other, never a footnote: a
// failure no rule covers is shown as such and given no guessed cause.
export function whyRows(result) {
  if (!result || !result.total) {
    return [{ kind: 'none', sentence: 'No failed tasks in this window.', rule: '', chain: [], count: 0 }];
  }
  const rows = (result.findings || []).map(f => ({
    kind: 'finding',
    sentence: f.sentence,
    rule: f.rule,
    chain: f.chain || [],
    count: f.count,
  }));
  const u = result.unexplained || { count: 0 };
  if (u.count > 0) {
    rows.push({ kind: 'unexplained', sentence: u.sentence, rule: '', chain: u.failures || [], count: u.count });
  }
  return rows;
}

// "failed 14 times" reads better than a bare count beside a sentence.
export function countText(n) {
  return n === 1 ? 'once' : `${n} times`;
}

export function renderWhy(result) {
  const box = el('div', { className: 'why' });
  const rows = whyRows(result);
  for (const r of rows) {
    const line = el('li', { className: `why-row why-${r.kind}` });
    line.append(el('span', { className: 'why-text', textContent: r.count ? `${r.sentence} (${countText(r.count)})` : r.sentence }));
    if (r.rule) line.append(' ', el('span', { className: 'why-badge', textContent: `Rule-derived: ${r.rule}`, title: 'Derived by a fixed rule, not a model' }));
    if (r.chain.length) {
      const label = r.kind === 'unexplained' ? 'events' : 'chain';
      line.append(el('div', { className: 'why-chain', textContent: `${label}: ${r.chain.map(id => `#${id}`).join(' → ')}` }));
    }
    box.append(el('ul', { className: 'why-list' }, line));
  }
  return box;
}
