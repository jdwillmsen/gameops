"""Stage add-on packs into a Bedrock world for lab-preview.

Pure file work, kept apart from the docker script so it can be tested alone.
"""
import json
import shutil
import sys
from pathlib import Path

KINDS = (("behavior_pack", "behavior_packs"), ("resource_pack", "resource_packs"))


def rewrite_manifest(manifest, version, staged=None):
    """Return a copy with header, module and staged-dependency versions replaced.

    A fixed high major plus a time-derived minor makes every stage look new to
    clients, which cache packs by uuid and version.
    """
    out = json.loads(json.dumps(manifest))
    out["header"]["version"] = list(version)
    # Only dependencies on packs staged together are renamed; script-API ones
    # (string version, no uuid) and external packs keep their real version.
    deps = [d for d in out.get("dependencies", []) if "uuid" in d and (staged is None or d["uuid"] in staged)]
    for entry in out.get("modules", []) + deps:
        entry["version"] = list(version)
    return out


def pack_list(manifests):
    return [{"pack_id": m["header"]["uuid"], "version": m["header"]["version"]} for m in manifests]


def stage(packs_dir, world_dir, version):
    packs_dir, world_dir = Path(packs_dir), Path(world_dir)
    found = {}
    sources = {dst: sorted(packs_dir.glob(f"*/{src}")) for src, dst in KINDS}
    if not any(sources.values()):
        raise SystemExit(f"no behavior_pack or resource_pack folders under {packs_dir}")
    staged = {
        json.loads((src / "manifest.json").read_text())["header"]["uuid"]
        for srcs in sources.values()
        for src in srcs
    }
    for _, dst_name in KINDS:
        found[dst_name] = []
        for src in sources[dst_name]:
            manifest = rewrite_manifest(json.loads((src / "manifest.json").read_text()), version, staged)
            dst = world_dir / dst_name / src.parent.name
            if dst.exists():
                shutil.rmtree(dst)
            shutil.copytree(src, dst)
            (dst / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
            found[dst_name].append(manifest)
    for dst_name, manifests in found.items():
        (world_dir / f"world_{dst_name}.json").write_text(json.dumps(pack_list(manifests), indent=2) + "\n")
    return {k: len(v) for k, v in found.items()}


def main(argv):
    if len(argv) != 4:
        print("usage: lab-preview-stage.py PACKS_DIR WORLD_DIR MINOR", file=sys.stderr)
        return 2
    if not 0 <= int(argv[3]) <= 65535:
        print("error: MINOR must be 0..65535 (BDS ignores larger version parts)", file=sys.stderr)
        return 2
    counts = stage(argv[1], argv[2], (99, int(argv[3]), 0))
    if not any(counts.values()):
        print(f"error: no */behavior_pack or */resource_pack under {argv[1]}", file=sys.stderr)
        return 1
    print(f"staged behavior={counts['behavior_packs']} resource={counts['resource_packs']} version=99.{argv[3]}.0")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
