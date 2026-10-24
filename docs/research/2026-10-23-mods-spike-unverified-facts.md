# Mods spike JDW-204: the UNVERIFIED facts, settled live (2026-10-23)

Follow-up to `2026-10-23-lab-mods-and-custom-characters.md`. Probed on a throwaway
`itzg/minecraft-bedrock-server:2026.10.1` container (`mods-spike-204`, BDS
**1.26.52.3**, `Branch: r/26_u5`), creative, online-mode off, allow-list off, data in
`/tmp/mods-spike-204`, removed afterwards. Console commands were fed through
`docker attach` (the image's `send-command` could not find the process from this
sandbox). Note the lab chart still pins image `2026.9.2`; re-run the Q1/Q2 checks when it moves.

## Results

| # | Question | Verdict | Evidence (short) |
|---|---|---|---|
| 1 | `/reload` applies a changed behaviour-pack **entity** JSON | **FAILED** (restart required) | health 20 to 40 then `reload all`: new mob still died at 25 damage. Restart: new mob survived 25, died at 45. |
| 1b | `reload all` applies changed **script** files | **VERIFIED** | `main.js` edited, `reload all` logged `[scr] script loaded REV2` and `Function and script files have been reloaded.` |
| 2 | `@minecraft/server` stable in a pack, `system.runInterval`, `/scriptevent` | **VERIFIED** | see 2 below. Max stable `@minecraft/server` here is **2.10.0**; no beta needed. |
| 3 | Large resource pack accepted by server | **VERIFIED server side**; client delivery **NOT TESTABLE** | 4.7 MB and 28 MB packs start with no warning. No retail client; a gophertunnel bot could not reach the server from this sandbox. |
| 4 | Headless GameTest | **VERIFIED** (console), CI wrapper unproven | `gametest run lab:probe_spawns` printed `onTestPassed`. Needs Beta APIs experiment in `level.dat`. |
| 5 | Mojang guidelines for a private free lab | **VERIFIED** (pages fetched) | see 5 below. A free shared server counts as "commercial use" under the guidelines, so follow the attribution rules. |

## 1. Reload vs restart (entity JSON)

Pack `probe` (`lab:probe_mob`, `minecraft:health` 20, physics, nameable), `damage`
thresholds on a stone pad inside a ticking area.

- v1, health 20: `damage 19` then `testfor` found it; `damage 2` then `testfor` ERROR `No targets matched selector` (dead).
- Edited to `"value":40,"max":40`, `reload all` (also plain `reload`): log `Function and script files have been reloaded.` Summoned v2: `damage 25` then `testfor` ERROR (dead). Still 20 HP.
- Edited to 60 with manifest version bumped to 1.0.1 and `world_behavior_packs.json` updated, `reload all`: v4 died at `damage 50`, so it still had the old 40 HP. A version bump does not make reload pick up entity files.
- `docker restart`: v3 survived `damage 25`, died after `damage 20` more (45 >= 40). Restart applies it.
- Restart cost on this tiny world: `Starting Server 21:15:59:693` to `Server started 21:16:00:884` (1.2 s); `docker restart` wall 3.5 s. A `summon` right after start failed with `Unable to summon object` (ticking area not loaded yet); it worked when retried about 25 s later. The real lab world and k8s pod start (probes, 30 s `initialDelaySeconds`) will be far slower; players are kicked either way.
- Consequence: a changed entity JSON needs a restart (tested). Whether spawn-rule or loot JSON changes reload without a restart was not tested and is UNVERIFIED; do not assume a general manifest rule. Script-only changes can go through `reload all`; a console-driven `/reload` is a live hot-fix path for logic, not entity data.

## 2. Script API

Manifest (pack `scr`): module `{"type":"script","language":"javascript","entry":"scripts/main.js"}` and
`"dependencies":[{"module_name":"@minecraft/server","version":"2.0.0"}]`. No experiment toggled. Log:

```
Pack Stack - [01] scr (id: 3333..., version: 1.0.0) @ worlds/w/behavior_packs/scr
WARN [Scripting] [scr] script loaded
WARN [Scripting] [scr] tick-interval fired 4          (runInterval 100 ticks, 20 s after start)
scriptevent lab:ping hello
INFO Script event lab:ping has been sent
WARN [Scripting] [scr] scriptevent id=lab:ping msg=hello source=Server
```

