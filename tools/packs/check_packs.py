#!/usr/bin/env python3
"""Static checks for the add-on packs under packs/ (no Minecraft needed).

Run as: python3 -I check_packs.py [pack-dir...]   (default: every dir in packs/)
Exit status 1 if any check fails; the size report is printed either way.
"""
import base64
import json
import os
import struct
import sys
import uuid
import zlib

CONFIGMAP_LIMIT = 1024 * 1024


class Report:
    def __init__(self):
        self.errors = []

    def fail(self, msg):
        self.errors.append(msg)


def load_json(path, rep):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        rep.fail(f"{path}: not valid JSON: {e}")
        return None


def walk(root):
    for base, _, names in os.walk(root):
        for n in sorted(names):
            yield os.path.join(base, n)


def check_uuid(value, where, rep):
    try:
        uuid.UUID(value)
    except (ValueError, AttributeError, TypeError):
        rep.fail(f"{where}: {value!r} is not a UUID")
        return None
    return value.lower()


def check_version(value, where, rep):
    if not (isinstance(value, list) and len(value) == 3 and all(isinstance(v, int) and v >= 0 for v in value)):
        rep.fail(f"{where}: version must be three non-negative integers, got {value!r}")


def check_manifest(path, manifest, module_type, rep):
    """Returns (header uuid, version, all uuids declared in this manifest)."""
    if manifest.get("format_version") != 2:
        rep.fail(f"{path}: format_version must be 2")
    header = manifest.get("header") or {}
    check_version(header.get("min_engine_version"), f"{path} header.min_engine_version", rep)
    uuids = []
    hid = check_uuid(header.get("uuid"), f"{path} header.uuid", rep)
    check_version(header.get("version"), f"{path} header.version", rep)
    if hid:
        uuids.append(hid)
    modules = manifest.get("modules") or []
    if not modules:
        rep.fail(f"{path}: no modules")
    for i, mod in enumerate(modules):
        mid = check_uuid(mod.get("uuid"), f"{path} modules[{i}].uuid", rep)
        check_version(mod.get("version"), f"{path} modules[{i}].version", rep)
        allowed = (module_type, "script") if module_type == "data" else (module_type,)
        if mod.get("type") not in allowed:
            rep.fail(f"{path}: modules[{i}].type must be {module_type!r}, got {mod.get('type')!r}")
        if mid:
            uuids.append(mid)
    return hid, header.get("version"), uuids


def read_png_size(path, rep):
    with open(path, "rb") as f:
        data = f.read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        rep.fail(f"{path}: not a PNG")
        return None
    pos = 8
    size = None
    while pos + 8 <= len(data):
        length, tag = struct.unpack(">I4s", data[pos:pos + 8])
        body = data[pos + 8:pos + 8 + length]
        crc_bytes = data[pos + 8 + length:pos + 12 + length]
        if len(body) != length or len(crc_bytes) != 4:
            rep.fail(f"{path}: truncated {tag.decode('ascii', 'replace')} chunk")
            return None
        crc = struct.unpack(">I", crc_bytes)[0]
        if zlib.crc32(tag + body) & 0xFFFFFFFF != crc:
            rep.fail(f"{path}: bad CRC in {tag.decode('ascii', 'replace')} chunk")
            return None
        if tag == b"IHDR":
            size = struct.unpack(">II", body[:8])
        pos += 12 + length
    if size is None:
        rep.fail(f"{path}: no IHDR chunk")
    return size


def entity_filter_items(node, rep=None, where=""):
    """Yields the filter tests that must all hold: the root and nested all_of.

    any_of and none_of change what a test means, and a subject other than
    "other" never matches the candidate, so both are rejected rather than
    flattened into a passing check.
    """
    if isinstance(node, dict):
        for key in ("any_of", "none_of"):
            if key in node and rep is not None:
                rep.fail(f"{where}: filters must not use {key}")
        if "test" in node:
            if node.get("subject", "other") != "other" and rep is not None:
                rep.fail(f"{where}: filter tests must use subject 'other'")
            yield node
        for item in node.get("all_of", []):
            yield from entity_filter_items(item, rep, where)
    elif isinstance(node, list):
        for item in node:
            yield from entity_filter_items(item, rep, where)


