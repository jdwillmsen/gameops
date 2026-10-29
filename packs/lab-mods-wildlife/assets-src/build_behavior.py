from species import Species

NOT_PLAYER = {"test": "is_family", "subject": "other", "operator": "!=", "value": "player"}


def event(add=(), remove=(), props=None):
    out = {}
    if props:
        out["set_property"] = props
    if add:
        out["add"] = {"component_groups": list(add)}
    if remove:
        out["remove"] = {"component_groups": list(remove)}
    return out


MELEE = {"priority": 4, "speed_multiplier": 1.2, "track_target": True}
LEAP = {"priority": 3, "must_be_on_ground": True, "yd": 0.45, "target_dist": 0.5}
# Script cannot read the sittable state, so the sit events mirror it into an entity property.
SITTING_PROPERTY = "gameops:sitting"


def family_test(family: str) -> dict:
    return {"test": "is_family", "subject": "other", "operator": "==", "value": family}


# A name tag marks a pet-in-waiting or a keepsake, so wild predators leave it alone.
NOT_NAMED = {"test": "has_nametag", "subject": "other", "operator": "==", "value": False}
# Format 1.21 predates the is_tamed filter, so the component test stands in for it.
NOT_TAMED = {"test": "has_component", "subject": "other", "operator": "!=", "value": "minecraft:is_tamed"}


def targeting(s: Species, tamed: bool) -> dict:
    """Prey and rivals as separate OR'd entries; each names a non-player family, so none can select a player."""
    entries = []
    if not tamed:
        entries += [{"filters": {"all_of": [family_test(f), NOT_NAMED]}, "max_dist": 16} for f in s.prey]
    for f in s.rivals:
        tests = [family_test(f)] + ([NOT_TAMED] if tamed else [])
        entries.append({"filters": {"all_of": tests}, "max_dist": 16})
    if not entries:
        return {}
    return {"minecraft:behavior.nearest_attackable_target": {
        "priority": 2, "must_see": True, "reselect_targets": True, "within_radius": 16,
        "entity_types": entries}}


def ability_groups(s: Species) -> tuple[dict, dict]:
    """Speed groups and events for the ability phases main.js fires; ready is explicit so the base value returns."""
    speeds = {"ready": s.speed, "active": s.active_speed, "tired": s.tired_speed}
    groups = {f"gameops:ability_{p}": {"minecraft:movement": {"value": v}} for p, v in speeds.items()}
    events = {}
    for phase in speeds:
        others = [f"gameops:ability_{o}" for o in speeds if o != phase]
        events[f"gameops:ability_{phase}"] = event(add=[f"gameops:ability_{phase}"], remove=others)
    return groups, events


