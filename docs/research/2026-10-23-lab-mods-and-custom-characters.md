# Lab mods and custom characters: add-ons vs alternative server software

Researched 2026-10-23. Scope: crew lab world on Bedrock Dedicated Server (BDS, itzg container, retail clients over RakNet, online-mode off). FWB stays vanilla and is out of scope. Every dated claim names its source; items I could not confirm from a primary source are marked UNVERIFIED.

## 1. Summary

Use the official add-on route (behaviour pack + resource pack + Script API) on the existing BDS. It runs on unmodified retail clients, needs no new server software, and our Go bots keep working. It covers custom entities with phases (JSON components plus script), custom models and animations, and boss HUD bars. Defer a server-software change (Endstone is the only credible candidate) until add-ons demonstrably cannot do something we need. Dragonfly and PowerNukkitX are poor fits: Dragonfly has no vanilla mob AI and PNX is a Java rewrite with vanilla-parity gaps. Both would also lose BDS parity for the world we already have.

## 2. (A) Official add-on route

### 2.1 What a pack can define (Microsoft Learn creator docs and `MicrosoftDocs/minecraft-creator` repo, read 2026-10-23)

- Custom entities: server side is a behaviour-pack entity JSON (components, component groups, events, `minecraft:behavior.*` goals, spawn rules, loot, `minecraft:boss` for the boss HUD). Client side is a resource-pack `client_entity` file (geometry, textures, render controllers, animations, animation controllers, Molang).
- The entity reference documents `minecraft:boss` (health bar name, `hud_range` default 55, `should_darken_sky`) and a full set of dragon goals (`dragonchargeplayer`, `dragonflaming`, `dragonholdingpattern`, `dragonlanding`, `dragonscanning`, `dragonstrafeplayer`, `dragontakeoff`, `dragondeath`). A dragon-class boss therefore reuses vanilla Ender Dragon behaviours with our own model, textures and stats. Pure custom flight and phase logic is done in script.
- Phases are normally done with component groups plus events triggered by `minecraft:environment_sensor` or health thresholds, or from script (`@minecraft/server` `entityHurt`/`entityDie` events, `entity.triggerEvent`, `setProperty` on entity properties).
- Custom items and blocks are supported (data-driven, plus custom components in script). Samples exist in `microsoft/minecraft-samples` (MIT, checked 2026-10-23): `custom_items`, `custom_blocks`, `custom_projectiles`, `add_entity_robot`, `npc_dialogue_sample`, `addon_starter`.
- Authoring tools: Blockbench for models and animations (docs page `Blockbench.md`); the `mctoolsval` pack-size validator exists in the docs repo's forms list.

### 2.2 Script API (npm registry dist-tags, 2026-10-23)

| Module | Stable (`latest`) | Beta tag |
|---|---|---|
| `@minecraft/server` | 2.10.0 | 2.13.0-beta.1.26.70-preview.21 (published 2026-10-20) |
| `@minecraft/server-ui` | 2.2.0 | 2.5.0-beta.1.26.70-preview.21 |
| `@minecraft/server-gametest` | 1.0.0-beta.1.21.60 (still beta) | 1.0.0-beta.1.26.70-preview.21 |
| `@minecraft/server-admin` | 1.0.0-beta (only beta) | same |
| `@minecraft/server-net` | 1.0.0-beta (only beta) | same |

- The retail release the gophertunnel dependency targets is 1.26.50 (protocol 2193, `Sandertv/gophertunnel` `protocol/info.go`, 2026-10-23). The npm beta tags track 26.70 previews, so stable-only APIs are the safe target for a pack that must run on a retail-matched BDS.
- Beta APIs need the "Beta APIs" experiment enabled on the world and are explicitly subject to change or removal each version (BDS scripting doc, `BedrockServer/scripting.md`). Using them couples the pack to an exact BDS+client build. Rule: depend on stable `@minecraft/server` and `@minecraft/server-ui` only; treat each beta call as a tracked exception.
- BDS-only modules: `@minecraft/server-admin` (server variables and secrets) and `@minecraft/server-net` (HTTP from script) work only on dedicated servers and are beta (same doc). `config/default/permissions.json` allows `server-gametest`, `server`, `server-ui`, `server-admin`, `server-editor` by default; `server-net` must be added per script-module UUID under `config/<module-uuid>/permissions.json`.
- Scripting runs sandboxed JavaScript inside the server process (QuickJS in BDS). There is no file or socket access beyond `server-net`, so any AI that needs outside compute must go through HTTP (see section 5).
- GameTest (`server-gametest`) is still beta and is the in-engine test harness; see testing in section 7.

