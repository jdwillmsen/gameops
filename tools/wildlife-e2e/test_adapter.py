import unittest

from adapter import parse_effects, parse_events


class ParseTests(unittest.TestCase):
    def test_events_are_parsed_by_name_and_noise_is_skipped(self):
        text = (
            '[2026-10-31 INFO] [Scripting] gameops:event {"ev":"tamed","type":"gameops:lion","id":"-4","owner":"p1","tick":9}\n'
            "[Scripting] gameops:event {broken\n"
            '[Scripting] gameops:event {"ev":"hit","type":"gameops:hyena","id":"-5","owner":null,"role":"attacker","other_type":"minecraft:player"}\n'
        )
        evs = parse_events(text)
        self.assertEqual([e["ev"] for e in evs], ["tamed", "hit"])
        self.assertEqual(evs[1]["other_type"], "minecraft:player")

    def test_effects_line_of_the_named_player_only(self):
        text = "gameops:effects other strength=1 resistance=none\ngameops:effects botA strength=0 resistance=none\n"
        self.assertEqual(parse_effects(text, "botA"), {"strength": 0, "resistance": None})
        self.assertIsNone(parse_effects(text, "nobody"))


if __name__ == "__main__":
    unittest.main()
