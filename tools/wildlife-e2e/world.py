"""Arena helpers: reset, summon, feed, tame. Fixed coordinates, conditions not sleeps."""
import time

from harness import Y, merge_entities, wait_until

A_POS = (10, 10)
B_POS = (16, 10)
FOODS = {
    "tiger": "beef", "cheetah": "rabbit", "lion": "mutton",
    "black_leopard": "chicken", "brown_bear": "salmon", "hyena": "rotten_flesh",
}
MAX_TAME_ATTEMPTS = 40


class World:
    def __init__(self, preview, bots, watcher, ev, source):
        self.pv, self.bots, self.watch, self.ev, self.src = preview, bots, watcher, ev, source
        self.A, self.B = bots["botA"], bots["botB"]

    # --- setup -------------------------------------------------------------
    def setup_server(self):
        pv = self.pv
        pv.cmd("tickingarea", "add", -16, Y, -16, 80, Y, 80, "arena")
        for rule in ("dodaylightcycle", "doweathercycle", "domobspawning", "naturalregeneration", "mobgriefing"):
            pv.cmd("gamerule", rule, "false")
        pv.cmd("difficulty", "normal")
        pv.cmd("time", "set", 6000)
        pv.cmd("weather", "clear")

    def uid(self, bot):
        return int(bot.state()["state"]["unique_id"])

    def reset(self):
        """Idempotent: no animals, healthy bots standing in their spots."""
        pv = self.pv
        pv.cmd("kill", "@e[type=!player]")
        pv.cmd("effect", "@a", "clear")
        pv.cmd("gamemode", "survival", "@a")
        pv.cmd("effect", "@a", "instant_health", 1, 20, "true")
        pv.cmd("effect", "@a", "saturation", 1, 20, "true")
        pv.cmd("clear", "@a")
        pv.cmd("time", "set", 6000)
        self.tp(self.A, A_POS)
        self.tp(self.B, B_POS)
        def emptied():
            left = [e for e in self.watch.entities() if e["type"] != "player" and e["type"] != "minecraft:item"]
            if left:
                pv.cmd("kill", "@e[type=!player]")
            return not left
        wait_until(emptied, 20, "arena emptied", poll=1.5)
        wait_until(lambda: self.hp(self.A) >= 20 and self.hp(self.B) >= 20, 10, "bots healed")

    def tp(self, bot, xz):
        self.pv.cmd("tp", bot.name, xz[0], Y, xz[1])
        wait_until(lambda: abs(self.pos(bot)[0] - xz[0] - 0.5) < 1.5 and abs(self.pos(bot)[2] - xz[1] - 0.5) < 1.5,
                   10, f"{bot.name} at spot")

    # --- reading -----------------------------------------------------------
    def snap(self, bot):
        return self.watch.snaps.get(bot.name) or bot.state()

    def hp(self, bot):
        return self.snap(bot)["state"]["health"]

    def pos(self, bot):
        return self.snap(bot)["state"]["position"]

    def entities(self, kind=None):
        es = self.watch.entities()
        return [e for e in es if kind is None or e["type"] == kind]

    def entity(self, uid):
        for e in self.watch.entities():
            if e["unique_id"] == uid:
                return e
        return None

    @staticmethod
    def dist(a, b):
        return ((a[0] - b[0]) ** 2 + (a[2] - b[2]) ** 2) ** 0.5

    # --- acting ------------------------------------------------------------
    def summon(self, kind, x, z, name=None):
        """Summons and returns the new entity's unique id once the bots see it."""
        before = {e["unique_id"] for e in self.watch.entities()}
        args = ["summon", kind] + ([f'"{name}"'] if name else []) + [x, Y, z]
        self.pv.cmd(*args)
        def fresh():
            for e in self.watch.entities():
                if e["unique_id"] not in before and e["type"] == kind:
                    return e["unique_id"]
        return wait_until(fresh, 10, f"{kind} to appear")

    def give(self, bot, item, n=64):
        self.pv.cmd("give", bot.name, item, n)
        wait_until(lambda: any(i["name"] == item for i in bot.state()["state"]["inventory"]), 10, f"{item} in {bot.name}'s hands")

    def stand(self, owner, uid):
        """Empty-hand interact toggles a sitting pet up. A no-op when it already stands."""
        e = self.entity(uid)
        if e and e["sitting"]:
            owner.task({"template": "interact_entity", "target": {"id": e["id"]}, "item": ""})
            wait_until(lambda: not self.entity(uid)["sitting"], 6, "pet to stand")

    def tame(self, owner, species, near=None, sc=None):
        """Feeds a wild animal its food by real interaction until it is tamed.

        Returns (uid, attempts). The taming chance is 0.33 per feed, the one genuine
        randomness, so this retries and reports how many feeds it took.
        """
        near = near or (A_POS[0] + 5, A_POS[1] + 4)
        kind = f"gameops:{species}"
        uid = self.summon(kind, near[0], near[1])
        food = FOODS[species]
        self.give(owner, food, 64)
        me = self.uid(owner)
        for attempt in range(1, MAX_TAME_ATTEMPTS + 1):
            e = self.entity(uid)
            if e is None:
                return uid, attempt - 1
            t = owner.task({"template": "interact_entity", "target": {"id": e["id"]}, "item": food, "times": 1})
            if t["state"] != "done":
                raise RuntimeError(f"feed task {t['state']}: {t.get('reason')}")
            try:
                wait_until(lambda: (self.entity(uid) or {}).get("tamed"), 1.2, "tamed", poll=0.1)
            except TimeoutError:
                continue
            self.ev.event("harness", "tamed", species=species, attempts=attempt, owner=me)
            return uid, attempt
        return uid, MAX_TAME_ATTEMPTS

    # --- more acting and judging ---------------------------------------------
    def tame_standing(self, owner, species, near=None):
        uid, n = self.tame(owner, species, near)
        self.stand(owner, uid)
        return uid, n

    def sit(self, owner, uid):
        e = self.entity(uid)
        if e and not e["sitting"]:
            owner.task({"template": "interact_entity", "target": {"id": e["id"]}, "item": ""})
            wait_until(lambda: self.entity(uid)["sitting"], 6, "pet to sit")
        self.settle(uid)

    def settle(self, uid, secs=1.0):
        """Waits until the animal has stopped sliding, so its resting spot can be measured."""
        last = [None]
        def still():
            e = self.entity(uid)
            now = e["pos"] if e else None
            ok = last[0] is not None and now is not None and self.dist(last[0], now) < 0.05
            last[0] = now
            return ok
        wait_until(still, 10, "pet to come to rest", poll=secs)

    def walk(self, bot, x, z):
        t = bot.task({"template": "goto", "to": {"x": x, "y": Y, "z": z}}, timeout=90)
        if t["state"] != "done":
            raise RuntimeError(f"{bot.name} could not walk to {x},{z}: {t.get('reason')}")

    def hit(self, bot, target_name, times):
        """bot punches the named player until `times` swings were sent."""
        t = bot.task({"template": "attack_entity", "target": {"type": "player", "name": target_name},
                      "item": "", "times": times, "every": 12}, timeout=60)
        if t["state"] != "done":
            raise RuntimeError(f"{bot.name} could not hit {target_name}: {t.get('reason')}")

    def hit_entity(self, bot, eid, times):
        t = bot.task({"template": "attack_entity", "target": {"id": eid}, "item": "", "times": times, "every": 12}, timeout=60)
        if t["state"] != "done":
            raise RuntimeError(f"{bot.name} could not hit entity {eid}: {t.get('reason')}")

    def away(self, bot, xz=(60, 60)):
        """Parks a bot out of everyone's way."""
        self.tp(bot, xz)

    def observe(self, seconds):
        """Waits and returns the time the window opened, for the history queries."""
        t0 = time.time()
        time.sleep(seconds)
        return t0

    def targeting(self, t0, victims, kinds=None):
        """(type, uid, target) for every animal snapshot since t0 whose target is in victims."""
        hits = set()
        for _ts, snap in self.watch.since(t0):
            for e in merge_entities(snap.values()):
                if e.get("target") in victims and e["type"].startswith("gameops:") and (kinds is None or e["type"] in kinds):
                    hits.add((e["type"], e["unique_id"], e["target"]))
        return sorted(hits)

    def min_health(self, t0, bot):
        vals = [s[bot.name]["state"]["health"] for _t, s in self.watch.since(t0) if bot.name in s]
        return min(vals) if vals else None

    def attacks_on_players(self, evs, only_types=None):
        """gameops animals that hit a player, from the pack's event lines; None when it logs none."""
        if evs is None:
            return None
        return [e for e in evs if e["ev"] == "hit" and e.get("role") == "attacker" and e.get("other_type") == "minecraft:player"
                and (only_types is None or e["type"] in only_types)]
