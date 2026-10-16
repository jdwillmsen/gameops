# jdwillmsen/minecraft-crew

Programmable Minecraft Bedrock worker unit that joins a server and reports its own state.

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-crew?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-crew?sort=semver)
![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)

## What it is

A crew unit signs in as a Microsoft account, joins a Bedrock server, keeps
track of what it believes about itself, reconnects with backoff and respawns if
it dies. It serves that belief on a status API and, if enabled, streams what it
sees to a browser camera page. It has no chat and no model client: its
behaviour is the code it ships. A static Go binary on distroless, running as
nonroot, linux/amd64.

## Pull

The same image, with the same digest, is published to two registries:

```bash
docker pull ghcr.io/jdwillmsen/minecraft-crew:<version>
docker pull docker.io/jdwillmsen/minecraft-crew:<version>
```

## Tags

| Tag | Meaning |
|---|---|
| `<version>` | A release, e.g. `0.1.0`. Never moved once published |
| `sha-<commit>` | The full commit the release was built from |

There is no `latest`. Deployments pin a version, and a moving tag would
upgrade a running unit silently, without anyone choosing to.

Every image carries OCI labels, a max-mode provenance attestation and an SBOM:

```bash
docker buildx imagetools inspect jdwillmsen/minecraft-crew:<version> --format '{{ json .Provenance }}'
docker buildx imagetools inspect jdwillmsen/minecraft-crew:<version> --format '{{ json .SBOM }}'
```

## Configuration

- `MC_HOST` and `MC_USERNAME` are required; the rest have defaults.
- `/data` is a writable volume holding the Xbox Live token cache and the
  texture atlas cache.
- Port `8080` serves the status API (`/healthz`, `/readyz`, `/v1/state`) and
  the camera. It carries positions and has no authentication, so it listens on
  `127.0.0.1` unless `STATUS_ADDR` says otherwise. Do not expose it.
- `MC_OFFLINE=true` skips sign-in and exists for a lab server with online
  mode off. A real server rejects such a unit.

Every variable and the first-run sign-in are documented on GitHub:
<https://github.com/jdwillmsen/gameops/tree/main/minecraft/crew#readme>

## Source and license

Source: <https://github.com/jdwillmsen/gameops/tree/main/minecraft/crew>
License: [PolyForm Noncommercial 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/)