POLICIES = ("arena", "wildlife")
WILDLIFE_FAMILY = "gameops_wildlife"
PLAYER_TARGET_GOALS = ("nearest_attackable_target", "owner_hurt_by_target", "owner_hit_target", "melee_attack")
OWNER_GOALS = ("owner_hurt_by_target", "owner_hit_target")


NEGATIVE_OPERATORS = ("!=", "not")
# Entries in entity_types are OR'd, so each one must be safe on its own.
PLAYER_SAFE_GOALS = ("nearest_attackable_target",) + OWNER_GOALS


def is_player_family(test, positive=True):
    if test.get("test") != "is_family" or test.get("value") != "player":
        return False
    negative = test.get("operator", "==") in NEGATIVE_OPERATORS
    return negative != positive


def excludes_player(tests, strict):
    """strict: only an explicit is_family != player counts (owner goals); else a non-player family does too."""
    for t in tests:
        if t.get("test") != "is_family":
            continue
        negative = t.get("operator", "==") in NEGATIVE_OPERATORS
        if negative and t.get("value") == "player":
            return True
        if not strict and not negative and t.get("value") != "player":
            return True
    return False


def check_wildlife_entity(path, comps, rep):
    if "minecraft:behavior.hurt_by_target" in comps:
        rep.fail(f"{path}: hurt_by_target would let the mob retaliate against players")
    for name in PLAYER_TARGET_GOALS:
        goal = comps.get("minecraft:behavior." + name)
        if goal is None:
            continue
        entries = goal.get("entity_types")
        if not entries and name in PLAYER_SAFE_GOALS:
            rep.fail(f"{path}: {name} needs entity_types that exclude family player")
        for i, et in enumerate(entries or []):
            tests = list(entity_filter_items(et.get("filters"), rep, path))
            if any(is_player_family(t) for t in tests):
                rep.fail(f"{path}: {name} must never select family player")
            elif name in PLAYER_SAFE_GOALS and not excludes_player(tests, name in OWNER_GOALS):
                rep.fail(f"{path}: {name} entity_types entry {i} must exclude family player "
                         f"(is_family != player) or name a non-player family")
    families = (comps.get("minecraft:type_family") or {}).get("family") or []
    if WILDLIFE_FAMILY not in families:
        rep.fail(f"{path}: entity must be in family {WILDLIFE_FAMILY}")
    if "minecraft:tameable" in comps:
        for need in ("minecraft:behavior.stay_while_sitting", "minecraft:persistent"):
            if need not in comps:
                rep.fail(f"{path}: tameable entity needs {need}")


def check_behaviour_entity(path, doc, rep, policy="arena"):
    ent = doc.get("minecraft:entity") or {}
    ident = (ent.get("description") or {}).get("identifier")
    comps = ent.get("components") or {}
    for group in (ent.get("component_groups") or {}).values():
        comps = {**comps, **group}
    if not ident or ident.startswith("minecraft:") or ":" not in ident:
        rep.fail(f"{path}: identifier {ident!r} must be in our own namespace")
    if "minecraft:boss" in comps:
        rep.fail(f"{path}: minecraft:boss must not be used")
    if policy == "wildlife":
        check_wildlife_entity(path, comps, rep)
        return ident
    if "minecraft:behavior.hurt_by_target" in comps:
        rep.fail(f"{path}: hurt_by_target would let the mob retaliate against players")
    target = comps.get("minecraft:behavior.nearest_attackable_target")
    if "minecraft:behavior.melee_attack" in comps and "minecraft:attack" not in comps:
        rep.fail(f"{path}: melee_attack without minecraft:attack never hurts anything")
    if target is not None:
        tests = [t for et in target.get("entity_types", []) for t in entity_filter_items(et.get("filters"), rep, path)]
        fam = [t for t in tests if t.get("test") == "is_family"]
        wants_player = any(t.get("value") == "player" and t.get("operator", "==") in ("==", "equals") for t in fam)
        tagged = any(t.get("test") == "has_tag" and t.get("value") == "arena_warrior"
                     and t.get("operator", "==") in ("==", "equals") for t in tests)
        if wants_player:
            rep.fail(f"{path}: nearest_attackable_target must never select family player")
        if not tagged:
            rep.fail(f"{path}: nearest_attackable_target must filter on has_tag arena_warrior")
    return ident


