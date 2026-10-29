"""Run as: python3 -I packs/lab-mods-wildlife/assets-src/test_generators.py

-I drops the script directory from sys.path, so it is added back here.
"""
import filecmp
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import build_behavior  # noqa: E402
import generate  # noqa: E402
from species import SPECIES  # noqa: E402

PACK = os.path.dirname(HERE)
IDS = ("tiger", "cheetah", "lion", "black_leopard", "brown_bear", "hyena")
GROUPS = ("gameops:wild", "gameops:tamed", "gameops:sitting")
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
            "gameops:on_tamed")
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


if __name__ == "__main__":
    unittest.main()
