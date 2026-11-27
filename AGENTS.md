# gameops: agent guide

The code behind the owner's Minecraft Bedrock servers: the server agent (`minecraft/agent`), the
console bridge (`bridge`), the AFK bots (`afkbot`), the web map (`mcmap`), the crew worker bots and
their console (`crew`, `crew-control`), and the add-on packs (`packs/`, shipped by
`minecraft/lab-packs`). The servers themselves are deployed from `jdwillmsen/jdw-deployments`. Jira
project: `JDW`.

## Build and test

- One Go module at the root. Each component's `component.yaml` holds its `lint`, `test` and `build`
  commands. CI runs exactly those for the components a change touches:
  `go run ./tools/components affected -base origin/main -head HEAD` lists them.
- Before pushing, run `go run ./tools/components validate`, then the touched components' tasks, for
  example `go test -race ./minecraft/agent/...`, and `gofmt -l` / `go vet`.
- crew-control's event and alert stores also run against Postgres in CI (`CREW_PG_TEST_DSN`). For
  crew, install numpy, or the Colosseum generator test skips.
- Packs: `tools/check-packs.sh`. Preview a pack locally with `tools/lab-preview`.
- The one required check is `ci-ok`. Commits are Conventional Commits, PRs rebase-merge, and commits are signed.

## Rollout path

1. A merge to `main` releases each touched component on its own (tag `<component>-v<semver>`). Only
   `feat`, `fix`, `perf`, `build` and `chore(deps)` commits release. The image goes to ghcr.io and
   is mirrored to Docker Hub.
2. Renovate in jdw-deployments opens the image bump. Agent, AFK bot, map and crew bumps auto-merge;
   the server image and lab packs move by hand.
3. Argo CD syncs from jdw-deployments. Confirm the new version is live in namespace `jdwillmsen-prd`.

Risk: crew and crew-control work, including its prd rollout, is R2 by owner ruling. Anything that
restarts the FWB game server, or touches world data, needs the owner.

## Never

- Commit FWB coordinates, the seed or predicted structure sites in code, tests, docs or PR text. A
  few positions are enough to brute-force the seed. Real-world tests read them from
  `MCMAP_REAL_PLACES`.
- Wire mob or entity selectors into the agent. Only the console bridge's allowlist writes to the console.

## Related

- `jdwillmsen/jdw-deployments`: charts, `tools/mc` (console CLI), runbooks in `docs/`.
- Operating the worlds (console recipes, traps, incidents): the `gameops:minecraft-ops` skill in
  `jdwillmsen/private-skills`.