def check_pack_dir(pack, rep):
    bp, rp = os.path.join(pack, "behavior_pack"), os.path.join(pack, "resource_pack")
    policy = "arena"
    policy_file = os.path.join(pack, "policy.txt")
    if os.path.isfile(policy_file):
        with open(policy_file, encoding="utf-8") as f:
            policy = f.read().strip()
        if policy not in POLICIES:
            rep.fail(f"{policy_file}: unknown policy {policy!r}, expected one of {', '.join(POLICIES)}")
            return
    for d in (bp, rp):
        if not os.path.isdir(d):
            rep.fail(f"{d}: missing")
    if rep.errors and not (os.path.isdir(bp) and os.path.isdir(rp)):
        return

    parsed = {}
    for root in (bp, rp):
        for path in walk(root):
            if path.endswith(".json"):
                doc = load_json(path, rep)
                if doc is not None:
                    parsed[path] = doc

    bp_man, rp_man = os.path.join(bp, "manifest.json"), os.path.join(rp, "manifest.json")
    for m in (bp_man, rp_man):
        if m not in parsed:
            rep.fail(f"{m}: missing or unreadable")
    if bp_man not in parsed or rp_man not in parsed:
        return

    bp_hid, _, bp_uuids = check_manifest(bp_man, parsed[bp_man], "data", rep)
    rp_hid, rp_ver, rp_uuids = check_manifest(rp_man, parsed[rp_man], "resources", rep)
    all_uuids = bp_uuids + rp_uuids
    dupes = sorted({u for u in all_uuids if all_uuids.count(u) > 1})
    if dupes:
        rep.fail(f"{pack}: UUIDs reused across headers/modules: {', '.join(dupes)}")

    deps = parsed[bp_man].get("dependencies") or []
    dep = [d for d in deps if isinstance(d, dict) and str(d.get("uuid", "")).lower() == rp_hid]
    if not dep:
        rep.fail(f"{bp_man}: must depend on the resource pack header UUID {rp_hid}")
    elif dep[0].get("version") != rp_ver:
        rep.fail(f"{bp_man}: dependency version {dep[0].get('version')} != resource pack version {rp_ver}")

    bp_ids = set()
    for path, doc in parsed.items():
        if path.startswith(os.path.join(bp, "entities") + os.sep):
            ident = check_behaviour_entity(path, doc, rep, policy)
            if ident:
                bp_ids.add(ident)

    rp_ids = set()
    geometries, controllers = set(), set()
    for path, doc in parsed.items():
        rel = os.path.relpath(path, rp)
        if rel.startswith("models" + os.sep):
            check_geometry_uv(path, doc, rep)
            for g in doc.get("minecraft:geometry", []):
                geometries.add(g.get("description", {}).get("identifier"))
        elif rel.startswith("render_controllers" + os.sep):
            controllers.update((doc.get("render_controllers") or {}).keys())
    for path, doc in parsed.items():
        if not os.path.relpath(path, rp).startswith("entity" + os.sep):
            continue
        desc = (doc.get("minecraft:client_entity") or {}).get("description") or {}
        ident = desc.get("identifier")
        if ident:
            rp_ids.add(ident)
        for name, ref in (desc.get("geometry") or {}).items():
            if ref not in geometries:
                rep.fail(f"{path}: geometry {ref!r} is not defined under models/")
        for ref in desc.get("render_controllers") or []:
            if ref not in controllers:
                rep.fail(f"{path}: render controller {ref!r} is not defined")
        for name, ref in (desc.get("textures") or {}).items():
            if not os.path.isfile(os.path.join(rp, ref + ".png")):
                rep.fail(f"{path}: texture {ref}.png does not exist")

    if not bp_ids:
        rep.fail(f"{bp}: no entity definitions found")
    if bp_ids != rp_ids:
        rep.fail(f"{pack}: entity identifiers differ: behaviour {sorted(bp_ids)} vs client {sorted(rp_ids)}")

    for path in walk(rp):
        if path.endswith(".png"):
            size = read_png_size(path, rep)
            if size and (size[0] > 1024 or size[1] > 1024):
                rep.fail(f"{path}: texture is {size[0]}x{size[1]}, over the 1024 budget for this stage")


