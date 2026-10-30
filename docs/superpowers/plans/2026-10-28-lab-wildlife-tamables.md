# Lab wildlife tamables Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship six tamable animals (tiger, cheetah, lion, black leopard, brown bear, hyena) with their ecosystem to the crew lab as a new `lab-mods-wildlife` pack, delivered by an image so further packs scale, and tested in layers.

**Architecture:** One species table drives generated behavior JSON, models and textures, so tuning is one edit. Abilities that JSON cannot express live in a small pure-function JavaScript module tested with `node --test`. A new `lab-packs` image carries every themed pack and a generic installer replaces the per-pack ConfigMap script. The old `lab-mods-stage0` stays in its ConfigMap; migrating it is a follow-up.

**Tech Stack:** Bedrock entity JSON (format 1.21.0), Script API `@minecraft/server` 2.0.0, Python 3 (generators, `tools/packs/check_packs.py`, unittest), Node 24 (`node --test`), Blockbench via Playwright for model checks, Go (`kinds.go`), Helm chart `charts/crew-lab` in `jdw-deployments`.

**Spec:** `docs/superpowers/specs/2026-10-28-lab-wildlife-tamables-design.md`

## Global Constraints

- Crew lab only; never FWB. Rollout restarts the lab server.
- Entity ids `gameops:<name>`, families `gameops_<name>` plus shared `gameops_wildlife`. Ids: `tiger`, `cheetah`, `lion`, `black_leopard`, `brown_bear`, `hyena`.
- New pack folder `packs/lab-mods-wildlife`; installed under the world as `gameops-lab-mods-wildlife`; four distinct UUIDs; both manifests, the dependency and the world pack lists bumped on every shipped change.
- No animal or pet ever targets or retaliates against a player (family `player`), directly or by `hurt_by_target`.
- Starting stats (tune in play): tiger hp 60 dmg 8 speed 0.28; cheetah hp 30 dmg 5 speed 0.45; lion hp 50 dmg 7 speed 0.30; black_leopard hp 40 dmg 6 speed 0.32; brown_bear hp 80 dmg 9 speed 0.25; hyena hp 30 dmg 7 speed 0.32.
- Taming foods: tiger raw beef, lion raw mutton, cheetah raw rabbit, black_leopard raw chicken, brown_bear raw salmon, hyena rotten flesh.
- Wild predators skip name-tagged animals; wild rivals (lion/hyena; hyena/cheetah; hyena/black_leopard) attack tamed rivals too.
- Variants are cosmetic and random on spawn: lion male/female, tiger orange/white, hyena regular/alpha (alpha +hp only).
- Models never reuse the vanilla cat model. Textures at most 1024x1024.
- Prefer entity JSON; scripts only for what JSON cannot do.
- Commits: Conventional Commits with the attribution trailers from the repo rules; never work on `main`.

## Review Focus

- A player hits a wild animal, or hits a tamed pet's owner: nothing attacks the player (Task 1 checker test; Task 9 preview check).
- A sitting pet must not follow or attack (Task 4 test on the generated JSON).
- A tamed pet must persist: never despawn, survive a pack update (stable component-group and event names; Task 2 test pins them).
- A second player cannot command a pet they do not own (Task 11 manual checklist).
- Sixty persistent animals must not drop the server's tick rate below the lab's baseline (Task 9 scale probe).
- A name-tagged cow near a wild lion is left alone (Task 7 test).

---

## Repos and order

`gameops` (pack, generators, checker, `kinds.go`, `lab-packs` image) first, then `jdw-deployments` (chart, rev tool). Create the implementation worktree from `origin/main`: `git worktree add ~/worktrees/jdwillmsen/gameops/feat/lab-wildlife-pack -b feat/lab-wildlife-pack origin/main`. Use `/usr/bin/git`, not `rtk git`.

### Task 1: Checker policy for wildlife packs

**Files:**
- Modify: `tools/packs/check_packs.py` (`check_behaviour_entity`, `check_pack_dir`)
- Modify: `tools/packs/test_check_packs.py`
- Create: `packs/lab-mods-wildlife/policy.txt` containing `wildlife`

