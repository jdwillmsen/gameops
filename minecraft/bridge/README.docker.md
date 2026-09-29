# jdwillmsen/mc-console-bridge

Sidecar exposing the Bedrock server console over HTTP behind a command allowlist

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/mc-console-bridge?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/mc-console-bridge?sort=semver)
[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

## What it is

A Go sidecar for a Bedrock dedicated server pod. It speaks
`mc-server-runner`'s websocket console and turns it into a small HTTP API
that only accepts commands from a fixed allowlist, so nothing upstream of it
can run an arbitrary console command. It is the only component with write
access to the console. The binary is static and ships on a distroless
nonroot base with no shell.

## Pull

The same image, with the same digest, is published to both registries:

```bash
docker pull ghcr.io/jdwillmsen/mc-console-bridge:<version>
docker pull docker.io/jdwillmsen/mc-console-bridge:<version>
```

GHCR is the primary registry; Docker Hub is a mirror of it.

## Tags

| Tag            | Meaning                                  |
| -------------- | ---------------------------------------- |
| `<version>`    | a release, e.g. `0.1.0`; never re-pushed |
| `sha-<commit>` | the same image, by full source commit    |

There is no `latest`. This sidecar has write access to the server console,
so a moving tag would let a later push silently replace what a running
deployment already trusts. Pin a version (or a digest).

Every image carries provenance and SBOM attestations:

```bash
docker buildx imagetools inspect jdwillmsen/mc-console-bridge:<version> --format '{{ json .Provenance }}'
docker buildx imagetools inspect jdwillmsen/mc-console-bridge:<version> --format '{{ json .SBOM }}'
```

## Configuration and docs

Environment variables, the HTTP API, the command allowlist and the console
protocol are documented in the
[GitHub README](https://github.com/jdwillmsen/mc-console-bridge#readme).

## Source and license

Source: <https://github.com/jdwillmsen/mc-console-bridge>
License: [PolyForm Noncommercial 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0/)