def check_geometry_uv(path, doc, rep):
    """Box-UV rectangles (width 2d+2w, height d+h) must lie inside the texture and not overlap."""
    for geo in doc.get("minecraft:geometry", []):
        desc = geo.get("description") or {}
        name = desc.get("identifier")
        tw, th = desc.get("texture_width"), desc.get("texture_height")
        if not isinstance(tw, int) or not isinstance(th, int):
            rep.fail(f"{path}: {name} needs integer texture_width and texture_height")
            continue
        rects = []
        for bone in geo.get("bones", []):
            for i, cube in enumerate(bone.get("cubes", [])):
                uv = cube.get("uv")
                if not isinstance(uv, list):
                    continue
                w, h, d = cube["size"]
                rects.append((f"{bone.get('name')}[{i}]", uv[0], uv[1], uv[0] + 2 * d + 2 * w, uv[1] + d + h))
        for label, x0, y0, x1, y1 in rects:
            if x0 < 0 or y0 < 0 or x1 > tw or y1 > th:
                rep.fail(f"{path}: {name} {label} uv rect ({x0},{y0})-({x1},{y1}) leaves the {tw}x{th} texture")
        for i, a in enumerate(rects):
            for b in rects[i + 1:]:
                if a[1] < b[3] and b[1] < a[3] and a[2] < b[4] and b[2] < a[4]:
                    rep.fail(f"{path}: {name} uv rects of {a[0]} and {b[0]} overlap")


def pack_size(pack):
    total = 0
    for sub in ("behavior_pack", "resource_pack"):
        for path in walk(os.path.join(pack, sub)):
            total += os.path.getsize(path)
    return total


def default_packs(base):
    """Every folder under base, except one holding only policy.txt (a pack still being scaffolded).

    Any other folder is kept so a typo'd directory name fails instead of vanishing.
    """
    return [os.path.join(base, n) for n in sorted(os.listdir(base))
            if os.path.isdir(os.path.join(base, n)) and os.listdir(os.path.join(base, n)) != ["policy.txt"]]


def main(argv):
    if argv:
        packs = argv
    else:
        packs = default_packs(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "packs"))
    if not packs:
        print("no packs found")
        return 1

    rep = Report()
    for pack in packs:
        before = len(rep.errors)
        check_pack_dir(pack, rep)
        status = "ok" if len(rep.errors) == before else "FAIL"
        size = pack_size(pack) if os.path.isdir(pack) else 0
        encoded = len(base64.b64encode(b"x" * size))
        print(f"{os.path.basename(os.path.normpath(pack))}: {status}")
        print(f"  total pack size: {size} bytes")
        print(f"  as ConfigMap binaryData (base64): {encoded} bytes of {CONFIGMAP_LIMIT} "
              f"({100 * encoded / CONFIGMAP_LIMIT:.2f}%), margin {CONFIGMAP_LIMIT - encoded} bytes")
    for e in rep.errors:
        print(f"error: {e}", file=sys.stderr)
    return 1 if rep.errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
