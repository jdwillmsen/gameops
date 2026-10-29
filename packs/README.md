# Add-on packs

Bedrock add-ons for the crew lab. Each folder under `packs/` is one theme: a
`behavior_pack/` (what a mob does) and a `resource_pack/` (how it looks and
moves). `tools/check-packs.sh` validates every folder.

## Catalog

| Folder | Mobs | Pack version | Notes |
|---|---|---|---|
| `lab-mods-stage0` | `gameops:training_dummy_brute`, `gameops:werewolf`, `gameops:ground_dragon` | 0.0.4 | First add-on; arena opponents and the ground dragon boss |
| `lab-mods-wildlife` | `gameops:tiger` (cheetah, lion, black leopard, brown bear and hyena to follow) | 0.0.1 | Tamable wildlife; `policy.txt` selects the wildlife checker rules; generated from `assets-src/` |

Keep this table current when a pack is added, split or versioned.

## One pack per theme

- A theme is a set of mobs that ship and change together, such as arena
  opponents or a boss with its own scripts. Do not make one pack per mob: each
  pack costs a manifest pair, two UUIDs, an install block in the chart and a
  client download. Do not put everything in one pack either: any change then
  restarts the lab and makes every client download it again.
- Split a boss into its own pack when it carries its own scripts or large
  textures, or when it changes on a different schedule.
- Packs do not depend on each other. Copy a shared texture rather than
  referencing another pack; cross-pack geometry reuse is unverified.

## Conventions

- Entity ids are `gameops:<name>`; type families are `gameops_<name>`.
- Pack folders are `<theme>`; the world folder name the chart installs them
  under is `gameops-<theme>`. Never rename an installed folder: the chart
  deletes by that name.
- Generate each UUID once and never reuse one. A pack has four distinct UUIDs
  (two headers, two modules); `check-packs.sh` enforces this.
- Bump `version` in both manifests, the behaviour pack's dependency on the
  resource pack, and the chart's world pack-list entries on every shipped
  change. Clients cache resource packs by UUID and version.
- Mobs target only the `arena_warrior` tag, never the player family, and have
  no `hurt_by_target`. `check-packs.sh` enforces both.

## Variants and tiers

- Model a variant or difficulty tier of the same body as component groups plus
  events inside one entity (for example an `alpha` group, a `tier_3` group),
  applied through the summon event. The arena director already passes a
  `variant` per wave mob.
- Use a separate entity only for a different body, hitbox or model.
- Tier groups should set health, damage and speed. Keep the console effects in
  the wave file's tiers as the common knob across vanilla and pack mobs.

## Bots and add-on mobs

Bot tactics are Go (`minecraft/crew/internal/life`) and are keyed by entity id,
so a new pack mob that bots should fight needs an entry in `kinds.go`. Pack
mobs themselves are driven only by their JSON goals.

## Shipping

The chart ships a pack as a tarball in a ConfigMap, which tops out near 780 KB
raw for everything combined. Move textured packs to an image or volume built by
CI before the second themed pack lands. Every change needs a lab restart: the
server reads packs only at start, and `/reload` does not pick up entity JSON.
Draft with `tools/lab-preview` first so the lab restarts once per release.