**Interfaces:**
- Produces: `check_behaviour_entity(path, doc, rep, policy="arena")` where policy is `"arena"` (existing rules) or `"wildlife"`; `check_pack_dir` reads `<pack>/policy.txt` (absent means `arena`).
- Wildlife rules: `hurt_by_target` is rejected; any filter in `nearest_attackable_target`, `owner_hurt_by_target`, `owner_hit_target` or `melee_attack` targets that matches family `player` is rejected; `owner_hurt_by_target` and `owner_hit_target` must carry a filter `is_family != player`; every entity belongs to family `gameops_wildlife`; every tameable entity also has `minecraft:behavior.stay_while_sitting` and `minecraft:persistent`.

- [ ] **Step 1: Write failing tests** in `CheckPacks`: `test_wildlife_policy_rejects_hurt_by_target`, `test_wildlife_policy_rejects_player_family_target`, `test_wildlife_owner_goal_needs_player_exclusion`, `test_wildlife_entity_needs_wildlife_family`, `test_tameable_needs_stay_while_sitting_and_persistent`, `test_arena_pack_still_requires_warrior_tag`. Each builds a temp pack copy with `policy.txt` set and asserts the message via `assertFails`.
- [ ] **Step 2: Run** `python3 -I tools/packs/test_check_packs.py` and confirm the new tests fail.
- [ ] **Step 3: Implement** the policy switch above; keep arena behavior identical.
- [ ] **Step 4: Run** the suite; expected all pass including the existing stage0 tests.
- [ ] **Step 5: Commit** `feat(packs): add a wildlife policy to the pack checker`.

### Task 2: Pack scaffold, species table and generators

**Files:**
- Create: `packs/lab-mods-wildlife/{behavior_pack,resource_pack}/manifest.json` (version 0.0.1, four fresh UUIDs, behavior pack script module `scripts/main.js` with dependency `@minecraft/server` `2.0.0`, min_engine 1.21.0)
- Create: `packs/lab-mods-wildlife/assets-src/species.py`, `build_behavior.py`, `build_models.py`, `build_textures.py`, `test_generators.py`
- Modify: `packs/README.md` catalog row

**Interfaces:**
- Produces: `species.py` exports `@dataclass(frozen=True) class Species` (`id`, `family`, `health`, `damage`, `speed`, `tame_item`, `prey: tuple[str,...]`, `rivals: tuple[str,...]`, `variants: tuple[str,...]`, `ability: str`, `rig: str`) and `SPECIES: dict[str, Species]` for the six animals from Global Constraints.
- `build_behavior.entity_json(s: Species) -> dict`, `build_models.geometry(s: Species) -> dict`, `build_textures.paint(s: Species, variant: str) -> bytes` (PNG).
- Stable names (never rename later): component groups `gameops:wild`, `gameops:tamed`, `gameops:sitting`; events `gameops:on_tamed`, `gameops:on_sit`, `gameops:on_stand`.

- [ ] **Step 1: Write failing tests** in `test_generators.py`: `test_species_table_has_six_animals`, `test_generated_files_match_committed` (regenerate to a temp dir and compare byte for byte), `test_group_and_event_names_are_stable` (asserts the exact names above in every generated entity), `test_every_species_has_tame_item_and_ability`.
- [ ] **Step 2: Run** `python3 -I packs/lab-mods-wildlife/assets-src/test_generators.py`; expected FAIL (modules missing).
- [ ] **Step 3: Implement** `species.py` and `build_behavior.py` first; the entity JSON follows the vanilla wolf's data: `minecraft:tameable` (probability 0.33, `tame_items`, `tame_event` `gameops:on_tamed`), `minecraft:is_tamed` in group `gameops:tamed`, `follow_owner`, `sittable`, `stay_while_sitting`, `owner_hurt_by_target` and `owner_hit_target` with the player exclusion, `minecraft:persistent` in the tamed group, `minecraft:behavior.avoid_mob_type` for players while wild, a `variant`/`mark_variant` component group per variant chosen by `minecraft:randomize`.
- [ ] **Step 4: Run** `tools/check-packs.sh packs/lab-mods-wildlife` and the generator tests; expected pass once Tasks 4-6 add art, so here only the behavior half is required (stub models allowed behind a clearly named `TODO` file that Task 4 removes).
- [ ] **Step 5: Commit** `feat(packs): scaffold the wildlife pack and species table`.