def entity_json(s: Species) -> dict:
    wild = {
        "minecraft:behavior.melee_attack": MELEE,
        "minecraft:tameable": {
            "probability": 0.33,
            "tame_items": [s.tame_item],
            "tame_event": {"event": "gameops:on_tamed", "target": "self"},
        },
        # Only a player holding the taming food is not a threat, so food is the way to approach.
        "minecraft:behavior.avoid_mob_type": {
            "priority": 3,
            "entity_types": [{
                "filters": {"all_of": [
                    {"test": "is_family", "subject": "other", "value": "player"},
                    {"test": "has_equipment", "subject": "other", "domain": "hand", "operator": "not",
                     "value": s.tame_item},
                ]},
                "max_dist": 6,
                "walk_speed_multiplier": 1.0,
                "sprint_speed_multiplier": 1.3,
            }],
        },
    }
    wild.update(targeting(s, tamed=False))
    if s.pounce:
        wild["minecraft:behavior.leap_at_target"] = LEAP
    tamed = {
        "minecraft:is_tamed": {},
        "minecraft:persistent": {},
        "minecraft:sittable": {
            "sit_event": {"event": "gameops:on_sit", "target": "self"},
            "stand_event": {"event": "gameops:on_stand", "target": "self"},
        },
    }
    # Everything that moves or fights lives apart from the sitting group, so on_sit can take it away.
    active = {
        "minecraft:behavior.melee_attack": MELEE,
        "minecraft:behavior.follow_owner": {
            "priority": 6, "speed_multiplier": 1.0, "start_distance": 10, "stop_distance": 2},
        # Each OR'd entry carries its own player exclusion; an owner's attacker may be a player.
        "minecraft:behavior.owner_hurt_by_target": {
            "priority": 1, "entity_types": [{"filters": NOT_PLAYER, "max_dist": 16}]},
        "minecraft:behavior.owner_hit_target": {
            "priority": 2, "entity_types": [{"filters": NOT_PLAYER, "max_dist": 16}]},
    }
    active.update(targeting(s, tamed=True))
    if s.pounce:
        active["minecraft:behavior.leap_at_target"] = LEAP
    sitting = {
        # on_sit also removes the active goals; priority 1 keeps lower-priority wandering from moving it.
        "minecraft:behavior.stay_while_sitting": {"priority": 1},
    }
    groups = {"gameops:wild": wild, "gameops:tamed": tamed, "gameops:tamed_active": active,
              "gameops:sitting": sitting}
    ability_events = {}
    if s.active_speed:
        ability_group_defs, ability_events = ability_groups(s)
        groups.update(ability_group_defs)

    spawn = [{"add": {"component_groups": ["gameops:wild"]}}]
    if s.variants:
        for i, name in enumerate(s.variants):
            groups[f"gameops:variant_{name}"] = {"minecraft:variant": {"value": i}}
            hp = dict(s.variant_health).get(name)
            if hp:
                groups[f"gameops:variant_{name}"]["minecraft:health"] = {"value": hp, "max": hp}
        spawn.append({"randomize": [
            {"weight": 1, "add": {"component_groups": [f"gameops:variant_{name}"]}} for name in s.variants]})

    w, h = s.box
    return {
        "format_version": "1.21.0",
        "minecraft:entity": {
            "description": {
                "identifier": f"gameops:{s.id}",
                "is_spawnable": True,
                "is_summonable": True,
                "is_experimental": False,
                "properties": {SITTING_PROPERTY: {"type": "bool", "default": False}},
            },
            "component_groups": groups,
            "components": {
                "minecraft:type_family": {"family": [s.family, "gameops_wildlife"]},
                "minecraft:collision_box": {"width": w, "height": h},
                "minecraft:health": {"value": s.health, "max": s.health},
                "minecraft:attack": {"damage": s.damage},
                "minecraft:follow_range": {"value": 24},
                "minecraft:movement": {"value": s.speed},
                "minecraft:movement.basic": {},
                "minecraft:navigation.walk": {"can_path_over_water": False, "avoid_water": True},
                "minecraft:jump.static": {},
                "minecraft:physics": {},
                "minecraft:pushable": {"is_pushable": True, "is_pushable_by_piston": True},
                "minecraft:nameable": {},
                "minecraft:behavior.float": {"priority": 0},
                "minecraft:behavior.random_stroll": {"priority": 7, "speed_multiplier": 0.8},
                "minecraft:behavior.look_at_player": {"priority": 8},
                "minecraft:behavior.random_look_around": {"priority": 9},
            },
            "events": {
                "minecraft:entity_spawned": {"sequence": spawn},
                "gameops:on_tamed": event(add=["gameops:tamed", "gameops:tamed_active"],
                                          remove=["gameops:wild"]),
                "gameops:on_sit": event(add=["gameops:sitting"], remove=["gameops:tamed_active"],
                                      props={SITTING_PROPERTY: True}),
                "gameops:on_stand": event(add=["gameops:tamed_active"], remove=["gameops:sitting"],
                                        props={SITTING_PROPERTY: False}),
                **ability_events,
            },
        },
    }
