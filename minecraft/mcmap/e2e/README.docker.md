# jdwillmsen/minecraft-map-checks

Browser checks of the Minecraft Bedrock web map, run against a live map with a read-only service session

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-map-checks?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-map-checks?sort=semver)
[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

## What it is

A job, not a service: it opens the
[web map](https://hub.docker.com/r/jdwillmsen/minecraft-map) in Chromium
under Playwright, uses the page as a visitor does, measures what it draws,
and ends. It checks the login, the session, the page's files and answers,
the map's controls, Follow on a player and on a mob, and that the live
stream stays open. Its results are a line of JSON per check on stdout, its
exit status and, where asked, a file of Prometheus metrics.

It logs in as a service identity that can read the map and write nothing,
and the browser it drives is stopped from sending anything but a read.
Nothing read off the page is written to a log or a metric: a result is a
check's name, a verdict, a reason from a fixed list, and numbers.

The image is Microsoft's Playwright image, pinned by digest, with the checks
in it. It runs as that image's unprivileged user, uid 1001.

## What the map must have

Service sessions switched on, which is `SERVICE_SESSION_SECRET` set on the
map, and the same secret in a file this container can read. The run reaches
the map at two addresses: its internal listener once, to exchange the secret
for a session, and its public address for everything after.

## Quick start

```sh
docker run --rm --read-only --tmpfs /tmp \
  -v <file holding the secret>:/run/secrets/map-checks:ro \
  jdwillmsen/minecraft-map-checks:<version> \
  --public-url https://<the map's address> \
  --internal-url https://<the map's internal listener> \
  --secret-file /run/secrets/map-checks
```

The secret is read from the file and from nowhere else: there is no argument
and no variable that takes it. `--internal-url` may be plain `http` only for
a Service inside the cluster, a host name with no dot in it, or the
machine the run is on; anywhere else it must be `https`. `--public-url` is
always `https`, with no path.

A whole run is a little over two minutes: one check watches the live stream
for 125 seconds. One CPU and 1 GiB are enough for it. The root filesystem
can be read-only, with `/tmp` the only place it writes.

## Exit status

| Status | Meaning |
|---|---|
| `0` | Every check passed, or had nothing in the world to look at |
| `1` | A check failed |
| `2` | The run itself could not be made, broke, or was stopped |

## Options

Each may be given as a variable instead, `MCMAP_E2E_<NAME>` with the dashes
as underscores, such as `MCMAP_E2E_PUBLIC_URL`.

| Option | Meaning |
|---|---|
| `--metrics-file <path>` | Write the run as Prometheus text, from when it begins |
| `--only a,b` | Run only these checks; the exchange and the page's first load are run whatever it says |
| `--evidence-dir <path>` | On a failed check, keep a screenshot of the page. Off unless given: a screenshot shows the live map |
| `--evidence-pages` | With it, also keep the page's HTML and the text of what went wrong |

The durations and budgets, the list of checks and what each one measures are
in the
[GitHub README](https://github.com/jdwillmsen/gameops/tree/main/minecraft/mcmap#browser-checks).
`--secret-from-kube` and `--port-forward`, which that README describes, run
`kubectl`, which is not in this image: they are for a run from a checkout.

## Pull

The same image, with the same digest, is published to both registries:

```sh
docker pull ghcr.io/jdwillmsen/minecraft-map-checks:<version>
docker pull docker.io/jdwillmsen/minecraft-map-checks:<version>
```

## Tags

Versions only (`1.2.3`) plus `sha-<commit>`; there is no `latest`.

Source: <https://github.com/jdwillmsen/gameops/tree/main/minecraft/mcmap/e2e>
