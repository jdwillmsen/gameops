"""Preview server, crew bots, evidence bundle and state watcher for the wildlife e2e suite."""
import collections
import http.client
import socket
import json
import os
import re
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

# The whole suite plays in this box of the preview's flat world (surface y -60).
Y = -60
TOKEN = "e2e"


def port_free(port):
    with socket.socket() as s:
        return s.connect_ex(("127.0.0.1", port)) != 0


def now_ms():
    return int(time.time() * 1000)


class Evidence:
    """One run directory: report.json, timeline.jsonl, server.log, README.md."""

    README = """# Wildlife e2e evidence

- `report.json`: one entry per scenario with its assertions (name, ok, detail) and artifacts.
- `timeline.jsonl`: one JSON object per line (`t_ms`, `source` harness|server|bot, `scenario`, `event`).
- `server.log`: the preview server's full console log, `gameops:event` lines included.
- `bots/*.log`: the two crew bots' own logs.
- `videos/`: filled in by the recorder (JDW-323); empty otherwise.
"""

    def __init__(self, root, commit, server):
        self.run_id = f"{time.strftime('%Y%m%d-%H%M%S', time.gmtime())}-{commit[:7]}"
        self.dir = Path(root) / self.run_id
        (self.dir / "bots").mkdir(parents=True)
        (self.dir / "videos").mkdir()
        (self.dir / "README.md").write_text(self.README)
        self.commit, self.server = commit, server
        self.scenarios = []
        self.scenario = None
        self._lock = threading.Lock()
        self._tl = open(self.dir / "timeline.jsonl", "a", buffering=1)

    def event(self, source, event, **kw):
        line = {"t_ms": now_ms(), "source": source, "scenario": self.scenario, "event": event, **kw}
        with self._lock:
            self._tl.write(json.dumps(line, separators=(",", ":")) + "\n")

    def report(self):
        return {"run_id": self.run_id, "commit": self.commit, "server": self.server, "scenarios": self.scenarios}

    def write_report(self):
        (self.dir / "report.json").write_text(json.dumps(self.report(), indent=2) + "\n")

    def close(self):
        self._tl.close()


class Scenario:
    """Collects the assertions of one scenario."""

    def __init__(self, ev, sid, title):
        self.ev, self.id, self.title = ev, sid, title
        self.assertions = []
        self.artifacts = []
        self.skipped = None
        self.started = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    def check(self, name, ok, detail=""):
        ok = bool(ok)
        self.assertions.append({"name": name, "ok": ok, "detail": str(detail)})
        self.ev.event("harness", "assert", name=name, ok=ok, detail=str(detail))
        return ok

    def skip(self, why):
        self.skipped = why
        self.ev.event("harness", "skip", why=why)

    def status(self):
        if self.skipped:
            return "skip"
        return "pass" if self.assertions and all(a["ok"] for a in self.assertions) else "fail"

    def record(self):
        detail = [{"name": "skipped", "ok": True, "detail": self.skipped}] if self.skipped else []
        return {
            "id": self.id, "title": self.title, "status": self.status(), "started_at": self.started,
            "ended_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "assertions": self.assertions + detail, "artifacts": self.artifacts,
        }


class Preview:
    """The throwaway Bedrock server, driven through tools/lab-preview."""

    def __init__(self, repo, name, port, version):
        self.repo, self.name, self.port, self.version = Path(repo), name, port, version
        self.tool = str(self.repo / "tools" / "lab-preview")
        self.env = {**os.environ, "LAB_PREVIEW_NAME": name, "LAB_PREVIEW_PORT": str(port), "LAB_PREVIEW_VERSION": version}
        self.stage = None
        self.up_done = False

    def _run(self, *args, check=True):
        return subprocess.run([self.tool, *args], env=self.env, capture_output=True, text=True, check=check)

    def up(self):
        self.down()
        self.stage = tempfile.mkdtemp(prefix="wildlife-e2e-stage-")
        pack = self.repo / "packs" / "lab-mods-wildlife"
        os.makedirs(f"{self.stage}/wildlife")
        for d in ("behavior_pack", "resource_pack"):
            shutil.copytree(pack / d, f"{self.stage}/wildlife/{d}")
        self.up_done = True
        r = self._run("up", self.stage, check=False)
        if r.returncode != 0:
            raise RuntimeError(f"lab-preview up failed: {(r.stdout + r.stderr)[-600:]}")

    def down(self):
        self._run("down", check=False)
        if self.stage:
            shutil.rmtree(self.stage, ignore_errors=True)
            self.stage = None

    def now(self):
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - 1))

    def logs(self, since=None):
        args = ["docker", "logs"] + (["--since", since] if since else []) + [self.name]
        r = subprocess.run(args, capture_output=True, text=True)
        return r.stdout + r.stderr

    def cmd(self, *args):
        """Sends a console command. The first write after a restart can find no process yet."""
        for _ in range(5):
            r = self._run("cmd", *[str(a) for a in args], check=False)
            if r.returncode == 0:
                return
            time.sleep(1.5)
        raise RuntimeError(f"console command never went through: {args}")

    def say(self, *args, wait=0.0, expect=None, timeout=8.0):
        """Runs a command and returns the log lines it produced after it was sent."""
        t = self.now()
        self.cmd(*args)
        deadline = time.time() + timeout
        out = ""
        while True:
            time.sleep(0.4)
            out = self.logs(t)
            if expect is None or re.search(expect, out) or time.time() > deadline:
                break
        time.sleep(wait)
        return out

    def testfor(self, selector):
        """True or False from testfor, or None when the line was dropped."""
        out = self.say("testfor", selector, expect=r"No targets matched|Found")
        if "No targets matched" in out:
            return False
        return True if "Found" in out else None


