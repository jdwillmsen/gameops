"""Where the suite reads what the pack did. The one place that knows both sources.

The structured `gameops:event` lines are the primary source. A pack without them
(older than the logging release) answers None from every question, and a scenario
that needs the answer skips with that reason instead of guessing.
"""
import json
import re
import time
from pathlib import Path

EVENT_RE = re.compile(r"gameops:event (\{.*\})")
EFFECTS_RE = re.compile(r"gameops:effects (\S+) strength=(\S+) resistance=(\S+)")


def parse_events(text):
    out = []
    for m in EVENT_RE.finditer(text):
        try:
            out.append(json.loads(m.group(1)))
        except ValueError:
            continue
    return out


def parse_effects(text, player):
    """The last effects line for a player as {strength, resistance}, with None for none."""
    found = None
    for m in EFFECTS_RE.finditer(text):
        if m.group(1) == player:
            found = {k: (None if v == "none" else int(v)) for k, v in (("strength", m.group(2)), ("resistance", m.group(3)))}
    return found


class PackSource:
    def __init__(self, preview, pack_dir):
        self.preview = preview
        scripts = Path(pack_dir) / "behavior_pack" / "scripts"
        self.has_events = "gameops:event" in "".join(p.read_text() for p in scripts.glob("*.js") if not p.name.endswith(".test.js"))
        self.mark = self.preview.now()

    def reset_window(self):
        self.mark = self.preview.now()
        return self.mark

    def events(self, since=None):
        """Parsed gameops:event objects since a mark, or None if the pack emits none."""
        if not self.has_events:
            return None
        return parse_events(self.preview.logs(since or self.mark))

    def effects(self, player, timeout=10):
        """Strength and resistance of a player via the test hook."""
        t = self.preview.now()
        self.preview.cmd("scriptevent", "gameops:effects", player)
        end = time.time() + timeout
        while time.time() < end:
            time.sleep(0.4)
            got = parse_effects(self.preview.logs(t), player)
            if got is not None:
                return got
        return None

    def census(self, timeout=10):
        t = self.preview.now()
        self.preview.cmd("scriptevent", "gameops:census")
        end = time.time() + timeout
        while time.time() < end:
            time.sleep(0.4)
            m = re.search(r"gameops:census (.*)", self.preview.logs(t))
            if m:
                return {k: int(v) for k, v in (kv.split("=") for kv in m.group(1).split())}
        return None
