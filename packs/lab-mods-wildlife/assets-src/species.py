from dataclasses import dataclass


@dataclass(frozen=True)
class Species:
    id: str
    family: str
    health: int
    damage: int
    speed: float
    tame_item: str
    prey: tuple[str, ...]
    rivals: tuple[str, ...]
    variants: tuple[str, ...]
    ability: str
    rig: str
    box: tuple[float, float]
    egg: tuple[str, str]
    # Movement while the ability is active and while the animal is recovering; 0 where it has none.
    active_speed: float = 0.0
    tired_speed: float = 0.0
    # Leaps at its target instead of only walking up to it.
    pounce: bool = False


def _s(id, health, damage, speed, tame_item, prey, rivals, variants, ability, rig, box, egg,
       active_speed=0.0, tired_speed=0.0, pounce=False):
    return Species(id, f"gameops_{id}", health, damage, speed, tame_item, prey, rivals, variants, ability, rig,
                   box, egg, active_speed, tired_speed, pounce)


SMALL = ("rabbit", "chicken")
LIVESTOCK = ("sheep", "pig", "cow")

SPECIES = {s.id: s for s in (
    _s("tiger", 60, 8, 0.28, "beef", LIVESTOCK, (), ("orange", "white"), "charge", "cat",
       (1.0, 1.2), ("#d9822b", "#2a2018"), active_speed=0.40, tired_speed=0.18),
    _s("cheetah", 30, 5, 0.45, "rabbit", SMALL, ("gameops_hyena",), (), "sprint", "cat",
       (0.8, 0.9), ("#d9b25a", "#3a2a18"), active_speed=0.60, tired_speed=0.20),
    _s("lion", 50, 7, 0.30, "mutton", LIVESTOCK, ("gameops_hyena",), ("male", "female"), "roar", "cat",
       (1.0, 1.2), ("#c9a05a", "#6a4220")),
    _s("black_leopard", 40, 6, 0.32, "chicken", SMALL, ("gameops_hyena",), (), "ambush", "cat",
       (0.9, 1.0), ("#26262c", "#6a6a74"), pounce=True),
    _s("brown_bear", 80, 9, 0.25, "salmon", (), (), (), "guard", "bear",
       (1.4, 1.5), ("#5a3a22", "#a07a50")),
    _s("hyena", 30, 7, 0.32, "rotten_flesh", (),
       ("gameops_lion", "gameops_cheetah", "gameops_black_leopard"), ("regular", "alpha"), "pack", "hyena",
       (0.9, 1.0), ("#b59a62", "#3a2e1a")),
)}