- `scriptevent other:ping nope` was filtered out by `namespaces:["lab"]` (no handler line). The handler also incremented scoreboard `ping`; `scoreboard players test count ping 1 1` returned `Score 1 is in range 1 to 1`.
- Versions on BDS 1.26.52.3 (restart per value): `2.0.0`, `2.2.0`, `2.10.0` load. `2.11.0`, `2.12.0`, `2.13.0`, `9.9.0` fail with `requested invalid version [X] of [@minecraft/server]` and no script runs. `2.0.0-beta` and `2.13.0-beta` fail with `requesting dependency on beta APIs [...], but the Beta APIs experiment is not enabled.`
- Pin `"version":"2.10.0"` (or lower) for this BDS; npm `latest` happened to equal 2.10.0 and matches. A pack asking for a newer stable than the server fails closed, so pin BDS and manifest together.
- `config/default/permissions.json` allows server, server-ui, server-admin, server-gametest, server-editor, debug-utilities by default (not server-net).
- The existing crew-lab `crew-unit-kit` pack already ships `scripts/main.js`, so the delivery path is proven in production for scripts.

## 3. Pack size and delivery

- Two generated resource packs (random-pixel PNGs, incompressible): `rp5` 4.7 MB, `rp30` 28 MB, both listed in `world_resource_packs.json`, with `TEXTUREPACK_REQUIRED=true`. Startup log showed **no warning and no pack line**: the `Pack Stack` lines list behaviour packs only. By contrast a registered but missing pack logs `WARN Configured pack (id: cccccccc-..., version: 1.0.0) was not found and was ignored`. So absence of that WARN is the only server-side acceptance signal; startup time was unchanged (about 1.2 s). BDS does not state a size cap in its logs or `server.properties`.
- Client side is NOT TESTABLE: no retail client. A gophertunnel bot (own module, v1.62.0) was run from the host and from a container sharing the server's network namespace; both failed at `discover mtu` (`i/o timeout` / `connection refused`) even though `mc-monitor status-bedrock` inside the container worked. Cause not found; I did not chase it. Pack transfer per cold join still goes over RakNet (research note section 2.4), so egress is about the pack size per new client.
- ConfigMap consequence: the object limit is 1 MiB (1,048,576 bytes) including base64 in `binaryData` (+33 %), so about 780 KB of raw bytes at most. 4.7 MB is 6x too big; the existing `crew-lab-unit-kit` ConfigMap (15 KB template plus a `kits.tar.gz`) is fine, a textured pack is not. A gzipped tar does not shrink PNG textures much.
- Lowest-effort alternative, matching `charts/crew-lab/values.yaml` (`initContainers: install-unit-kit` writing under `$world/behavior_packs/$pack`, then merging with `jq` into `world_behavior_packs.json`): keep the init container and replace the ConfigMap mount with a download. Sketch:
  - CI in the mods repo builds `lab-mods-<ver>.tar.gz` (with `behavior_pack/` and `resource_pack/`) and attaches it plus a `.sha256` to a GitHub Release.
  - Values: `LAB_MODS_URL`/`LAB_MODS_SHA256` env on the init container (bump = restart, same trick as `UNIT_KIT_REV`).
  - Script: `curl -fsSL "$LAB_MODS_URL" -o /tmp/m.tgz; echo "$LAB_MODS_SHA256  /tmp/m.tgz" | sha256sum -c; tar -xzf /tmp/m.tgz -C "$world"` then the existing `jq` merge, adding a `world_resource_packs.json` merge for the resource pack uuid. The `itzg` image has `curl`/`jq`; the root fs is read-only, so use the `/data` volume or an `emptyDir` for the temp file.
  - Fallback if egress to GitHub from the pod is unwanted: an NFS/PVC volume holding the extracted pack, mounted read-only and copied in by the same script.

## 4. GameTest headless

- BDS binary contains `DedicatedServerGameTestRunner`, `test-results-gametest.json` and a `test_config.json` reader (strings in the 1.26.52.3 binary; the config schema is not documented and was not exercised).
- The `gametest` console command exists without any pack: `gametest run foo:bar` gave `Could not find test with name 'foo:bar'`.
- Registering a test needs the beta module: manifest dependency `{"module_name":"@minecraft/server-gametest","version":"1.0.0-beta"}` fails with `requesting dependency on [@minecraft/server-gametest - 1.0.0-beta], but the Beta APIs experiment is not enabled.` No server property exists for it. I enabled it by adding an `experiments` compound with `gametest:1b` to `level.dat` (script `/tmp/mods-spike-tools/patch_level.py`, server stopped first); the log then showed `Experiment(s) active: gtst`.
- Test `gt.register("lab","probe_spawns", t => { t.spawn("lab:probe_mob",{x:1,y:2,z:1}); t.succeedWhenEntityPresent(...) }).structureName("lab:empty_pad")`. Without a structure: `onTestFailed: ... Failed to spawn test structure with path 'structures/lab/empty_pad.mcstructure'`. Note `structure save ... disk` stores into the world db, not a file; the test needs a real `.mcstructure` in the pack (I generated a 3x3x3 air one with a small NBT writer). With the file: `execute positioned 20 126 20 run gametest run lab:probe_spawns` then `onTestStructureLoaded` and `onTestPassed: lab:probe_spawns`.
- CI usability: yes in principle. A throwaway container, level.dat pre-patched, test packs plus structures mounted, console fed via `docker attach`, assertions by grepping `onTestPassed|onTestFailed` from `docker logs`. Caveats: beta module (re-verify per BDS bump), no exit code from the command, level.dat patch is a hack (ship a template world with the experiment on instead), and the real test-runner config (`test_config.json`) is a possible cleaner route that is unexplored.