### Task 3: Abilities module

**Files:**
- Create: `packs/lab-mods-wildlife/behavior_pack/scripts/abilities.js`, `abilities.test.js`, `main.js`

**Interfaces:**
- Produces (pure, no `@minecraft/server` import in `abilities.js`):
  - `sprintPhase(state, tick, cfg) -> {phase: "ready"|"active"|"tired", until: number}`; cfg `{activeTicks, tiredTicks}`.
  - `packDamageMultiplier(nearbyPets, cfg) -> number` (1 for 0-1 pets, `1 + cfg.perPet * (n - 1)` capped at `cfg.max`).
  - `isNight(timeOfDay) -> boolean` (true for 13000-23000).
  - `inRoarRange(a, b, range) -> boolean` (3-D distance).
- `main.js` wires them to entities and subscribes to `world.afterEvents`; it also registers `system.afterEvents.scriptEventReceive` ids `gameops:tame` (tames the nearest wildlife animal to the caller via `EntityTameableComponent.tame(player)`) and `gameops:perf` (logs mean tick interval over 200 ticks). These two are test hooks and are listed in the README.

- [ ] **Step 1: Write failing tests** in `abilities.test.js` with `node:test`: `sprintPhase goes ready to active to tired to ready` (cfg 160/160 ticks), `packDamageMultiplier is 1 for one pet and caps at max`, `isNight is false at 6000 and true at 18000`, `inRoarRange is inclusive at the boundary`.
- [ ] **Step 2: Run** `node --test packs/lab-mods-wildlife/behavior_pack/scripts/`; expected FAIL.
- [ ] **Step 3: Implement** the four functions; then `main.js`.
- [ ] **Step 4: Run** the node tests; expected pass.
- [ ] **Step 5: Commit** `feat(packs): add the wildlife ability module and test hooks`.

### Task 4: Tiger and cheetah (vertical slice)

**Files:**
- Create: `packs/lab-mods-wildlife/assets-src/cat_rig.py` (shared four-legged skeleton: bones `body`, `head`, `tail`, `front_left_leg`, `front_right_leg`, `back_left_leg`, `back_right_leg`, with per-species proportion parameters)
- Modify: `build_models.py`, `build_textures.py`, `build_behavior.py`
- Generate: `behavior_pack/entities/{tiger,cheetah}.json`, `resource_pack/{entity,models/entity,animations,render_controllers,textures/entity}/` files, `texts/en_US.lang`
- Test: `test_generators.py`

**Interfaces:**
- Produces: `cat_rig.build(params: CatParams) -> dict` returning a geometry; shared animations `animation.gameops.cat.{walk,sit,idle,attack}` used by every cat.
- Abilities: tiger `charge` (short speed burst then rest), cheetah `sprint` via `sprintPhase`, implemented in `main.js` by toggling a component group's speed.

- [ ] **Step 1: Write failing tests**: `test_tiger_is_larger_stronger_slower_than_cheetah` (reads the generated JSON: tiger health > cheetah health, damage greater, `minecraft:movement` lower), `test_tiger_has_white_variant`, `test_models_are_not_vanilla_cat` (geometry identifier starts `geometry.gameops.`).
- [ ] **Step 2: Run** the generator tests; expected FAIL.
- [ ] **Step 3: Implement** the rig, textures (stripes; spots) and client entity files, then regenerate and commit generated output.
- [ ] **Step 4: Run** `tools/check-packs.sh` and the generator tests; expected pass.
- [ ] **Step 5: Look at them:** load each `.geo.json` and texture into web.blockbench.net with Playwright (drop the geo alone first, then the PNG and choose "Import Texture"), screenshot from three angles, fix proportions until each reads as its animal.
- [ ] **Step 6: Commit** `feat(packs): add the tamable tiger and cheetah`.

