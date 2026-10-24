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


def check_manifest(path, manifest, rep):
    """Returns (header uuid, version, all uuids declared in this manifest)."""
    header = manifest.get("header") or {}
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
        crc = struct.unpack(">I", data[pos + 8 + length:pos + 12 + length])[0]
        if zlib.crc32(tag + body) & 0xFFFFFFFF != crc:
            rep.fail(f"{path}: bad CRC in {tag.decode('ascii', 'replace')} chunk")
            return None
        if tag == b"IHDR":
            size = struct.unpack(">II", body[:8])
        pos += 12 + length
    if size is None:
        rep.fail(f"{path}: no IHDR chunk")
    return size


def entity_filter_items(node):
    """Yields every filter test dict nested anywhere in a filters tree."""
    if isinstance(node, dict):
        if "test" in node:
            yield node
        for v in node.values():
            yield from entity_filter_items(v)
    elif isinstance(node, list):
        for v in node:
            yield from entity_filter_items(v)


def check_behaviour_entity(path, doc, rep):
    ent = doc.get("minecraft:entity") or {}
    ident = (ent.get("description") or {}).get("identifier")
    comps = ent.get("components") or {}
    for group in (ent.get("component_groups") or {}).values():
        comps = {**comps, **group}
    if not ident or ident.startswith("minecraft:") or ":" not in ident:
        rep.fail(f"{path}: identifier {ident!r} must be in our own namespace")
    if "minecraft:boss" in comps:
        rep.fail(f"{path}: minecraft:boss must not be used")
    if "minecraft:behavior.hurt_by_target" in comps:
        rep.fail(f"{path}: hurt_by_target would let the mob retaliate against players")
    target = comps.get("minecraft:behavior.nearest_attackable_target")
    if "minecraft:behavior.melee_attack" in comps and "minecraft:attack" not in comps:
        rep.fail(f"{path}: melee_attack without minecraft:attack never hurts anything")
    if target is not None:
        tests = [t for et in target.get("entity_types", []) for t in entity_filter_items(et.get("filters"))]
        fam = [t for t in tests if t.get("test") == "is_family"]
        wants_target = any(t.get("value") == "arena_target" and t.get("operator", "==") in ("==", "equals") for t in fam)
        wants_player = any(t.get("value") == "player" and t.get("operator", "==") in ("==", "equals") for t in fam)
        if not wants_target:
            rep.fail(f"{path}: nearest_attackable_target must filter on family arena_target")
        if wants_player:
            rep.fail(f"{path}: nearest_attackable_target must never select family player")
        if not any(t.get("value") == "player" and t.get("operator") == "!=" for t in fam):
            rep.fail(f"{path}: nearest_attackable_target must explicitly exclude family player")
    return ident


def check_pack_dir(pack, rep):
    bp, rp = os.path.join(pack, "behavior_pack"), os.path.join(pack, "resource_pack")
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

    bp_hid, bp_ver, bp_uuids = check_manifest(bp_man, parsed[bp_man], rep)
    rp_hid, _, rp_uuids = check_manifest(rp_man, parsed[rp_man], rep)
    all_uuids = bp_uuids + rp_uuids
    dupes = sorted({u for u in all_uuids if all_uuids.count(u) > 1})
    if dupes:
        rep.fail(f"{pack}: UUIDs reused across headers/modules: {', '.join(dupes)}")

    deps = parsed[rp_man].get("dependencies") or []
    dep = [d for d in deps if isinstance(d, dict) and str(d.get("uuid", "")).lower() == bp_hid]
    if not dep:
        rep.fail(f"{rp_man}: must depend on the behaviour pack header UUID {bp_hid}")
    elif dep[0].get("version") != bp_ver:
        rep.fail(f"{rp_man}: dependency version {dep[0].get('version')} != behaviour pack version {bp_ver}")

    bp_ids = set()
    for path, doc in parsed.items():
        if path.startswith(os.path.join(bp, "entities") + os.sep):
            ident = check_behaviour_entity(path, doc, rep)
            if ident:
                bp_ids.add(ident)

    rp_ids = set()
    geometries, controllers = set(), set()
    for path, doc in parsed.items():
        rel = os.path.relpath(path, rp)
        if rel.startswith("models" + os.sep):
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


def pack_size(pack):
    total = 0
    for sub in ("behavior_pack", "resource_pack"):
        for path in walk(os.path.join(pack, sub)):
            total += os.path.getsize(path)
    return total


def main(argv):
    if argv:
        packs = argv
    else:
        base = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "packs")
        packs = [os.path.join(base, n) for n in sorted(os.listdir(base)) if os.path.isdir(os.path.join(base, n))]
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