## 5. Mojang guidelines (fetched 2026-10-23 with curl, WebFetch timed out)

URLs: https://www.minecraft.net/en-us/usage-guidelines (also https://aka.ms/mcusageguidelines) and https://www.minecraft.net/en-us/eula. Guidelines footer: "(c) 2026 Mojang AB"; no explicit revision date on either page. They state: "These guidelines may change as time goes by."

- Personal use: "We are very relaxed about things you create for yourself. Pretty much anything goes there ... just remember the policies and don't do anything illegal or infringing on others."
- Sharing counts as commercial: "In these guidelines, commercial use means any uses of our name, brand, or assets that you use and share with others (regardless of whether you receive payment or provide it for free). This can be through videos and streams you create, merchandise you make, or even a server you host." So a lab shared with friends is "commercial use" in their sense, even free; it must follow the rules below, which a private non-monetised lab does.
- Servers: "You may host a server for Minecraft. You may even charge for access ..." Access "Must only be granted to users who have a genuine paid-for version of Minecraft". We charge nothing and do not hide EULA dialogs.
- Mods and add-ons: "By modifications ('mods') we mean an original plug-in that extends the functionality of Minecraft but that doesn't contain a substantial part of our copyrightable code or content." "You may create, use, or distribute a mod if: You distribute the mod only and not a modded version of Minecraft", no play-to-earn, and no NFT-style ownership checks.
- EULA: "Any Mods you create for Minecraft: Java Edition from scratch belong to you ... as long as you don't sell them for money / try to make money from them and so long as you don't distribute Modded Versions of the game." (the EULA wording names Java Edition; the usage guidelines apply to the game generally). "Do not distribute or make commercial use of anything we've made without our permission."
- Do not: "redistribute our games or any alterations of our games or game files"; "pretend to be / associated with / supported by Mojang or Microsoft and make it clear: You (not us) are responsible for the product or service (this includes events, servers ...)", who owns it and whom to contact.
- Skins and content: "Your content is yours, but please share it responsibly and safely"; we "own things that are copies (or substantial copies) or derivatives of our property" (vanilla textures and character skins stay Mojang's). Original custom skins and models are fine; a skin pack with brand logos is called out as not allowed for companies.
- The Bedrock Dedicated Server download carries its own licence terms (Minecraft EULA plus Microsoft software terms); not fetched here. Do not publish a modified BDS image, per the existing research note.

Practical reading for the lab: own-made behaviour/resource packs, own skins and models are within the rules; never commit or redistribute vanilla assets or vanilla-derived entity files; keep access to owners of legitimate copies; keep the lab non-monetised; add a short "unofficial, run by <owner>, contact <address>" notice to the lab's public description if it is ever shared beyond the crew.

## Recommendation for stage-0 custom entity (JDW-205)

Deliver the entity as a behaviour pack plus resource pack through the **init-container-plus-release-tarball** path (section 3), reusing the unit-kit jq merge and a revision env var, because a textured pack cannot fit a 1 MiB ConfigMap; for a first spike with a plain-colour 64x64 texture (tens of KB) the existing ConfigMap mechanism is still enough and is the quickest way to prove the entity. Budget one restart (kicks players, tens of seconds to minutes on the real world) for every entity JSON, texture or manifest change, and bump the manifest version every time so clients refresh their cache; put logic in a script module pinned to `@minecraft/server` 2.10.0 so it can be hot-fixed with `reload all` and driven from Go via `/scriptevent lab:<cmd>`. CI should: (1) JSON/manifest/uuid/version-bump checks; (2) boot a throwaway BDS from the built tarball and fail on any `ERROR` or `Configured pack ... not found` line; (3) summon `lab:...` and prove stats with `damage` thresholds as above; (4) send `scriptevent lab:ping` and assert the handler log; (5) a GameTest (needs beta toggle and a pack `.mcstructure`) once the entity has behaviour worth asserting. Still manual: a retail client confirming the model and texture render, and a Go bot completing the pack handshake under `texturepack-required` (not testable from this sandbox; investigate the bot connectivity failure first).

## Appendix: reproduce

Scratch files: `/tmp/mods-spike-204` (world and packs, container removed), `/tmp/mods-spike-tools/{patch_level.py,mkstruct.py,mkpng.py,bot}`. Docker run flags: `-i` (needed for console via attach), `-e ALLOW_LIST=false` (otherwise BDS refuses to start: `Using an allowlist without online authentication ... is not allowed`), `-p 19244:19132/udp`.
