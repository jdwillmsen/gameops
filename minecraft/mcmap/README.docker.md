# jdwillmsen/minecraft-map

Web map of a Minecraft Bedrock world, rendered from the live server's own save

![Docker Image Version](https://img.shields.io/docker/v/jdwillmsen/minecraft-map?sort=semver)
![Docker Image Size](https://img.shields.io/docker/image-size/jdwillmsen/minecraft-map?sort=semver)
[![License](https://img.shields.io/badge/License-PolyForm%20NonCommercial%201.0-blue)](https://polyformproject.org/licenses/noncommercial/1.0.0/)

## What it is

A Go service that keeps a mirror of a running Bedrock Dedicated Server's
world, renders it to map tiles, and serves a zoomable web map of the
overworld, the nether and the end. It shows the terrain players actually
explored and built, not what the seed would generate, and refreshes every few
minutes without stopping the server.

It needs the [console bridge](https://hub.docker.com/r/jdwillmsen/mc-console-bridge)
running beside the server: that is what hands it a consistent copy of the
world.

## Quick start

```sh
docker run -d --name minecraft-map \
  -e BRIDGE_URL=http://console-bridge:8766 \
  -e BRIDGE_TOKEN=<the bridge's token> \
  -e LEVEL_NAME=<world directory name> \
  -v minecraft-map-data:/data \
  -p 8080:8080 \
  jdwillmsen/minecraft-map:<version>
```

The first cycle copies the whole world and renders it, which takes minutes;
later cycles move only what changed.

## The renderer is downloaded at runtime

Tiles are drawn by [uNmINeD](https://unmined.net/). Its licence allows free
non-commercial use but not redistribution, so it is not in this image: the
service downloads it on first start, checks it against a pinned SHA-256, and
keeps it in the `/data` volume. The container therefore needs outbound HTTPS
to `unmined.net` once.

## Configuration

`BRIDGE_URL`, `BRIDGE_TOKEN` and `LEVEL_NAME` are required. `REFRESH_INTERVAL`
(default `15m`), `QUIET_UTC`, `RENDER_CHUNK_PROCESSORS` and the rest are in the
[GitHub README](https://github.com/jdwillmsen/gameops/tree/main/minecraft/mcmap#readme).

## Tags

Versions only (`1.2.3`) plus `sha-<commit>`; there is no `latest`.

Source: <https://github.com/jdwillmsen/gameops/tree/main/minecraft/mcmap>
