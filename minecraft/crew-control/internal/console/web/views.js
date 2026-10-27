// The console's view registry: one row per top-bar view. index.html buttons
// and the key handler in console.js must agree with it; views.test.mjs checks.
export const REGISTRY = [
  { view: 'wall', toggle: 'wall-toggle', key: 'w', title: 'Toggle wall', label: 'Wall' },
  { view: 'fleet', toggle: 'fleet-toggle', key: 'o', title: 'Fleet overview', label: 'Fleet' },
  { view: 'dash', toggle: 'dash-toggle', key: 'b', title: 'Dashboard', label: 'Dashboard' },
  { view: 'detail', toggle: 'detail-toggle', key: 'd', title: 'Unit detail', label: 'Detail' },
  { view: 'heat', toggle: 'heat-toggle', key: 'h', title: 'Heatmap', label: 'Heat' },
  { view: 'jobs', toggle: 'jobgraph-toggle', key: 'g', title: 'Job graph', label: 'Graph' },
  { view: 'events', toggle: 'explorer-toggle', key: 'x', title: 'Event explorer', label: 'Explorer' },
  { view: 'ledger', toggle: 'ledger-toggle', key: 'l', title: 'World ledger', label: 'Ledger' },
  { view: 'timeline', toggle: 'timeline-toggle', key: 't', title: 'Timeline', label: 'Timeline' },
  { view: 'replay', toggle: 'replay-toggle', key: 'y', title: 'Replay', label: 'Replay' },
];

// Views with no toggle of their own: focus is the default, reached by a unit.
export const UNREGISTERED = ['focus'];