### 2.3 Installing packs on BDS

- Facts from the BDS getting-started doc (updated 2026-01-28) and the itzg README (read 2026-10-23): packs live in top-level `behavior_packs/` and `resource_packs/` or per-world folders. Each world has `world_behavior_packs.json` and `world_resource_packs.json`, entries of `{ "pack_id": "<header uuid>", "version": [x,y,z] }`. The pack_id is the manifest header UUID, not the module UUID.
- Restart cost: BDS reads packs at startup. A change to the pack files needs a server restart; script-only edits can sometimes be applied with `/reload` in a client-hosted world, but I found no source that this is reliable on BDS. Treat every pack change as a restart that kicks players, as the current crew-lab chart already does. UNVERIFIED for `/reload` on BDS.
- itzg: `MC_PACK` unpacks an archive or directory (behaviour packs, resource packs, `.mcaddon`) into the server's pack folders at startup; `FORCE_PACK_COPY=true` replaces same-named folders on every start (otherwise a pack is copied only if absent, which would silently serve stale packs after an update); `TEXTUREPACK_REQUIRED` maps to `texturepack-required`. This matches the existing ConfigMap + init-container mechanism: the stage-0 pack should use it with force-copy on.
- Pack optimizer (new, BDS doc `pack-optimization.md`, read 2026-10-23): BDS can run `PackOptimizerConfigPath=<config>` to minify JSON and archive loose files; optimised packs require client 1.26.40 or newer. Use only if first-join time becomes a problem.
- Pack size limits: I found no primary-source hard limit for dedicated-server packs. Practical limits are the ConfigMap object cap (1 MiB in Kubernetes, which a resource pack with textures and geometry will exceed) and client download time. UNVERIFIED for any game-side cap. Consequence: the stage-0 ConfigMap mechanism only holds small text-only packs; real resource packs need an image/PVC/OCI-artifact based delivery.

### 2.4 How a retail client gets a server resource pack over RakNet

- The server sends `ResourcePacksInfo` with `TexturePackRequired`, `HasAddons`, `HasScripts` flags (field definitions in gophertunnel `packet/resource_packs_info.go`, 2026-10-23). The client answers with the packs it lacks, and the server streams them in chunks (`ResourcePackDataInfo` and `ResourcePackChunkData`) through the game connection itself. There is no CDN and no HTTP hop; the BDS process serves the bytes, so large packs are paid for in server egress on each cold join.
- `texturepack-required=true` forces acceptance: a declining client is disconnected (BDS server-properties doc, 2026-10-23 and gophertunnel comment: "behaviour packs never have to be downloaded"). Behaviour packs stay on the server; only resource packs are downloaded, so custom visuals require the resource pack to be accepted.
- Clients cache downloaded packs by UUID+version. Always bump the manifest version on every change, or clients keep the stale cache.
- Encryption/signing: Bedrock supports per-pack content keys (AES-256 CFB8, `content.key` plus `contents.json`) so a pack can be encrypted; gophertunnel's `resource` package implements it. This hides assets from casual download but not from a determined client, and it is not needed for a private lab.
- Our Go bots join as clients: when `texturepack-required` is true the bot must accept each required pack through `DownloadResourcePack` (returning false for a required pack ends the join); only when it is false may a bot decline packs, and it still needs `HasScripts`/`HasAddons` handled. This should be proven in the stage-0 spike (a bot joining a server with a required pack) before anything else.

### 2.5 What "custom characters" can mean

1. Custom entities as characters (NPC-like or boss): own geometry, texture, animations, behaviour; owned by the server; visible to every retail client that accepted the resource pack. This is the best fit.
2. Custom player skins and Persona: the skin (and optional custom geometry and animations) is sent by the connecting client in the login JWT and relayed to others. Our bots can already set generated skins in gophertunnel. Custom geometry via a skin is supported by the protocol (`SkinGeometry`, `SkinResourcePatch`) but other retail clients may only render it if the account is allowed to use custom skins. UNVERIFIED which retail platforms accept bot-supplied custom geometry; test with a real client. For a Marketplace-style "skin pack" Microsoft has `SkinPack.md` and `PackagingASkinPack.md`, which is a client-side catalogue mechanism and not relevant to a server.
3. Scripted NPCs: the `minecraft:npc` component gives dialogue and commands, with `@minecraft/server-ui` forms for richer menus (sample `npc_dialogue_sample`).
4. Client-side-only visuals (attachables, particle effects, camera shake via the camera system docs) can decorate bosses without any server code.