### Task 5: Lion and black leopard

Same shape as Task 4. Lion: male/female variants (mane geometry bone `mane` shown only on male via a render-controller array), `roar` aura through `minecraft:mob_effect` (Strength, range 16, owner and tamed wildlife only). Black leopard: `ambush` (damage multiplier on the first hit or at night via `isNight`) and `pounce` through `minecraft:behavior.leap_at_target`. Tests: `test_lion_female_has_no_mane`, `test_black_leopard_pounces`. Commit `feat(packs): add the tamable lion and black leopard`.

### Task 6: Brown bear and hyena

Own rigs (`bear_rig.py` with a shoulder hump; `hyena_rig.py` with a sloping back). Bear: `minecraft:mob_effect` Resistance for the owner within 6 blocks. Hyena: `packDamageMultiplier` from nearby tamed pets sharing a target; alpha variant +hp. Tests: `test_bear_gives_owner_resistance`, `test_hyena_alpha_has_more_health`. Commit `feat(packs): add the tamable brown bear and hyena`.

### Task 7: Ecosystem relations

**Files:** Modify `build_behavior.py`, `species.py`, `test_generators.py`.

**Interfaces:** Consumes `Species.prey` and `Species.rivals`; each wild entity gets one `nearest_attackable_target` entry per prey family and rival family with filter exclusions: skip name-tagged animals (verify the exact filter name against the Bedrock entity filter docs before use; if no filter exists, enforce it in `main.js`), and never `player`.

- [ ] **Step 1: Write failing tests**: `test_cheetah_hunts_rabbit_and_chicken_only`, `test_lion_and_hyena_are_rivals_both_ways`, `test_named_animals_are_excluded`, `test_no_species_targets_player`.
- [ ] **Step 2: Run** the generator tests; expected FAIL.
- [ ] **Step 3: Implement** and regenerate.
- [ ] **Step 4: Run** `tools/check-packs.sh`; expected pass under the wildlife policy.
- [ ] **Step 5: Commit** `feat(packs): wire the wildlife food web and rivalries`.

### Task 8: Bots

> Superseded: the ids are left unlisted (`other`), not `provoked`, so a hurt unit never blames a nearby pet; test is `TestWildlifeIsNeverASuspect`.

**Files:** Modify `minecraft/crew/internal/life/kinds.go` and the kinds test (`kinds_boss_test.go` style).

**Interfaces:** each `gameops:<animal>` maps to `provoked` in `kinds`.

- [ ] **Step 1: Write the failing test** `TestWildlifeKindsAreProvoked` iterating the six ids.
- [ ] **Step 2: Run** `go test ./minecraft/crew/internal/life/ -run Wildlife`; expected FAIL.
- [ ] **Step 3: Add** the six entries beside the other pack mobs.
- [ ] **Step 4: Run** the package tests; expected pass.
- [ ] **Step 5: Commit** `feat(crew): treat wildlife as provoked`.

### Task 9: Preview smoke and scale test

**Files:** Create `tools/lab-preview-smoke.sh`; modify `tools/lab-preview.test.sh` only if its harness needs a hook; update `packs/lab-mods-wildlife/README.md`.

**Interfaces:** `tools/lab-preview-smoke.sh [--count N]` runs on the lab's Bedrock version (`LAB_PREVIEW_VERSION=1.26.60.6`): `up`, summon N of each animal, wait 60 s, assert all alive, the content log has no error lines, and (with `gameops:perf`) that mean tick interval stays within 10 percent of an empty-server baseline taken first. Default N = 10 (sixty animals).

- [ ] **Step 1: Write the script** with each assertion printing `PASS`/`FAIL`.
- [ ] **Step 2: Run** it with N = 1; expected all PASS.
- [ ] **Step 3: Run** it with N = 10; record the tick numbers in the PR body.
- [ ] **Step 4: Run the manual checklist** with a real client: tame each (`gameops:tame` hook or food), follow, sit and stay, defend, each ability, rivalry fights, a hit on a wild animal does not hurt the player, a second player cannot sit another's pet.
- [ ] **Step 5: Commit** `test(packs): add the wildlife preview smoke and scale test`.

