import bear_rig
import cat_rig
import hyena_rig
from species import Species

# Every rig keeps the cat bone names, so one animation set serves all species.
# One list for the animation file, the client entity files and their tests.
ANIMATION_NAMES = ("walk", "sit", "idle", "attack")
ANIMATION_PREFIX = "animation.gameops.cat."

SWING = "math.cos(query.modified_distance_moved * 24.0) * {amp} * query.modified_move_speed"
LUNGE = "math.sin(variable.attack_time * 180.0)"


RIGS = {"cat": cat_rig, "bear": bear_rig, "hyena": hyena_rig}


def rig_layout(s: Species) -> list:
    rig = RIGS[s.rig]
    return rig.layout(rig.PARAMS[s.id])


def geometry(s: Species) -> dict:
    rig = RIGS[s.rig]
    return rig.build(rig.PARAMS[s.id], f"geometry.gameops.{s.id}")


def animations() -> dict:
    bones = {
        "walk": {
            "front_left_leg": {"rotation": [SWING.format(amp="40.0"), 0, 0]},
            "back_right_leg": {"rotation": [SWING.format(amp="40.0"), 0, 0]},
            "front_right_leg": {"rotation": [SWING.format(amp="-40.0"), 0, 0]},
            "back_left_leg": {"rotation": [SWING.format(amp="-40.0"), 0, 0]},
            "tail": {"rotation": [0, "math.cos(query.modified_distance_moved * 12.0) * 8.0", 0]},
        },
        # Pitching the body about its front drops the hindquarters; the head levels out again and the
        # hind legs fold forward under the haunches.
        "sit": {
            "body": {"rotation": [-20, 0, 0]},
            "head": {"rotation": [20, 0, 0]},
            "back_left_leg": {"rotation": [-75, 0, 0]},
            "back_right_leg": {"rotation": [-75, 0, 0]},
            "tail": {"rotation": [-15, 0, 0]},
        },
        "idle": {
            "tail": {"rotation": [0, "math.sin(query.life_time * 120.0) * 14.0", 0]},
            "head": {"rotation": ["math.clamp(query.target_x_rotation, -30.0, 30.0)",
                                  "math.clamp(query.target_y_rotation, -60.0, 60.0)", 0]},
        },
        "attack": {
            "head": {"rotation": [f"30.0 * {LUNGE}", 0, 0], "position": [0, 0, f"-3.0 * {LUNGE}"]},
        },
    }
    return {
        "format_version": "1.8.0",
        "animations": {ANIMATION_PREFIX + n: {"loop": True, "bones": bones[n]} for n in ANIMATION_NAMES},
    }
