# jdwillmsen/minecraft-crew-control

Console and control plane for Crew worker units.

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-crew-control?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-crew-control?sort=semver)
![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)

## What it is

A web console for the Crew units. Anyone can watch: it reverse-proxies an
allow-list of each unit's camera routes and exposes nothing else of the units.
Operators who sign in with a passkey can queue work that the units pull. A static Go
binary on distroless, running as nonroot, linux/amd64.

## Pull

The same image, with the same digest, is published to two registries:

```bash
docker pull ghcr.io/jdwillmsen/minecraft-crew-control:<version>
docker pull docker.io/jdwillmsen/minecraft-crew-control:<version>
```

There is no `latest`; deployments pin a version. Tags are `<version>` and
`sha-<commit>`.

## Configuration

- `CREW_UNITS` is required: a JSON list of `{"id","name","url"}` units; each
  may also name `"player"`, its id in the world save, when the unit has not
  reported its own to the detail route.
- `LISTEN_ADDR` defaults to `0.0.0.0:8080`; `LOG_LEVEL` sets verbosity.
- Sign-in, the work queue (Postgres) and the unit token are optional; see the
  source README for their variables. Sign-in keeps its keys on a volume at
  `DATA_DIR` (default `/data`).
- The unit detail route reads a world save read-only from `CREW_WORLD_DB`;
  unset, it answers without the save's half. Mount the world read-only, and
  give `CREW_WORLD_WORK` a writable scratch volume (the root is read-only).

Source and docs: <https://github.com/jdwillmsen/gameops/tree/main/minecraft/crew-control#readme>
License: [PolyForm Noncommercial 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/)
