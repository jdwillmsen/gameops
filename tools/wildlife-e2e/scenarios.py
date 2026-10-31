"""The scenarios. Each takes a World and a Scenario and records assertions."""
from harness import wait_until
from world import A_POS, FOODS, MAX_TAME_ATTEMPTS

SPECIES = list(FOODS)
REGISTRY = []


def scenario(sid, title):
    def deco(fn):
        REGISTRY.append((sid, title, fn))
        return fn
    return deco


def _tame_one(species):
    def run(w, sc):
        w.reset()
        uid, attempts = w.tame(w.A, species)
        e = w.entity(uid)
        sc.check("tamed by real feeding", e is not None and e["tamed"],
                 f"{FOODS[species]} fed {attempts} time(s), cap {MAX_TAME_ATTEMPTS}")
        sc.check("owner is the feeder", e is not None and e.get("owner") == w.uid(w.A),
                 f"owner={e and e.get('owner')} feeder={w.uid(w.A)}")
        sc.check("a stranger did not own it", e is not None and e.get("owner") != w.uid(w.B), "")
        evs = w.src.events()
        if evs is not None:
            sc.check("pack logged the taming", any(x["ev"] == "tamed" and x["id"] == str(uid) for x in evs),
                     f"{len(evs)} gameops:event lines")
    return run


for _sp in SPECIES:
    scenario(f"tame-{_sp.replace('_', '-')}", f"Tame the {_sp.replace('_', ' ')} with {FOODS[_sp].replace('_', ' ')} by real interaction")(_tame_one(_sp))
