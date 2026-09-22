# jdwillmsen/minecraft-afk-bot

Headless Minecraft Bedrock client that holds a player slot so mob farms keep ticking.

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-afk-bot?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-afk-bot?sort=semver)
![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)

## What it is

Mob farms need a *player* in the dimension: ticking areas keep chunks loaded
but never spawn mobs. This bot signs in as a real Microsoft account, connects
to a Bedrock server, asks for a chunk radius, and stays — reconnecting with
backoff and respawning itself if it dies.

That is the whole job. It does not read chat, run commands or persist anything
beyond its Xbox Live token cache. A static Go binary on distroless, running as
nonroot, linux/amd64.

## Pull

The same image, with the same digest, is published to two registries:

```bash
docker pull ghcr.io/jdwillmsen/minecraft-afk-bot:<version>
docker pull docker.io/jdwillmsen/minecraft-afk-bot:<version>
```

## Tags

| Tag | Meaning |
|---|---|
| `<version>` | A release, e.g. `0.3.0`. Never moved once published |
| `sha-<commit>` | The full commit the release was built from |

There is no `latest`. Deployments pin a version, and a moving tag would
upgrade a running bot silently, without anyone choosing to.

Every image carries OCI labels, a max-mode provenance attestation and an SBOM:

```bash
docker buildx imagetools inspect jdwillmsen/minecraft-afk-bot:<version> --format '{{ json .Provenance }}'
docker buildx imagetools inspect jdwillmsen/minecraft-afk-bot:<version> --format '{{ json .SBOM }}'
```

## Configuration and first run

The bot needs `MC_HOST`, `MC_USERNAME`, a writable `/data` volume for the auth
cache, and a one-time device-code login by hand. Every variable, the log events
and the first-run steps are documented on GitHub:
<https://github.com/jdwillmsen/minecraft-afk-bot#readme>

## Source and license

Source: <https://github.com/jdwillmsen/minecraft-afk-bot>
License: [PolyForm Noncommercial 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/)
