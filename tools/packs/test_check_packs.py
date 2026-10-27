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


if __name__ == "__main__":
    unittest.main()
