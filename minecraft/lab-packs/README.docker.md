# jdwillmsen/lab-packs

Installs the crew lab's Bedrock add-on packs into a world and registers them.

## What it is

An init-container image. `/install.sh <world-dir>` replaces
`behavior_packs/gameops-<theme>` and `resource_packs/gameops-<theme>` for every
theme in `/packs`, then registers each pack's uuid and version in the world's
pack lists, keeping other entries. It needs no network and writes only inside
the world directory.

The image sets no user: the chart's pod securityContext decides it (1000:3000
in the lab), and that user must be able to write the world files.

## Pull

```bash
docker pull ghcr.io/jdwillmsen/lab-packs:<version>
docker pull docker.io/jdwillmsen/lab-packs:<version>
```

## Tags

| Tag            | Meaning                                  |
| -------------- | ---------------------------------------- |
| `<version>`    | a release, e.g. `0.1.0`; never re-pushed |
| `sha-<commit>` | the same image, by full source commit    |

There is no `latest`; pin a version (or a digest).
