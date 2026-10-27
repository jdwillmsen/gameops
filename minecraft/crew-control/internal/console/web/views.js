// The console's view registry: one row per top-bar view. index.html buttons
// and the key handler in console.js must agree with it; views.test.mjs checks.
export const REGISTRY = [
  { view: 'wall', handler: 'toggleWall', toggle: 'wall-toggle', key: 'w', title: 'Toggle wall', label: 'Wall' },
  { view: 'fleet', handler: 'toggleFleet', toggle: 'fleet-toggle', key: 'o', title: 'Fleet overview', label: 'Fleet' },
  { view: 'dash', handler: 'toggleDash', toggle: 'dash-toggle', key: 'b', title: 'Dashboard', label: 'Dashboard' },
  { view: 'detail', handler: 'toggleDetail', toggle: 'detail-toggle', key: 'd', title: 'Unit detail', label: 'Detail' },
  { view: 'heat', handler: 'toggleHeat', toggle: 'heat-toggle', key: 'h', title: 'Heatmap', label: 'Heat' },
  { view: 'jobs', handler: 'toggleJobs', toggle: 'jobgraph-toggle', key: 'g', title: 'Job graph', label: 'Graph' },
  { view: 'events', handler: 'toggleExplorer', toggle: 'explorer-toggle', key: 'x', title: 'Event explorer', label: 'Explorer' },
  { view: 'ledger', handler: 'toggleLedger', toggle: 'ledger-toggle', key: 'l', title: 'World ledger', label: 'Ledger' },
  { view: 'timeline', handler: 'toggleTimeline', toggle: 'timeline-toggle', key: 't', title: 'Timeline', label: 'Timeline' },
  { view: 'replay', handler: 'toggleReplay', toggle: 'replay-toggle', key: 'y', title: 'Replay', label: 'Replay' },
];

// Views with no toggle of their own: focus is the default, reached by a unit.
export const UNREGISTERED = ['focus'];