## 3. (B) Alternative server software

Facts from each project's GitHub repo via `gh api`, 2026-10-23.

| Option | Language | Licence | Latest release / client tracking | Custom entities | Retail client / bot interop | Ops on Kubernetes |
|---|---|---|---|---|---|---|
| BDS + add-ons (baseline) | closed (Mojang), packs in JSON/JS | Minecraft EULA; modded server software may not be distributed, mods may | tracks retail exactly | Yes, data-driven plus Script API | Perfect | Already running |
| Endstone `EndstoneMC/endstone` | C++ and Python plugins on top of real BDS | Apache-2.0 | v0.11.14 (2026-10-12); README badge 26.52 | Uses BDS entity system, so same add-on route plus event cancellation, packet hooks, deeper access | Perfect (it is BDS); README claims 2x TPS of BDS under load | `endstone/endstone` Docker image exists; pip-based; Python 3.11+, Linux supported |
| LeviLamina `LiteLDev/LeviLamina` | C++ mods, hooks BDS binary | LGPL-3.0 plus its own EULA and usage guidelines (README warns to read them) | v26.51.7 (2026-10-12) | BDS entity system plus hooks | Perfect | Windows-first; Linux support less documented. Documentation and community mostly Chinese. Hooks the closed binary, so a BDS update breaks it until patched |
| Dragonfly `df-mc/dragonfly` | Go library (go 1.26, gophertunnel v1.62.0) | MIT | no tagged releases found via the releases API; last push 2026-09-20 | Implement `world.EntityType` yourself (interface in `world/entity.go`); needs a resource pack for the visuals; no vanilla mob AI shipped (entity package lists projectiles, items, TNT, etc., no zombies) | Retail clients join; but you reimplement worldgen/mobs/redstone and world is not a BDS world | Single Go binary, tiny image, easy to run |
| PowerNukkitX `PowerNukkitX/PowerNukkitX` | Java 21 | LGPL-3.0 | 3.0.5 (2026-09-15), protocol 2193 / 26.50 per README | "Custom Item, Block and Entity" listed; vanilla-like mob AI, worldgen | Retail clients join; behaviour differs from BDS in the long tail | JVM container, heavier; own world format handling |
| Nukkit `CloudburstMC/Nukkit` | Java | GPL-3.0 | Only a 2018 snapshot release; I did not verify current protocol | Not current | Treat as legacy | Not recommended |
| PocketMine-MP `pmmp/PocketMine-MP` | PHP | LGPL-3.0 | 5.44.3 (2026-07-06), repository now archived | Limited custom entity support | Last release is two versions behind retail | Archived upstream: do not adopt |

Notes:

- Endstone is the only option currently recommended for this lab that adds server-side power without giving up the real BDS engine (LeviLamina also hooks the BDS binary, but its Windows-first tooling suits a Linux/Kubernetes setup less well). It also answers the question of control from Go: a plugin can expose a local socket or HTTP endpoint (see section 5). Risk: it trails each BDS release by days to weeks, and it hooks BDS internals like LeviLamina does.
- Dragonfly deserves a note because we already speak gophertunnel. It would be the right choice for a clean-room "arena minigame world" with fully scripted mobs, but we would reimplement everything the lab already relies on (vanilla worldgen, existing world data, mob AI, command blocks).
- The EULA states modified server software cannot be distributed, while mods can be (EULA summary, minecraft.net search result, 2026-10-23). Running Endstone or LeviLamina privately on the lab is not distribution; do not publish prebuilt modified server images.

## 4. (C) Controlling custom entities from our Go AI

Options ordered by effort:

