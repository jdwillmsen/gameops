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
    # (variant, health) for variants that are tougher than the base value.
    variant_health: tuple = ()
    # What it drops on death: (item, min, max) rolled independently. Experience is paid only when a
    # player landed the last hit, so lava and falls cannot farm it.
    drops: tuple = ()
    xp: int = 0
    # How much larger than the shared model the adult is drawn; the hitbox in `box` already includes it.
    adult_scale: float = 1.0
    # Seconds a cub takes to grow up; feeding it speeds that up.
    grow_seconds: int = 900
    # Cubs per birth (min, max); main.js rolls it and mirrors this table in abilities.js LITTERS.
    litter: tuple = (1, 1)


def _s(id, health, damage, speed, tame_item, prey, rivals, variants, ability, rig, box, egg,
       active_speed=0.0, tired_speed=0.0, pounce=False, variant_health=(), drops=(), xp=0,
       adult_scale=1.0, grow_seconds=900, litter=(1, 1)):
    return Species(id, f"gameops_{id}", health, damage, speed, tame_item, prey, rivals, variants, ability, rig,
                   box, egg, active_speed, tired_speed, pounce, variant_health, drops, xp, adult_scale, grow_seconds, litter)


SMALL = ("rabbit", "chicken")
LIVESTOCK = ("sheep", "pig", "cow")

SPECIES = {s.id: s for s in (
    _s("tiger", 60, 8, 0.28, "beef", LIVESTOCK, (), ("orange", "white"), "charge", "cat",
       (1.0, 1.2), ("#d9822b", "#2a2018"), active_speed=0.40, tired_speed=0.18, drops=(("beef", 1, 3), ("leather", 1, 2)), xp=8, litter=(1, 2)),
    _s("cheetah", 30, 5, 0.45, "rabbit", SMALL, ("gameops_hyena",), (), "sprint", "cat",
       (0.8, 0.9), ("#d9b25a", "#3a2a18"), active_speed=0.60, tired_speed=0.20, drops=(("rabbit", 1, 2), ("rabbit_hide", 0, 1)), xp=5, litter=(2, 3)),
    _s("lion", 50, 7, 0.30, "mutton", LIVESTOCK, ("gameops_hyena",), ("male", "female"), "roar", "cat",
       (1.0, 1.2), ("#c9a05a", "#6a4220"), drops=(("mutton", 1, 3), ("leather", 0, 2)), xp=7, litter=(1, 3)),
    _s("black_leopard", 40, 6, 0.32, "chicken", SMALL, ("gameops_hyena",), (), "ambush", "cat",
       (0.9, 1.0), ("#26262c", "#6a6a74"), pounce=True, drops=(("chicken", 0, 1), ("leather", 0, 2), ("bone", 0, 1)), xp=6, litter=(1, 2)),
    _s("brown_bear", 80, 9, 0.25, "salmon", (), (), (), "guard", "bear",
       (1.8, 1.95), ("#5a3a22", "#a07a50"), drops=(("leather", 1, 3), ("salmon", 0, 2)), xp=10,
       adult_scale=1.3, grow_seconds=1800, litter=(1, 1)),
    _s("hyena", 30, 7, 0.32, "rotten_flesh", (),
       ("gameops_lion", "gameops_cheetah", "gameops_black_leopard"), ("regular", "alpha"), "pack", "hyena",
       (0.9, 1.0), ("#b59a62", "#3a2e1a"), variant_health=(("alpha", 40),),
       drops=(("rotten_flesh", 1, 2), ("bone", 0, 2)), xp=5, litter=(2, 4)),
)}
