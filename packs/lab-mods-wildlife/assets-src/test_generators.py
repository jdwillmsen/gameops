"""Run as: python3 -I packs/lab-mods-wildlife/assets-src/test_generators.py

-I drops the script directory from sys.path, so it is added back here.
"""
import filecmp
import json
import os
import re
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import build_behavior  # noqa: E402
import build_models  # noqa: E402
import build_textures  # noqa: E402
import bear_rig  # noqa: E402
import cat_rig  # noqa: E402
import hyena_rig  # noqa: E402
import generate  # noqa: E402
from species import SPECIES  # noqa: E402

PACK = os.path.dirname(HERE)
IDS = ("tiger", "cheetah", "lion", "black_leopard", "brown_bear", "hyena")
GROUPS = ("gameops:wild", "gameops:tamed", "gameops:sitting")
ACTIVE_GOALS = tuple("minecraft:behavior." + g for g in (
    "melee_attack", "follow_owner", "owner_hurt_by_target", "owner_hit_target"))
CATS = ("tiger", "cheetah", "lion", "black_leopard")
CAT_TEXTURES = (("tiger", "orange"), ("tiger", "white"), ("cheetah", "default"), ("lion", "male"),
                ("lion", "female"), ("black_leopard", "default"))
OWN_RIGS = {"brown_bear": ("geometry.gameops.brown_bear", bear_rig), "hyena": ("geometry.gameops.hyena", hyena_rig)}
OWN_TEXTURES = (("brown_bear", "default"), ("hyena", "regular"), ("hyena", "alpha"))
EVENTS = ("gameops:on_tamed", "gameops:on_sit", "gameops:on_stand")