1. Bots as players with custom skins (already working): the AI drives a player entity with a generated skin. Custom geometry and animations are available through the skin payload but only the player skeleton's bones can be animated. Good for characters that look different; no new attacks, no boss HUD.
2. Script API as the AI host: write the boss brain in `@minecraft/server` (state machine on `system.runInterval`). Reasoning stays in-process JavaScript. Limited CPU budget per tick (watch-dog) and no network unless `server-net` (beta, BDS only).
3. Go AI plus Script API over HTTP: script polls or posts via `@minecraft/server-net` `http.request` to a Go service on the cluster (state in, action out: `entity.applyImpulse`, `triggerEvent`, `runCommand`). Needs `server-net` permission per script UUID; set `allowed_uris` under `module_permissions` in that UUID's `permissions.json` to the Go service endpoint, because an unset list is not enforced. Latency means this suits high-level decisions (phase changes, target choice every 0.5-1 s), not per-tick movement.
4. Go AI over the bot's own client connection: a bot cannot control a non-player entity. It can only command through chat or `/scriptevent` if the bot has operator permission. `/scriptevent <id> <msg>` is received by script via `system.afterEvents.scriptEventReceive` (stable in `@minecraft/server`), which makes it the cleanest one-way bridge from the Go side with no beta API. Limit: bot must be an operator on the lab.
5. Endstone plugin exposing a socket: only if (3) and (4) prove insufficient.

Recommended: 4 for commands into the world and 3 later for read-back, both behind stable-API-first design.

## 5. (D) Recommendation and staged plan

Value per effort ranking for custom characters and bosses on the lab: (1) add-ons on existing BDS; (2) add-ons plus Endstone only if a concrete limit is hit; (3) Dragonfly or PNX only as a separate experimental world, never as the lab's primary server.

Stages (each stage ships as one PR; a restart kicks players, so batch changes):

- Stage 0, spike (about a day): one custom entity (a small mob with custom geometry from Blockbench, a self-drawn texture, and a simple `minecraft:behavior.melee_attack` with a target behaviour such as `minecraft:behavior.nearest_attackable_target` and a `minecraft:attack` damage component, without which it never hurts anything) delivered as a resource pack plus behaviour pack through the existing crew-lab mechanism with `FORCE_PACK_COPY=true` and bumped versions. Success criteria: summon it, a retail client sees the custom model, a Go bot joins without disconnecting, and content log (`content-log-file-enabled=true`) is clean. Record the ConfigMap size so we know where the 1 MiB wall is.
- Stage 1: delivery. Move packs out of the ConfigMap into an OCI image or PVC built by CI from the new repo; set `texturepack-required=true` on the lab; verify cold-join time and cache behaviour with a bumped version. Add the content-log check to the rollout.
- Stage 2: boss prototype. Dragon-class entity reusing the documented dragon goals plus `minecraft:boss` HUD; two phases via component groups and a script health-threshold event; `/scriptevent` bridge from Go for "enter phase N".
- Stage 3: characters. Custom NPC entities with dialogue forms (`server-ui`), attachables, particles. Test bot skins with custom geometry on real clients.
- Stage 4: decision gate. If scripts hit CPU, API, or restart limits, evaluate Endstone on a scratch lab world (Docker image, plugin as the host for the HTTP bridge). Otherwise stay on stock BDS.

Repo and workstream layout:

- Put the pack source in a new repo (`jdwillmsen/lab-mods`, working name), not in `gameops`. Reasons: different release cadence (a pack change restarts the lab), asset-heavy history (binary textures), its own CI (JSON schema checks, Blockbench exports, GameTest), and no coupling to the Go bot module. `gameops` keeps only the Go-side bridge client (the `/scriptevent` sender) and docs, and `jdw-deployments` keeps the chart that mounts the built pack artifact.
- Layout inside the new repo: `behavior_pack/`, `resource_pack/`, `src/` (TypeScript for the Script API compiled by `just-scripts`/tsc, the flow the Microsoft docs recommend), `tests/`, `assets-src/` (Blockbench `.bbmodel` and source art that we created ourselves), `tools/` (UUID/version bump and pack build).
- Until stage 1 delivers an artifact path, the spike can live as a branch of `gameops/docs/arena-bosses-mods` only if kept under the ConfigMap size cap; the new repo is the target.

Licensing and EULA (Minecraft Usage Guidelines and EULA, minecraft.net, searched 2026-10-23; check the live pages before any public release):

