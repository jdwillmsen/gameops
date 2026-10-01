// Deliberately empty. semantic-release depends on its npm publishing plugin,
// which bundles the whole npm CLI. Nothing here publishes to npm and the
// plugin list in .releaserc.json never names it, so the dependency is
// overridden with this stub: the CLI's bundled packages were the only source
// of vulnerability findings against this directory, in code that never ran.
throw new Error('@semantic-release/npm is stubbed out in this repo; add the real plugin before listing it in .releaserc.json');