### Task 10: The `lab-packs` image

**Files:** Create `minecraft/lab-packs/{component.yaml,Dockerfile,install.sh,test-install.sh}`

**Interfaces:**
- Image `ghcr.io/jdwillmsen/lab-packs`, tag from the component's semver. Contains `/packs/<theme>/{behavior_pack,resource_pack}` for every folder under `packs/` that is not stage0, and `/install.sh <world-dir>`.
- `install.sh` for each theme: replace `behavior_packs/gameops-<theme>` and `resource_packs/gameops-<theme>`, then register each manifest's header uuid and version in `world_behavior_packs.json` and `world_resource_packs.json`, keeping other entries. Read UUIDs from the manifests, so adding a theme needs no script edit.
- `component.yaml`: `kind: service`, `release: {tag: lab-packs, artifacts: [image]}`, tasks `test: tools/check-packs.sh && python3 -I tools/packs/test_check_packs.py && node --test packs/ && minecraft/lab-packs/test-install.sh`.

- [ ] **Step 1: Write failing `test-install.sh`**: runs `install.sh` twice on a temp world holding a pre-existing unrelated pack entry; asserts both lists contain the wildlife uuids once, the unrelated entry survives, and a second run is idempotent.
- [ ] **Step 2: Run** it; expected FAIL.
- [ ] **Step 3: Implement** `install.sh` (sh plus `jq`) and the Dockerfile (small base with `jq`, pinned by digest like the other Dockerfiles).
- [ ] **Step 4: Run** `test-install.sh` and `go run ./tools/components` affected checks as CI does; expected pass.
- [ ] **Step 5: Commit** `feat(lab-packs): add the lab pack image and installer`. Open the gameops PR; merge after review; the release workflow publishes `lab-packs` at its first version.

### Task 11: Chart delivery in `jdw-deployments`

**Files:** Modify `charts/crew-lab/values.yaml` (second init container `install-lab-packs`, after `install-unit-kit`, running `/install.sh /data/worlds/crew-lab` from the pinned `ghcr.io/jdwillmsen/lab-packs:<tag>`, read-only root, same resource limits); modify `tools/crew-lab-kit-rev.py` so the revision also hashes the `install-lab-packs` init container; modify `tools/tests/test-crew-unit-kit.sh` to assert the new container and that `UNIT_KIT_REV` is current; update `charts/crew-lab/README.md`.

**Interfaces:** `revision()` hashes both init containers; the tag is a literal string (init containers are not templated), moved only deliberately.

- [ ] **Step 1: Write the failing chart test** assertions (container present and ordered after `install-unit-kit`; image tag pinned; rev current).
- [ ] **Step 2: Run** `tools/tests/test-crew-unit-kit.sh`; expected FAIL.
- [ ] **Step 3: Implement** the values change and the rev tool; run `tools/crew-lab-kit-rev.py --write`.
- [ ] **Step 4: Run** the chart test and `helm template`; expected pass.
- [ ] **Step 5: Commit** `feat(crew-lab): install wildlife packs from the lab-packs image`. Open the PR.

### Task 12: Rollout

Not started without the owner's go-ahead for the restart. The unit-kit gate holds the change until nobody is online; forcing past it needs the owner's OK each time, with a `say` warning first (`docs` of the lab add-on memory describe the procedure). After sync: confirm the content log shows both packs loaded with no errors, summon one of each, tame one, and confirm the crew units stayed connected. Record the live check in the PR.

---

## Self-review

- Spec coverage: roster (Tasks 2-6), taming (2), ecosystem (7), look and Blockbench (4-6), checker rule (1), bots (8), delivery and tests (9-11), rollout (12). Biome spawning, breeding, riding are non-goals and absent.
- Names: `Species`, `sprintPhase`, `packDamageMultiplier`, group and event names are the same wherever used.
- Proportion: a plan, not the code; bodies are left to the implementer.