- Never commit Mojang's all-rights-reserved assets (vanilla textures, models, sounds, the vanilla behaviour/resource pack). The Microsoft docs hold the JSON schema; the MIT licence on `microsoft/minecraft-samples` (checked 2026-10-23) lets us copy sample code, but still keep the copyright notice and treat any Minecraft-branded art in the samples as unlicensed until checked file by file. Use own art, Blockbench from scratch models, or CC0 sources and record provenance.
- A vanilla-derived entity JSON (copied from the vanilla pack) is Mojang's file; write our own components instead of copying.
- Per the Usage Guidelines quoted in `2026-10-23-mods-spike-unverified-facts.md` (section 5, fetched from the official page on 2026-10-23), a free private lab with your own add-ons and skins is within the rules; the same text counts any server shared with others as commercial use, so follow its attribution rules. Mods may be distributed but modified game/server software may not; do not publish an Endstone/LeviLamina-patched server image.
- Do not tie access to blockchain items or promote unrelated brands with the assets. Not relevant for the lab, listed because the guidelines call them out.
- Keep the whole thing off FWB. The packs must not be mounted on the public server, and the lab pack list must not leak into FWB's world files.

Testing approach:

- Static: `jq`/JSON schema checks of manifests and entity files (the docs repo ships form schemas under `content/forms`), UUID uniqueness and version-bump check in CI, and a content-log scan after every boot (`content-log-file-enabled=true`; fail the rollout on any error line).
- Unit-ish: TypeScript logic for phase state machines kept pure and tested in Node outside Minecraft; GameTest (beta module) for in-engine checks, run on a throwaway BDS container in CI where practical. UNVERIFIED that BDS GameTest can run headless in CI; plan a spike.
- Integration: a Go bot joins the lab, `/summon`s the entity, asserts it appears with the expected type and health via the bot's entity-add packets (`AddActor`), then kills it and asserts the drop and the phase events. This reuses our existing client-side harness and needs no GUI.
- Visual: one manual retail-client check per release (model, animation, HUD bar), with screenshots attached to the PR. Visual regressions cannot be automated cheaply.

Main risks:

1. Beta API drift: any beta Script API call can break on a BDS update. Mitigation: stable-only policy, pin BDS and client versions together, one place per beta call.
2. Restart cost: each pack update kicks players. Mitigation: batch, announce, schedule; consider keeping a second lab pod for pack testing.
3. Pack delivery: ConfigMap 1 MiB cap and RakNet-only pack streaming make large resource packs slow on cold join. Mitigation: stage 1, pack optimiser, small textures.
4. Client cache staleness: forgetting the version bump hides changes. Mitigation: CI check.
5. Custom skin geometry on players may not render on all retail platforms (UNVERIFIED).
6. Licensing mistakes (committed vanilla assets). Mitigation: asset provenance file and a CI grep against a vanilla filename list.
7. Endstone/LeviLamina trail BDS releases and hook the binary; adopting them couples our upgrade cadence to a third party.
8. Boss AI cost: heavy script work in the main thread causes server watchdog stalls; keep per-tick work tiny and push thinking to the Go side over the bridge.

## 6. Source index (all read 2026-10-23)

- Microsoft Learn / `MicrosoftDocs/minecraft-creator` (docs repo main): `BedrockServer/getting-started.md` (updated 2026-01-28), `BedrockServer/scripting.md`, `BedrockServer/server-properties.md`, `BedrockServer/pack-optimization.md`, `Reference/Content/EntityReference/Examples/EntityComponents/minecraftComponent_boss.md`, dragon goals under `EntityGoals/`, `scripting/introduction` (page updated 2026-05-12).
- npm registry dist-tags for `@minecraft/server`, `server-ui`, `server-gametest`, `server-admin`, `server-net`.
- GitHub repos: `EndstoneMC/endstone` (v0.11.14, Apache-2.0), `LiteLDev/LeviLamina` (v26.51.7, LGPL-3.0), `df-mc/dragonfly` (MIT, go.mod), `PowerNukkitX/PowerNukkitX` (3.0.5, LGPL-3.0), `CloudburstMC/Nukkit`, `pmmp/PocketMine-MP` (5.44.3, archived), `Sandertv/gophertunnel` (protocol 2193 = 1.26.50), `itzg/docker-minecraft-bedrock-server` README, `microsoft/minecraft-samples` (MIT).
- minecraft.net Usage Guidelines and EULA (via search summary only; a direct fetch timed out, so confirm wording on the live pages before publishing anything).
