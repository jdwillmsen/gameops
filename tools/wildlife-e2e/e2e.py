#!/usr/bin/env python3
"""Runs the wildlife scenarios against a throwaway preview server and writes the evidence bundle."""
import argparse
import atexit
import os
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import harness  # noqa: E402
import scenarios  # noqa: E402
from adapter import PackSource  # noqa: E402
from world import World  # noqa: E402


def main():
    ap = argparse.ArgumentParser(description=__doc__, allow_abbrev=False)
    ap.add_argument("--only", action="append", default=[], help="scenario id or id prefix; repeatable")
    ap.add_argument("--list", action="store_true")
    ap.add_argument("--version", default=os.environ.get("LAB_PREVIEW_VERSION", "1.26.60.6"))
    ap.add_argument("--port", type=int, default=int(os.environ.get("WILDLIFE_E2E_PORT", "19160")))
    ap.add_argument("--keep", action="store_true", help="leave the preview running afterwards")
    args = ap.parse_args()
    if args.list:
        for sid, title, _ in scenarios.REGISTRY:
            print(f"{sid}\t{title}")
        return 0

    repo = Path(__file__).resolve().parents[2]
    commit = subprocess.run(["/usr/bin/git", "-C", str(repo), "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip() or "unknown"
    chosen = [s for s in scenarios.REGISTRY if not args.only or any(s[0].startswith(o) for o in args.only)]
    if not chosen:
        print("no scenario matches", args.only, file=sys.stderr)
        return 2
    root = os.environ.get("WILDLIFE_EVIDENCE_DIR") or os.path.expanduser("~/evidence/wildlife")
    ev = harness.Evidence(root, commit, {"version": args.version, "transport": "raknet"})
    own_work = not os.environ.get("WILDLIFE_E2E_WORK")
    work = os.environ.get("WILDLIFE_E2E_WORK") or tempfile.mkdtemp(prefix="wildlife-e2e-")
    pidfile = Path(work) / "pids"
    harness.install_signal_handlers()

    preview = harness.Preview(repo, os.environ.get("WILDLIFE_E2E_NAME", "wildlife-e2e"), args.port, args.version)
    bots, watcher = {}, None
    done_ids = set()

    def guarded(step):
        try:
            step()
        except Exception as exc:
            print(f"cleanup: {exc!r}", file=sys.stderr)

    def cleanup():
        if watcher:
            watcher.stop_flag.set()
            guarded(lambda: watcher.join(3))
        for b in bots.values():
            guarded(b.stop)
        if preview.up_done:
            guarded(lambda: (ev.dir / "server.log").write_text(preview.logs()))
        # A start that failed halfway leaves a stage even under --keep.
        if preview.stage and (not args.keep or not preview.up_done):
            guarded(preview.down)
        # A run cut short still reports every scenario it was asked for.
        for sid, title, _ in chosen:
            if sid not in done_ids:
                sc = harness.Scenario(ev, sid, title)
                if sid == ev.scenario:
                    sc.check("scenario ran to the end", False, "run interrupted")
                else:
                    sc.skip("run interrupted before it started")
                ev.scenarios.append(sc.record())
        guarded(ev.write_report)
        guarded(ev.close)
        if own_work:
            shutil.rmtree(work, ignore_errors=True)
    atexit.register(cleanup)
    print(f"evidence: {ev.dir}", flush=True)

    ev.event("harness", "build_crew")
    crew = os.path.join(work, "crew")
    r = subprocess.run(["go", "build", "-o", crew, "./minecraft/crew/cmd/crew"], cwd=repo, capture_output=True, text=True)
    if r.returncode != 0:
        print(r.stderr, file=sys.stderr)
        return 3
    ev.event("harness", "preview_up", version=args.version)
    preview.up()
    ports = (int(os.environ.get("WILDLIFE_E2E_BOT_PORT", "18111")),)
    for i, name in enumerate(("botA", "botB")):
        bots[name] = harness.Bot(crew, name, ports[0] + i, preview, ev.dir / "bots", pidfile)
    for b in bots.values():
        b.wait_spawned()
    watcher = harness.Watcher(bots, ev)
    watcher.start()
    source = PackSource(preview, harness.pack_dir(repo))
    world = World(preview, bots, watcher, ev, source)
    world.setup_server()
    ev.event("server", "ready", events_supported=source.has_events)

    failed = 0
    for sid, title, fn in chosen:
        ev.scenario = sid
        sc = harness.Scenario(ev, sid, title)
        print(f"== {sid}: {title}", flush=True)
        ev.event("harness", "scenario_start", title=title)
        source.reset_window()
        try:
            fn(world, sc)
        except Exception as exc:  # a crash is a failed scenario with its trace, not a lost run
            sc.check("scenario ran to the end", False, "".join(traceback.format_exception_only(type(exc), exc)).strip())
            ev.event("harness", "error", trace=traceback.format_exc())
        ev.scenarios.append(sc.record())
        done_ids.add(sid)
        ev.write_report()
        st = sc.status()
        failed += st == "fail"
        for a in sc.assertions:
            print(f"   {'ok  ' if a['ok'] else 'FAIL'} {a['name']}: {a['detail']}", flush=True)
        print(f"   -> {st}", flush=True)
        ev.event("harness", "scenario_end", status=st)
    ev.scenario = None
    print(f"{len(chosen) - failed}/{len(chosen)} scenarios passed (skips count as passed); report: {ev.dir / 'report.json'}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
