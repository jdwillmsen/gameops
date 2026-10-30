"""Run as: python3 -I tools/packs/test_check_packs.py

-I drops the script directory from sys.path, so the checker is loaded by path.
"""
import importlib.util
import json
import os
import shutil
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SPEC = importlib.util.spec_from_file_location("check_packs", os.path.join(HERE, "check_packs.py"))
check_packs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check_packs)

PACK = os.path.join(HERE, "..", "..", "packs", "lab-mods-stage0")
BP_ENTITY = os.path.join("behavior_pack", "entities", "training_dummy_brute.json")
RP_ENTITY = os.path.join("resource_pack", "entity", "training_dummy_brute.entity.json")


class CheckPacks(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.pack = os.path.join(self.tmp, "p")
        shutil.copytree(PACK, self.pack)

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def edit(self, rel, fn):
        path = os.path.join(self.pack, rel)
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
        fn(doc)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(doc, f)

    def errors(self):
        rep = check_packs.Report()
        check_packs.check_pack_dir(self.pack, rep)
        return rep.errors

    def assertFails(self, needle):
        errs = self.errors()
        self.assertTrue(any(needle in e for e in errs), f"{needle!r} not in {errs}")

    def test_shipped_pack_is_clean(self):
        self.assertEqual(self.errors(), [])

    def test_broken_json(self):
        with open(os.path.join(self.pack, BP_ENTITY), "w") as f:
            f.write("{")
        self.assertFails("not valid JSON")

    def test_module_uuid_equal_to_header(self):
        def fn(d):
            d["modules"][0]["uuid"] = d["header"]["uuid"]
        self.edit(os.path.join("behavior_pack", "manifest.json"), fn)
        self.assertFails("UUIDs reused")

    def test_behaviour_pack_may_carry_a_script_module_but_resource_pack_may_not(self):
        script = {"type": "script", "language": "javascript", "entry": "scripts/main.js",
                  "uuid": "00000000-0000-4000-8000-000000000001", "version": [0, 0, 1]}
        self.edit(os.path.join("behavior_pack", "manifest.json"), lambda d: d["modules"].append(dict(script)))
        self.assertEqual(self.errors(), [])
        self.edit(os.path.join("resource_pack", "manifest.json"), lambda d: d["modules"].append(dict(script)))
        self.assertFails("modules[1].type must be 'resources'")

    def test_uuid_shared_between_packs(self):
        def fn(d):
            d["header"]["uuid"] = "40cf3288-abac-4a12-b49b-20c8b46a2d42"
        self.edit(os.path.join("resource_pack", "manifest.json"), fn)
        self.assertFails("UUIDs reused")

    def test_dependency_must_match_resource_header(self):
        def fn(d):
            d["dependencies"][0]["uuid"] = "00000000-0000-4000-8000-000000000000"
        self.edit(os.path.join("behavior_pack", "manifest.json"), fn)
        self.assertFails("must depend on the resource pack header UUID")

    def test_wrong_module_type(self):
        def fn(d):
            d["modules"][0]["type"] = "resources"
        self.edit(os.path.join("behavior_pack", "manifest.json"), fn)
        self.assertFails("must be 'data'")

    def test_missing_min_engine_version(self):
        def fn(d):
            del d["header"]["min_engine_version"]
        self.edit(os.path.join("resource_pack", "manifest.json"), fn)
        self.assertFails("min_engine_version")

    def test_wrong_format_version(self):
        def fn(d):
            d["format_version"] = 1
        self.edit(os.path.join("behavior_pack", "manifest.json"), fn)
        self.assertFails("format_version must be 2")

    def test_truncated_png_is_reported(self):
        path = os.path.join(self.pack, "resource_pack", "textures", "entity", "training_dummy_brute.png")
        with open(path, "rb") as f:
            data = f.read()
        with open(path, "wb") as f:
            f.write(data[:45])
        self.assertFails("truncated")

    def test_any_of_filter_rejected(self):
        def fn(d):
            comp = d["minecraft:entity"]["components"]["minecraft:behavior.nearest_attackable_target"]
            f = comp["entity_types"][0]["filters"]
            comp["entity_types"][0]["filters"] = {"any_of": [f]}
        self.edit(BP_ENTITY, fn)
        self.assertFails("any_of")

    def test_self_subject_rejected(self):
        def fn(d):
            comp = d["minecraft:entity"]["components"]["minecraft:behavior.nearest_attackable_target"]
            comp["entity_types"][0]["filters"]["subject"] = "self"
        self.edit(BP_ENTITY, fn)
        self.assertFails("subject 'other'")

    def test_identifier_mismatch(self):
        def fn(d):
            d["minecraft:client_entity"]["description"]["identifier"] = "gameops:other"
        self.edit(RP_ENTITY, fn)
        self.assertFails("entity identifiers differ")

    def test_missing_texture(self):
        os.remove(os.path.join(self.pack, "resource_pack", "textures", "entity", "training_dummy_brute.png"))
        self.assertFails("does not exist")

    def test_uv_overlap_rejected(self):
        def fn(d):
            bones = d["minecraft:geometry"][0]["bones"]
            bones[1]["cubes"][0]["uv"] = list(bones[0]["cubes"][0]["uv"])
        self.edit(os.path.join("resource_pack", "models", "entity", "ground_dragon.geo.json"), fn)
        self.assertFails("overlap")

    def test_uv_outside_texture_rejected(self):
        def fn(d):
            d["minecraft:geometry"][0]["bones"][0]["cubes"][0]["uv"] = [250, 250]
        self.edit(os.path.join("resource_pack", "models", "entity", "ground_dragon.geo.json"), fn)
        self.assertFails("leaves the 256x256 texture")

    def test_boss_component_rejected(self):
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"]["components"].update({"minecraft:boss": {}}))
        self.assertFails("minecraft:boss")

    def test_player_target_rejected(self):
        def fn(d):
            comp = d["minecraft:entity"]["components"]["minecraft:behavior.nearest_attackable_target"]
            comp["entity_types"][0]["filters"] = {"all_of": [
                {"test": "has_tag", "subject": "other", "value": "arena_warrior"},
                {"test": "is_family", "subject": "other", "value": "player"}]}
        self.edit(BP_ENTITY, fn)
        self.assertFails("never select family player")

    def test_missing_warrior_tag_filter(self):
        def fn(d):
            comp = d["minecraft:entity"]["components"]["minecraft:behavior.nearest_attackable_target"]
            comp["entity_types"][0]["filters"] = {"test": "is_family", "subject": "other", "value": "zombie"}
        self.edit(BP_ENTITY, fn)
        self.assertFails("has_tag arena_warrior")

    def test_melee_without_attack_component(self):
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"]["components"].pop("minecraft:attack"))
        self.assertFails("never hurts anything")

    def make_wildlife(self, fn=None):
        with open(os.path.join(self.pack, "policy.txt"), "w") as f:
            f.write("wildlife\n")
        no_player = {"test": "is_family", "subject": "other", "operator": "!=", "value": "player"}

        def base(d):
            comps = d["minecraft:entity"]["components"]
            comps["minecraft:type_family"] = {"family": ["gameops_wildlife"]}
            comps["minecraft:behavior.nearest_attackable_target"] = {"priority": 1, "entity_types": [
                {"filters": {"test": "is_family", "subject": "other", "value": "zombie"}}]}
            comps["minecraft:tameable"] = {"probability": 0.33, "tame_items": "bone"}
            comps["minecraft:behavior.stay_while_sitting"] = {"priority": 3}
            comps["minecraft:persistent"] = {}
            for goal in ("owner_hurt_by_target", "owner_hit_target"):
                comps["minecraft:behavior." + goal] = {"priority": 1, "entity_types": [{"filters": no_player}]}
            if fn:
                fn(comps)
        self.edit(BP_ENTITY, base)
        for other in ("ground_dragon.json", "werewolf.json"):
            # fixtures keep only the brute; the copied arena entities are not under test
            for rel in (os.path.join("behavior_pack", "entities", other),
                        os.path.join("resource_pack", "entity", other.replace(".json", ".entity.json"))):
                if os.path.exists(os.path.join(self.pack, rel)):
                    os.remove(os.path.join(self.pack, rel))

    def test_wildlife_baseline_is_clean(self):
        self.make_wildlife()
        self.assertEqual(self.errors(), [])

    def test_wildlife_policy_rejects_hurt_by_target(self):
        self.make_wildlife(lambda c: c.update({"minecraft:behavior.hurt_by_target": {"priority": 1}}))
        self.assertFails("hurt_by_target")

    def test_wildlife_policy_rejects_player_family_target(self):
        player = {"test": "is_family", "subject": "other", "value": "player"}
        for goal in ("nearest_attackable_target", "owner_hurt_by_target", "owner_hit_target", "melee_attack"):
            with self.subTest(goal=goal):
                def fn(c, goal=goal):
                    c["minecraft:behavior." + goal] = {"priority": 1, "entity_types": [
                        {"filters": {"all_of": [player]}}]}
                self.make_wildlife(fn)
                self.assertFails(f"{goal} must never select family player")

    def test_wildlife_owner_goal_needs_player_exclusion(self):
        for goal in ("owner_hurt_by_target", "owner_hit_target"):
            with self.subTest(goal=goal):
                def fn(c, goal=goal):
                    c["minecraft:behavior." + goal] = {"priority": 1, "entity_types": [
                        {"filters": {"test": "is_family", "subject": "other", "value": "zombie"}}]}
                self.make_wildlife(fn)
                self.assertFails(f"{goal} entity_types entry 0 must exclude family player")

    def test_wildlife_goals_are_checked_in_every_group(self):
        unsafe = {"minecraft:behavior.nearest_attackable_target": {"priority": 1, "entity_types": [
            {"filters": {"all_of": [{"test": "is_family", "subject": "other", "value": "player"}]}}]}}
        filterless = {"minecraft:behavior.nearest_attackable_target": {"priority": 1, "entity_types": [{}]}}
        safe = {"minecraft:behavior.nearest_attackable_target": {"priority": 1, "entity_types": [
            {"filters": {"all_of": [{"test": "is_family", "subject": "other", "value": "zombie"}]}}]}}
        for bad in (unsafe, filterless):
            for order in (("wild", "tamed_active"), ("tamed_active", "wild")):
                with self.subTest(bad=list(bad.values())[0]["entity_types"][0], order=order):
                    self.make_wildlife()

                    def fn(d, bad=bad, order=order):
                        d["minecraft:entity"]["component_groups"] = {order[0]: bad, order[1]: safe}
                    self.edit(BP_ENTITY, fn)
                    self.assertFails(f"(group {order[0]})")

    def test_wildlife_unsafe_goal_in_base_components_fails_beside_safe_groups(self):
        self.make_wildlife(lambda c: c.update({"minecraft:behavior.nearest_attackable_target": {
            "priority": 1, "entity_types": [{}]}}))
        safe = {"minecraft:behavior.nearest_attackable_target": {"priority": 1, "entity_types": [
            {"filters": {"test": "is_family", "subject": "other", "value": "zombie"}}]}}
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"].update({"component_groups": {"wild": safe}}))
        self.assertFails("must exclude family player")

    def test_wildlife_component_outside_allow_list_rejected(self):
        for comp in ("minecraft:behavior.nearest_prioritized_attackable_target", "minecraft:area_attack",
                     "minecraft:mob_effect", "minecraft:behavior.ranged_attack"):
            with self.subTest(comp=comp, where="base"):
                self.make_wildlife(lambda c, comp=comp: c.update({comp: {}}))
                self.assertFails(f"{comp} is not on the wildlife allow-list")

    def test_wildlife_component_outside_allow_list_rejected_in_any_group(self):
        for group in ("gameops:wild", "gameops:variant_x"):
            with self.subTest(group=group):
                self.make_wildlife()
                self.edit(BP_ENTITY, lambda d, group=group: d["minecraft:entity"].setdefault(
                    "component_groups", {}).update({group: {"minecraft:behavior.knockback_roar": {}}}))
                self.assertFails(f"(group {group}): minecraft:behavior.knockback_roar is not on the wildlife allow-list")

    def test_wildlife_event_actions_are_allow_listed(self):
        for action in ({"queue_command": {"command": ["damage @p 5"]}}, {"trigger": "other"}):
            with self.subTest(action=list(action)[0]):
                self.make_wildlife()
                self.edit(BP_ENTITY, lambda d, action=action: d["minecraft:entity"].update(
                    {"events": {"gameops:x": action}}))
                self.assertFails(f"event gameops:x: action {list(action)[0]} is not allowed")

    def test_wildlife_nested_event_actions_are_checked(self):
        self.make_wildlife()
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"].update({"events": {
            "gameops:x": {"sequence": [{"randomize": [{"weight": 1, "queue_command": {"command": ["kill @p"]}}]}]}}}))
        self.assertFails("action queue_command is not allowed")

    def test_wildlife_plain_event_actions_pass(self):
        self.make_wildlife()
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"].update({"events": {
            "gameops:x": {"sequence": [{"add": {"component_groups": ["a"]}}, {"randomize": [
                {"weight": 1, "remove": {"component_groups": ["a"]}, "set_property": {"gameops:p": True}}]}]}}}))
        self.assertEqual(self.errors(), [])

    def test_arena_policy_ignores_the_wildlife_allow_lists(self):
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"].update(
            {"events": {"gameops:x": {"queue_command": {"command": ["say hi"]}}}}))
        self.assertEqual(self.errors(), [])

    def test_wildlife_entity_needs_wildlife_family(self):
        self.make_wildlife(lambda c: c.update({"minecraft:type_family": {"family": ["gameops_brute"]}}))
        self.assertFails("gameops_wildlife")

    def test_tameable_needs_stay_while_sitting_and_persistent(self):
        for comp in ("minecraft:behavior.stay_while_sitting", "minecraft:persistent"):
            with self.subTest(comp=comp):
                self.make_wildlife(lambda c, comp=comp: c.pop(comp))
                self.assertFails(comp)

    def test_arena_pack_still_requires_warrior_tag(self):
        def fn(d):
            comp = d["minecraft:entity"]["components"]["minecraft:behavior.nearest_attackable_target"]
            comp["entity_types"][0]["filters"] = {"test": "is_family", "subject": "other", "value": "zombie"}
        self.edit(BP_ENTITY, fn)
        self.assertFails("has_tag arena_warrior")

    def test_unknown_policy_rejected(self):
        with open(os.path.join(self.pack, "policy.txt"), "w") as f:
            f.write("bogus\n")
        self.assertFails("unknown policy")

    def test_wildlife_equals_sign_operator_is_a_positive_match(self):
        def fn(c):
            c["minecraft:behavior.nearest_attackable_target"]["entity_types"] = [
                {"filters": {"test": "is_family", "subject": "other", "operator": "=", "value": "player"}}]
        self.make_wildlife(fn)
        self.assertFails("nearest_attackable_target must never select family player")

    def test_wildlife_player_test_under_any_of_or_none_of_rejected(self):
        player = {"test": "is_family", "subject": "other", "value": "player"}
        for key in ("any_of", "none_of"):
            with self.subTest(key=key):
                def fn(c, key=key):
                    c["minecraft:behavior.nearest_attackable_target"]["entity_types"] = [
                        {"filters": {key: [player]}}]
                self.make_wildlife(fn)
                self.assertFails(f"filters must not use {key}")

    def test_wildlife_owner_goal_mixed_entries_rejected(self):
        no_player = {"test": "is_family", "subject": "other", "operator": "!=", "value": "player"}
        for goal in ("owner_hurt_by_target", "owner_hit_target", "nearest_attackable_target"):
            with self.subTest(goal=goal):
                def fn(c, goal=goal):
                    c["minecraft:behavior." + goal] = {"priority": 1, "entity_types": [
                        {"filters": no_player}, {"max_dist": 8}]}
                self.make_wildlife(fn)
                self.assertFails(f"{goal} entity_types entry 1 must exclude family player")

    def test_wildlife_filterless_target_rejected(self):
        def fn(c):
            c["minecraft:behavior.nearest_attackable_target"]["entity_types"] = [{"max_dist": 16}]
        self.make_wildlife(fn)
        self.assertFails("nearest_attackable_target entity_types entry 0 must exclude family player")

    def test_wildlife_target_without_entity_types_rejected(self):
        for goal in ("nearest_attackable_target", "owner_hit_target"):
            with self.subTest(goal=goal):
                self.make_wildlife(lambda c, goal=goal: c.update({"minecraft:behavior." + goal: {"priority": 1}}))
                self.assertFails(f"{goal} needs entity_types")

    def test_explicit_arena_policy_behaves_as_default(self):
        with open(os.path.join(self.pack, "policy.txt"), "w") as f:
            f.write("arena\n")
        self.assertEqual(self.errors(), [])
        self.edit(BP_ENTITY, lambda d: d["minecraft:entity"]["components"].update(
            {"minecraft:behavior.hurt_by_target": {}}))
        self.assertFails("hurt_by_target")

    def test_default_walk_skips_only_policy_only_folders(self):
        base = os.path.join(self.tmp, "packs")
        os.makedirs(os.path.join(base, "policy-only"))
        with open(os.path.join(base, "policy-only", "policy.txt"), "w") as f:
            f.write("wildlife\n")
        shutil.copytree(PACK, os.path.join(base, "good"))
        self.assertEqual([os.path.basename(p) for p in check_packs.default_packs(base)], ["good"])
        os.rename(os.path.join(base, "good", "behavior_pack"), os.path.join(base, "good", "behaviour_pack"))
        self.assertEqual([os.path.basename(p) for p in check_packs.default_packs(base)], ["good"])
        rep = check_packs.Report()
        check_packs.check_pack_dir(os.path.join(base, "good"), rep)
        self.assertTrue(rep.errors)


if __name__ == "__main__":
    unittest.main()
