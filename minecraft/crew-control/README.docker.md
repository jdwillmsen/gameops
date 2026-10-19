# jdwillmsen/minecraft-crew-control

Console and control plane for Crew worker units.

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-crew-control?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-crew-control?sort=semver)
![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)

## What it is

A view-only web console for the Crew units. It reverse-proxies an allow-list of
each unit's camera routes and exposes nothing else of the units. A static Go
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

- `CREW_UNITS` is required: a JSON list of `{"id","name","url"}` units.
- `LISTEN_ADDR` defaults to `0.0.0.0:8080`; `LOG_LEVEL` sets verbosity.
- The service holds no state and needs no volume.

Source and docs: <https://github.com/jdwillmsen/gameops/tree/main/minecraft/crew-control#readme>
License: [PolyForm Noncommercial 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/)
