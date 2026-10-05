# gameops

Tooling around the game servers: services, bots, frontends and the packs they
load. One repo so a change that crosses components lands in one reviewed PR.

## Layout

| Path | What |
|---|---|
| `minecraft/agent/` | Bedrock server chat agent, census, join probe |
| `minecraft/bridge/` | Console bridge sidecar |
| `minecraft/afkbot/` | AFK bots |
| `minecraft/mcmap/` | Web map of the world, rendered from the live server's save |
| `internal/` | Go code shared across games (`internal/presenceapi`: the actor-presence contract) |
| `minecraft/internal/` | Go code shared across Minecraft components (created when first needed) |
| `tools/components/` | Reads `component.yaml`; CI and releases use it |

Each component's own README covers what it does and how to run it.

## Adding a component

1. Create its directory under the game it belongs to.
2. Add a `component.yaml` (copy a neighbour's). `kind`, `language`, `tasks`
   and, if it ships, `release` are the whole contract; CI and releases read
   nothing else.
3. `go run ./tools/components validate`.

CI runs a component's `lint`, `test` and `build` when its directory, a
`depends` path or its language's root toolchain files change. A merge to
`main` releases it (tag `<release.tag>-v<semver>`) when a commit touching it
is a `feat`, `fix`, `perf`, `build` or `chore(deps)`.

## Go

One module, `github.com/jdwillmsen/gameops`, at the root. Build a component
from the root: `go build ./minecraft/agent/...`.

## License

[PolyForm Noncommercial 1.0.0](LICENSE.md)
<!-- throwaway stacked PR probe -->