class Bot:
    """An offline crew bot with the task API and the entity list open."""

    def __init__(self, crew_bin, name, status_port, preview, logdir, pidfile):
        if not port_free(status_port):
            raise RuntimeError(f"status port {status_port} is already in use; set WILDLIFE_E2E_BOT_PORT")
        self.name, self.port, self.preview = name, status_port, preview
        self.log = open(Path(logdir) / f"{name}.log", "w")
        env = {**os.environ, "MC_HOST": "127.0.0.1", "MC_PORT": str(preview.port), "MC_USERNAME": name,
               "MC_OFFLINE": "true", "STATUS_ADDR": f"127.0.0.1:{status_port}", "TASKS_TOKEN": TOKEN,
               "CREW_HARNESS": "true", "DEFEND": "false", "EAT": "false", "IDLE_WANDER": "false", "CREW_LAB": "false"}
        self.proc = subprocess.Popen([crew_bin], env=env, stdout=self.log, stderr=subprocess.STDOUT)
        with open(pidfile, "a") as f:
            f.write(f"{self.proc.pid}\n")

    def _get(self, path):
        with urllib.request.urlopen(f"http://127.0.0.1:{self.port}{path}", timeout=5) as r:
            return json.load(r)

    def state(self):
        return self._get("/v1/state")

    def wait_spawned(self, timeout=60):
        end = time.time() + timeout
        while time.time() < end:
            try:
                s = self.state()
                if s["state"]["spawned"] and s["state"].get("unique_id"):
                    return s
            except (urllib.error.URLError, OSError, ValueError):
                pass
            if self.proc.poll() is not None:
                raise RuntimeError(f"bot {self.name} exited with {self.proc.returncode}")
            time.sleep(0.5)
        raise RuntimeError(f"bot {self.name} never spawned")

    def task(self, body, timeout=60):
        """Submits a task and waits for it; returns the finished task record."""
        req = urllib.request.Request(f"http://127.0.0.1:{self.port}/v1/tasks", data=json.dumps(body).encode(),
                                     headers={"Authorization": f"Bearer {TOKEN}"}, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=5) as r:
                tid = json.load(r)["id"]
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"task refused: {e.read().decode().strip()}") from e
        end = time.time() + timeout
        while time.time() < end:
            tasks = self.state()["tasks"]
            for t in ([tasks["current"]] if tasks["current"] else []) + tasks["recent"]:
                if t["id"] == tid and t["state"] in ("done", "failed", "cancelled"):
                    return t
            time.sleep(0.2)
        raise RuntimeError(f"task {tid} {body.get('template')} still running after {timeout}s")

    def stop(self):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        self.log.close()


class Watcher(threading.Thread):
    """Polls both bots, keeps a short history and writes state changes to the timeline."""

    def __init__(self, bots, ev, period=0.2):
        super().__init__(daemon=True)
        self.bots, self.ev, self.period = bots, ev, period
        self.snaps = {}
        self.history = collections.deque(maxlen=900)
        self.stop_flag = threading.Event()
        self._last = {}
        self.errors = 0

    def run(self):
        while not self.stop_flag.is_set():
            try:
                self._poll()
            except Exception as exc:  # a bad snapshot must not freeze the data every scenario reads
                self.errors += 1
                if self.errors <= 5 and not self.stop_flag.is_set():
                    self.ev.event("harness", "watcher_error", error=repr(exc))
            self.stop_flag.wait(self.period)

    def _poll(self):
        snap = {}
        for name, bot in self.bots.items():
            try:
                snap[name] = bot.state()
            except (urllib.error.URLError, OSError, ValueError, http.client.HTTPException):
                pass
        if snap:
            self.snaps = snap
            self.history.append((time.time(), snap))
            self._diff(snap)

    def _diff(self, snap):
        for name, s in snap.items():
            hp = s["state"]["health"]
            if self._last.get((name, "hp")) != hp:
                self.ev.event("bot", "health", bot=name, health=hp)
                self._last[(name, "hp")] = hp
        for e in merge_entities(snap.values()):
            if not (e["type"].startswith("gameops:") or e["type"] in ("minecraft:zombie",)):
                continue
            key = (e["unique_id"],)
            sig = (e["tamed"], e["sitting"], e.get("owner"), e.get("target"), round(e["health"]) if e["health_known"] else None)
            if self._last.get(key) != sig:
                self.ev.event("bot", "entity", type=e["type"], uid=e["unique_id"], tamed=e["tamed"], sitting=e["sitting"],
                              owner=e.get("owner"), target=e.get("target"), health=e["health"] if e["health_known"] else None,
                              pos=[round(c, 1) for c in e["pos"]])
                self._last[key] = sig

    def entities(self):
        """The latest entities either bot knows of, one per unique id."""
        return merge_entities(self.snaps.values())

    def since(self, t):
        return [(ts, s) for ts, s in list(self.history) if ts >= t]


def merge_entities(snaps):
    merged = {}
    for s in snaps:
        for e in s.get("entities", []):
            merged.setdefault(e["unique_id"], e)
    return list(merged.values())


def wait_until(pred, timeout, desc="condition", poll=0.25):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        last = pred()
        if last:
            return last
        time.sleep(poll)
    raise TimeoutError(f"timed out after {timeout}s waiting for {desc}")


def install_signal_handlers():
    def bail(signum, _frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, bail)
    signal.signal(signal.SIGINT, bail)