class Generators(unittest.TestCase):
    def test_species_table_has_six_animals(self):
        self.assertEqual(sorted(SPECIES), sorted(IDS))
        tiger = SPECIES["tiger"]
        self.assertEqual((tiger.health, tiger.damage, tiger.speed), (60, 8, 0.28))
        self.assertEqual(SPECIES["hyena"].tame_item, "rotten_flesh")
        self.assertEqual(SPECIES["tiger"].variants, ("orange", "white"))
        self.assertEqual(SPECIES["lion"].variants, ("male", "female"))
        self.assertEqual(SPECIES["hyena"].variants, ("regular", "alpha"))

    def test_generated_files_match_committed(self):
        with tempfile.TemporaryDirectory() as tmp:
            written = generate.generate(tmp)
            self.assertTrue(written)
            for rel in written:
                committed = os.path.join(PACK, rel)
                self.assertTrue(os.path.isfile(committed), f"{rel} is not committed")
                self.assertTrue(filecmp.cmp(os.path.join(tmp, rel), committed, shallow=False),
                                f"{rel} differs from a fresh generate")

    def test_group_and_event_names_are_stable(self):
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            for g in GROUPS:
                self.assertIn(g, ent["component_groups"], s.id)
            for e in EVENTS:
                self.assertIn(e, ent["events"], s.id)
            self.assertEqual(
                ent["component_groups"]["gameops:wild"]["minecraft:tameable"]["tame_event"]["event"],
                "gameops:on_tamed", s.id)
        tiger = json.dumps(build_behavior.entity_json(SPECIES["tiger"]))
        self.assertIn('"minecraft:sittable"', tiger)

    def test_every_species_has_tame_item_and_ability(self):
        for s in SPECIES.values():
            self.assertTrue(s.tame_item, s.id)
            self.assertTrue(s.ability, s.id)
            tame = build_behavior.entity_json(s)["minecraft:entity"]["component_groups"]["gameops:wild"][
                "minecraft:tameable"]
            self.assertEqual(tame["tame_items"], [s.tame_item])
            self.assertEqual(tame["probability"], 0.33)

    def load(self, rel):
        with open(os.path.join(PACK, rel), "rb") as f:
            return f.read()

    def committed_entity(self, sid):
        return json.loads(self.load(f"behavior_pack/entities/{sid}.json"))["minecraft:entity"]

    def test_tiger_is_larger_stronger_slower_than_cheetah(self):
        tiger, cheetah = self.committed_entity("tiger"), self.committed_entity("cheetah")
        self.assertGreater(tiger["components"]["minecraft:health"]["max"],
                           cheetah["components"]["minecraft:health"]["max"])
        self.assertGreater(tiger["components"]["minecraft:attack"]["damage"],
                           cheetah["components"]["minecraft:attack"]["damage"])
        self.assertLess(tiger["components"]["minecraft:movement"]["value"],
                        cheetah["components"]["minecraft:movement"]["value"])

    def test_tiger_has_white_variant(self):
        self.assertIn("white", SPECIES["tiger"].variants)
        tiger = self.committed_entity("tiger")
        self.assertIn("gameops:variant_white", tiger["component_groups"])
        orange = self.load("resource_pack/textures/entity/tiger_orange.png")
        white = self.load("resource_pack/textures/entity/tiger_white.png")
        self.assertNotEqual(orange, white)
        client = json.loads(self.load("resource_pack/entity/tiger.entity.json"))
        self.assertIn("white", client["minecraft:client_entity"]["description"]["textures"])
        ctl = json.loads(self.load("resource_pack/render_controllers/tiger.render_controllers.json"))
        self.assertIn("Texture.white", json.dumps(ctl))
        self.assertIn("query.variant", json.dumps(ctl))

    def test_models_are_not_vanilla_cat(self):
        for sid in CATS:
            geo = json.loads(self.load(f"resource_pack/models/entity/{sid}.geo.json"))["minecraft:geometry"][0]
            self.assertEqual(geo["description"]["identifier"], f"geometry.gameops.{sid}")
            expected = list(cat_rig.BONES) + (["mane", "mane_chest"] if sid == "lion" else [])
            self.assertEqual([b["name"] for b in geo["bones"]], expected)
            self.assertEqual(geo["description"]["texture_width"], 128)
            client = json.loads(self.load(f"resource_pack/entity/{sid}.entity.json"))
            self.assertEqual(client["minecraft:client_entity"]["description"]["geometry"]["default"],
                             f"geometry.gameops.{sid}")
            self.assertNotIn("geometry.cat", self.load(f"resource_pack/entity/{sid}.entity.json").decode())

    def test_cheetah_and_tiger_differ_in_silhouette(self):
        t, c = cat_rig.PARAMS["tiger"], cat_rig.PARAMS["cheetah"]
        self.assertGreater(t.body_w * t.body_h * t.body_len, 2 * c.body_w * c.body_h * c.body_len)
        self.assertGreater(t.leg_w, c.leg_w)
        self.assertGreater(c.leg_len / (c.leg_len + c.body_h), t.leg_len / (t.leg_len + t.body_h))
        self.assertGreater(t.head_w, c.head_w)
        self.assertGreater(c.tail_len / c.body_len, t.tail_len / t.body_len)
        self.assertGreater(c.tuck, t.tuck)

    def test_animations_are_shared_and_named_once(self):
        anims = json.loads(self.load("resource_pack/animations/cat.animation.json"))["animations"]
        self.assertEqual(sorted(anims), sorted(build_models.ANIMATION_PREFIX + n for n in build_models.ANIMATION_NAMES))
        self.assertTrue(all(a["bones"] for a in anims.values()))
        self.assertIn("query.modified_distance_moved", json.dumps(anims[build_models.ANIMATION_PREFIX + "walk"]))
        for sid in CATS:
            client = json.loads(self.load(f"resource_pack/entity/{sid}.entity.json"))["minecraft:client_entity"]
            self.assertEqual(sorted(client["description"]["animations"].values()), sorted(anims))

    def test_textures_are_deterministic_128_png(self):
        for sid, variant in CAT_TEXTURES + OWN_TEXTURES:
            a = build_textures.paint(SPECIES[sid], variant)
            self.assertEqual(a, build_textures.paint(SPECIES[sid], variant))
            self.assertEqual(a[:8], b"\x89PNG\r\n\x1a\n")
            self.assertEqual(int.from_bytes(a[16:20], "big"), 128)
            self.assertEqual(int.from_bytes(a[20:24], "big"), 128)

    def test_cat_cubes_do_not_overlap_in_uv(self):
        for params in cat_rig.PARAMS.values():
            rects = [(c.uv[0], c.uv[1], c.uv[0] + 2 * c.size[2] + 2 * c.size[0], c.uv[1] + c.size[2] + c.size[1])
                     for c in cat_rig.layout(params)]
            for i, a in enumerate(rects):
                self.assertLessEqual(a[2], 128)
                self.assertLessEqual(a[3], 128)
                for b in rects[i + 1:]:
                    self.assertFalse(a[0] < b[2] and b[0] < a[2] and a[1] < b[3] and b[1] < a[3])

    def test_ability_speeds_come_from_the_species_table(self):
        for sid, base, active, tired in (("tiger", 0.28, 0.40, 0.18), ("cheetah", 0.45, 0.60, 0.20)):
            s = SPECIES[sid]
            self.assertEqual((s.speed, s.active_speed, s.tired_speed), (base, active, tired))
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            groups, events = ent["component_groups"], ent["events"]
            self.assertEqual(groups["gameops:ability_active"]["minecraft:movement"]["value"], active)
            self.assertEqual(groups["gameops:ability_tired"]["minecraft:movement"]["value"], tired)
            self.assertEqual(groups["gameops:ability_ready"]["minecraft:movement"]["value"], base)
            for e in ("gameops:ability_ready", "gameops:ability_active", "gameops:ability_tired"):
                self.assertIn(e, events)
            self.assertEqual(sorted(ent["events"]["gameops:ability_active"]["add"]["component_groups"]),
                             ["gameops:ability_active"])

    def test_every_sprint_phase_has_an_ability_event(self):
        with open(os.path.join(PACK, "behavior_pack/scripts/abilities.js"), encoding="utf-8") as f:
            phases = set(re.findall(r'phase: "(\w+)"', f.read()))
        self.assertEqual(phases, {"ready", "active", "tired"})
        for sid in ("tiger", "cheetah"):
            events = self.committed_entity(sid)["events"]
            for phase in phases:
                self.assertIn(f"gameops:ability_{phase}", events, sid)

    def test_cheetah_eats_rabbit_and_tiger_beef(self):
        self.assertEqual(SPECIES["cheetah"].tame_item, "rabbit")
        self.assertEqual(SPECIES["tiger"].tame_item, "beef")

    def test_sitting_pet_cannot_attack(self):
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            groups, active = ent["component_groups"], set()

            def fire(name):
                ev = ent["events"][name]
                active.difference_update(ev.get("remove", {}).get("component_groups", []))
                active.update(ev.get("add", {}).get("component_groups", []))

            def components():
                comps = dict(ent["components"])
                for g in sorted(active):
                    comps.update(groups[g])
                return comps

            fire("gameops:on_tamed")
            idle = components()
            for goal in ACTIVE_GOALS:
                self.assertIn(goal, idle, f"{s.id} tamed")
            fire("gameops:on_sit")
            sitting = components()
            for goal in ACTIVE_GOALS:
                self.assertNotIn(goal, sitting, f"{s.id} sitting")
            self.assertIn("minecraft:behavior.stay_while_sitting", sitting, s.id)
            fire("gameops:on_stand")
            for goal in ACTIVE_GOALS:
                self.assertIn(goal, components(), f"{s.id} stood up")
            self.assertNotIn("minecraft:behavior.stay_while_sitting", components(), s.id)

    def test_no_animal_targets_a_player(self):
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            for comps in [ent["components"], *ent["component_groups"].values()]:
                self.assertNotIn("minecraft:behavior.hurt_by_target", comps, s.id)
                for goal in ("owner_hurt_by_target", "owner_hit_target"):
                    for entry in comps.get("minecraft:behavior." + goal, {}).get("entity_types", []):
                        f = entry["filters"]
                        self.assertEqual((f["test"], f["operator"], f["value"]), ("is_family", "!=", "player"))

    def goal_entries(self, sid, group):
        ent = build_behavior.entity_json(SPECIES[sid])["minecraft:entity"]
        goal = ent["component_groups"][group].get("minecraft:behavior.nearest_attackable_target")
        return goal["entity_types"] if goal else []

    @staticmethod
    def families(entries):
        out = []
        for e in entries:
            out += [t["value"] for t in e["filters"]["all_of"] if t["test"] == "is_family"]
        return sorted(out)

    def test_cheetah_hunts_rabbit_and_chicken_only(self):
        self.assertEqual(self.families(self.goal_entries("cheetah", "gameops:wild")),
                         ["chicken", "gameops_hyena", "rabbit"])

    def test_lion_and_hyena_are_rivals_both_ways(self):
        self.assertIn("gameops_hyena", self.families(self.goal_entries("lion", "gameops:wild")))
        for cat in ("lion", "cheetah", "black_leopard"):
            self.assertIn(f"gameops_{cat}", self.families(self.goal_entries("hyena", "gameops:wild")))
            self.assertIn(f"gameops_{cat}", self.families(self.goal_entries("hyena", "gameops:tamed_active")))

    def test_named_animals_are_excluded(self):
        nametag = {"test": "has_nametag", "subject": "other", "operator": "==", "value": False}
        for s in SPECIES.values():
            for e in self.goal_entries(s.id, "gameops:wild"):
                fam = [t["value"] for t in e["filters"]["all_of"] if t["test"] == "is_family"]
                if fam[0] in s.prey:
                    self.assertIn(nametag, e["filters"]["all_of"], f"{s.id} {fam}")

    def test_no_species_targets_player(self):
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            allowed = set(s.prey) | set(s.rivals)
            for comps in [ent["components"], *ent["component_groups"].values()]:
                goal = comps.get("minecraft:behavior.nearest_attackable_target")
                for e in (goal or {}).get("entity_types", []):
                    tests = [e["filters"]] if "test" in e["filters"] else e["filters"]["all_of"]
                    fams = [t["value"] for t in tests if t["test"] == "is_family" and t["operator"] in ("==", "equals")]
                    excl = any(t["test"] == "is_family" and t["operator"] == "!=" and t["value"] == "player"
                               for t in tests)
                    self.assertTrue(excl or (fams and set(fams) <= allowed), f"{s.id} {e}")
                    self.assertNotIn("player", fams, s.id)

    def test_tamed_pets_only_target_wild_rivals_not_prey(self):
        not_tamed = {"test": "has_component", "subject": "other", "operator": "!=", "value": "minecraft:is_tamed"}
        for s in SPECIES.values():
            entries = self.goal_entries(s.id, "gameops:tamed_active")
            self.assertEqual(self.families(entries), sorted(s.rivals), s.id)
            for e in entries:
                self.assertIn(not_tamed, e["filters"]["all_of"], s.id)
        self.assertEqual(self.goal_entries("brown_bear", "gameops:tamed_active"), [])

    def test_sitting_pet_has_no_targeting(self):
        goal = "minecraft:behavior.nearest_attackable_target"
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            self.assertNotIn(goal, ent["components"], s.id)
            self.assertNotIn(goal, ent["component_groups"]["gameops:sitting"], s.id)
            self.assertNotIn(goal, ent["component_groups"]["gameops:tamed"], s.id)
            self.assertEqual(ent["events"]["gameops:on_sit"]["remove"]["component_groups"],
                             ["gameops:tamed_active"], s.id)

    def test_bear_has_no_targeting_goals(self):
        ent = build_behavior.entity_json(SPECIES["brown_bear"])["minecraft:entity"]
        for comps in [ent["components"], *ent["component_groups"].values()]:
            self.assertNotIn("minecraft:behavior.nearest_attackable_target", comps)

    def geo_bones(self, sid):
        geo = json.loads(self.load(f"resource_pack/models/entity/{sid}.geo.json"))["minecraft:geometry"][0]
        return {b["name"]: b for b in geo["bones"]}

    def test_lion_female_has_no_mane(self):
        bones = self.geo_bones("lion")
        self.assertEqual(len(bones["mane"]["cubes"]), 4)
        self.assertEqual(bones["mane"]["parent"], "head")
        self.assertEqual(bones["mane_chest"]["parent"], "body")
        self.assertGreaterEqual(len(bones["mane_chest"]["cubes"]), 2)
        ctl = json.loads(self.load("resource_pack/render_controllers/lion.render_controllers.json"))
        rc = ctl["render_controllers"]["controller.render.gameops.lion"]
        male = SPECIES["lion"].variants.index("male")
        self.assertEqual(SPECIES["lion"].variants.index("female"), 1 - male)
        self.assertIn({"*": True}, rc["part_visibility"])
        for part in ("mane", "mane_chest"):
            self.assertIn({part: f"query.variant == {male}"}, rc["part_visibility"])
        for sid in CATS:
            if sid != "lion":
                self.assertNotIn("mane", self.geo_bones(sid))
                self.assertNotIn("mane_chest", self.geo_bones(sid))
        ent = self.committed_entity("lion")
        for v in ("male", "female"):
            self.assertIn(f"gameops:variant_{v}", ent["component_groups"])
        spawn = ent["events"]["minecraft:entity_spawned"]["sequence"]
        self.assertEqual(len(spawn[1]["randomize"]), 2)

    def test_lion_mane_frames_the_head_and_leaves_the_face_clear(self):
        bones = self.geo_bones("lion")
        head = bones["head"]["cubes"][0]
        hx0, hx1 = head["origin"][0], head["origin"][0] + head["size"][0]
        front = head["origin"][2]
        sides = [c for c in bones["mane"]["cubes"] if c["origin"][0] + c["size"][0] <= hx0 + 0.01
                 or c["origin"][0] >= hx1 - 0.01]
        self.assertEqual(len(sides), 2)
        for c in bones["mane"]["cubes"]:
            self.assertGreater(c["origin"][2], front, "mane must not cover the face")
        top = max(c["origin"][1] + c["size"][1] for c in bones["mane"]["cubes"])
        self.assertGreater(top, head["origin"][1] + head["size"][1])
        bib = min(bones["mane_chest"]["cubes"], key=lambda c: c["origin"][2])
        self.assertLess(bib["origin"][2], bones["body"]["cubes"][0]["origin"][2])

    def test_lion_mane_is_painted_differently_from_the_coat(self):
        male = build_textures.paint(SPECIES["lion"], "male")
        female = build_textures.paint(SPECIES["lion"], "female")
        self.assertNotEqual(male, female)
        self.assertNotEqual(build_textures.COLOURS[("lion", "male")]["mane"],
                            build_textures.COLOURS[("lion", "male")]["base"])

    def test_lion_and_leopard_are_distinct_cats(self):
        P = cat_rig.PARAMS
        vol = lambda p: p.body_w * p.body_h * p.body_len
        self.assertGreater(vol(P["lion"]), vol(P["cheetah"]) * 2)
        self.assertGreater(vol(P["tiger"]), vol(P["lion"]))
        self.assertGreater(P["lion"].head_w, P["lion"].head_h)
        self.assertGreater(P["lion"].chest_len, P["lion"].body_len // 2)
        self.assertTrue(P["lion"].tuft)
        self.assertFalse(P["black_leopard"].tuft)
        lp, ch = P["black_leopard"], P["cheetah"]
        self.assertGreater(lp.body_w, ch.body_w)
        self.assertLess(lp.leg_len / (lp.leg_len + lp.body_h), ch.leg_len / (ch.leg_len + ch.body_h))
        self.assertGreater(lp.tail_len / lp.body_len, 1.0)
        self.assertLess(lp.head_w, P["lion"].head_w)

    def test_lion_tail_has_a_dark_tuft(self):
        names = [c["size"] for c in self.geo_bones("lion")["tail"]["cubes"]]
        self.assertEqual(len(names), 2)
        self.assertEqual(len(self.geo_bones("black_leopard")["tail"]["cubes"]), 1)

    def test_black_leopard_pounces(self):
        ent = self.committed_entity("black_leopard")
        groups = ent["component_groups"]
        for g in ("gameops:wild", "gameops:tamed_active"):
            leap = groups[g]["minecraft:behavior.leap_at_target"]
            self.assertTrue(leap["must_be_on_ground"], g)
            self.assertGreater(leap["yd"], 0, g)
        self.assertNotIn("minecraft:behavior.leap_at_target", groups["gameops:sitting"])
        self.assertNotIn("minecraft:behavior.leap_at_target", ent["components"])
        for sid in IDS:
            if sid != "black_leopard":
                self.assertNotIn("leap_at_target", json.dumps(build_behavior.entity_json(SPECIES[sid])), sid)
        self.assertNotIn("component_groups", ent["events"].get("minecraft:entity_spawned", {}))
        self.assertEqual(SPECIES["black_leopard"].variants, ())
        self.assertNotIn("gameops:ability_active", groups)

    def test_lion_and_leopard_taming_foods_and_stats(self):
        for sid, food, hp, dmg in (("lion", "mutton", 50, 7), ("black_leopard", "chicken", 40, 6)):
            ent = self.committed_entity(sid)
            tame = ent["component_groups"]["gameops:wild"]["minecraft:tameable"]
            self.assertEqual(tame["tame_items"], [food])
            self.assertEqual(ent["components"]["minecraft:health"]["max"], hp)
            self.assertEqual(ent["components"]["minecraft:attack"]["damage"], dmg)
            self.assertNotIn("gameops:ability_active", ent["component_groups"])

    def test_bear_and_hyena_have_their_own_rigs(self):
        names = {}
        for sid, (ident, _) in OWN_RIGS.items():
            geo = json.loads(self.load(f"resource_pack/models/entity/{sid}.geo.json"))["minecraft:geometry"][0]
            self.assertEqual(geo["description"]["identifier"], ident)
            bones = {b["name"]: b for b in geo["bones"]}
            for need in ("body", "head", "tail", "front_left_leg", "front_right_leg", "back_left_leg", "back_right_leg"):
                self.assertIn(need, bones, sid)
            cubes = sorted((tuple(c["size"]), tuple(c["origin"])) for b in geo["bones"] for c in b["cubes"])
            names[sid] = cubes
            for cat in CATS:
                cat_geo = self.geo_bones(cat)
                cat_cubes = sorted((tuple(c["size"]), tuple(c["origin"])) for b in cat_geo.values() for c in b["cubes"])
                self.assertNotEqual(cubes, cat_cubes, f"{sid} is the {cat} model")
            client = json.loads(self.load(f"resource_pack/entity/{sid}.entity.json"))["minecraft:client_entity"]
            self.assertEqual(client["description"]["geometry"]["default"], ident)
        self.assertNotEqual(names["brown_bear"], names["hyena"])

    def test_bear_has_a_shoulder_hump_above_the_back(self):
        bones = self.geo_bones("brown_bear")
        cubes = bones["body"]["cubes"]
        back = max(c["origin"][1] + c["size"][1] for c in cubes if c["size"][1] > 4)
        hump = [c for c in cubes if c["size"][1] <= 4 and c["origin"][1] >= back - 0.01]
        self.assertEqual(len(hump), 1)
        self.assertLess(hump[0]["origin"][2] + hump[0]["size"][2] / 2, 0, "hump sits over the shoulders")
        self.assertLessEqual(max(c["size"][0] for c in bones["head"]["cubes"]), 9)
        legs = bones["front_left_leg"]["cubes"][0]["size"]
        self.assertGreaterEqual(legs[0], 5)
        self.assertLessEqual(legs[1], 10)

    def test_hyena_back_slopes_down_to_the_hips(self):
        bones = self.geo_bones("hyena")
        by_z = sorted((c for c in bones["body"]["cubes"] if c["size"][0] > 3), key=lambda c: c["origin"][2])
        front, rear = by_z[0], by_z[-1]
        self.assertGreater(front["origin"][1] + front["size"][1], rear["origin"][1] + rear["size"][1])
        self.assertLess(bones["back_left_leg"]["cubes"][0]["size"][1], bones["front_left_leg"]["cubes"][0]["size"][1])
        head = bones["head"]["cubes"][0]
        ears = [c for c in bones["head"]["cubes"] if c["size"][2] == 1]
        self.assertEqual(len(ears), 2)
        self.assertGreaterEqual(ears[0]["size"][0], 3)
        self.assertGreater(head["size"][0], bones["tail"]["cubes"][0]["size"][0] * 2)

    def test_bear_and_hyena_textures_differ_from_cats_and_variants(self):
        bear = build_textures.paint(SPECIES["brown_bear"], "default")
        regular = build_textures.paint(SPECIES["hyena"], "regular")
        alpha = build_textures.paint(SPECIES["hyena"], "alpha")
        self.assertEqual(len({bear, regular, alpha}), 3)
        for s_id, v in CAT_TEXTURES:
            self.assertNotEqual(build_textures.paint(SPECIES[s_id], v), bear)

    def test_bear_taming_food_stats_and_no_variants(self):
        ent = self.committed_entity("brown_bear")
        tame = ent["component_groups"]["gameops:wild"]["minecraft:tameable"]
        self.assertEqual(tame["tame_items"], ["salmon"])
        self.assertEqual(ent["components"]["minecraft:health"]["max"], 80)
        self.assertEqual(ent["components"]["minecraft:attack"]["damage"], 9)
        self.assertEqual(ent["components"]["minecraft:movement"]["value"], 0.25)
        self.assertEqual(SPECIES["brown_bear"].variants, ())
        self.assertNotIn("randomize", json.dumps(ent["events"]["minecraft:entity_spawned"]))

    def test_hyena_alpha_has_more_health(self):
        ent = self.committed_entity("hyena")
        self.assertEqual(ent["component_groups"]["gameops:wild"]["minecraft:tameable"]["tame_items"], ["rotten_flesh"])
        self.assertEqual(ent["components"]["minecraft:health"]["max"], 30)
        self.assertEqual(ent["components"]["minecraft:attack"]["damage"], 7)
        self.assertEqual(ent["components"]["minecraft:movement"]["value"], 0.32)
        alpha = ent["component_groups"]["gameops:variant_alpha"]
        self.assertEqual(alpha["minecraft:health"], {"value": 40, "max": 40})
        self.assertEqual(alpha["minecraft:variant"]["value"], 1)
        self.assertNotIn("minecraft:health", ent["component_groups"]["gameops:variant_regular"])
        self.assertEqual(len(ent["events"]["minecraft:entity_spawned"]["sequence"][1]["randomize"]), 2)

    def test_bear_gives_owner_resistance(self):
        with open(os.path.join(PACK, "behavior_pack/scripts/main.js"), encoding="utf-8") as f:
            main = f.read()
        bear = re.search(r'"gameops:brown_bear": \{(.*?)\n  \},', main, re.S).group(1)
        for part in ('interval: 20', 'range: 6', 'effect: "resistance"', 'amplifier: 0', 'duration: 60',
                     'targets: ["owner"]'):
            self.assertIn(part, bear)
        hyena = re.search(r'"gameops:hyena": \{(.*?)\n  \},', main, re.S).group(1)
        for part in ('interval: 40', 'effect: "strength"', 'targets: ["self"]', 'range: 8'):
            self.assertIn(part, hyena)

    def test_sitting_is_readable_from_script(self):
        for s in SPECIES.values():
            ent = build_behavior.entity_json(s)["minecraft:entity"]
            prop = ent["description"]["properties"]["gameops:sitting"]
            self.assertEqual((prop["type"], prop["default"]), ("bool", False), s.id)
            self.assertEqual(ent["events"]["gameops:on_sit"]["set_property"], {"gameops:sitting": True}, s.id)
            self.assertEqual(ent["events"]["gameops:on_stand"]["set_property"], {"gameops:sitting": False}, s.id)


if __name__ == "__main__":
    unittest.main()
