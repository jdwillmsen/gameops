"""The scenarios. Each takes a World and a Scenario and records assertions."""
import time

from harness import merge_entities, wait_until
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


# --- following, sitting ---------------------------------------------------------

@scenario("follow-owner", "A standing pet follows its owner across the arena")
def follow_owner(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame_standing(w.A, "lion")
    start = w.entity(uid)["pos"]
    w.walk(w.A, 44, 10)
    near = lambda: w.dist(w.entity(uid)["pos"], w.pos(w.A)) < 7
    try:
        wait_until(near, 40, "pet to catch up")
    except TimeoutError:
        pass
    pet = w.entity(uid)["pos"]
    sc.check("pet ended near its owner", near(), f"{w.dist(pet, w.pos(w.A)):.1f} blocks apart")
    sc.check("pet really travelled", w.dist(start, pet) > 20, f"moved {w.dist(start, pet):.1f} blocks")


@scenario("sit-stand", "Empty-hand interact sits and stands the owner's pet; a sitting pet stays put")
def sit_stand(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame(w.A, "tiger")
    sc.check("a freshly tamed pet stands", not w.entity(uid)["sitting"], "")
    w.sit(w.A, uid)
    sc.check("empty-hand interact sits it", w.entity(uid)["sitting"], "")
    w.stand(w.A, uid)
    sc.check("empty-hand interact stands it again", not w.entity(uid)["sitting"], "")
    w.sit(w.A, uid)
    sc.check("and sits it once more", w.entity(uid)["sitting"], "")
    spot = w.entity(uid)["pos"]
    w.walk(w.A, 44, 10)
    t0 = w.observe(8)
    moved = w.dist(spot, w.entity(uid)["pos"])
    sc.check("a sitting pet does not follow", moved < 1.0, f"moved {moved:.2f} blocks while the owner walked 30")
    evs = w.src.events()
    if evs is not None:
        kinds = [e["ev"] for e in evs if e["ev"] in ("sit", "stand") and e["id"] == str(uid)]
        sc.check("pack logged sit, stand, sit", kinds == ["sit", "stand", "sit"], kinds)


@scenario("sit-stranger", "A second player cannot sit or stand another player's pet")
def sit_stranger(w, sc):
    w.reset()
    uid, _ = w.tame_standing(w.A, "brown_bear")
    before = w.entity(uid)["sitting"]
    w.tp(w.B, (w.entity(uid)["pos"][0] + 4, w.entity(uid)["pos"][2]))
    t = w.B.task({"template": "interact_entity", "target": {"id": w.entity(uid)["id"]}, "item": "", "times": 3, "every": 15})
    time.sleep(1.5)
    sc.check("stranger's interact was sent", t["state"] == "done", t.get("reason", ""))
    sc.check("pet still stands for its owner", w.entity(uid)["sitting"] == before and not before,
             f"sitting before={before} after={w.entity(uid)['sitting']}")


# --- owner defence --------------------------------------------------------------

@scenario("owner-defence", "A pet engages a hostile mob that hurts its owner")
def owner_defence(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame_standing(w.A, "lion")
    w.tp(w.A, A_POS)
    zombie = w.summon("minecraft:zombie", A_POS[0] + 5, A_POS[1])
    t0 = time.time()
    engaged = lambda: (w.entity(zombie) or {}).get("target") == 0 and False
    try:
        wait_until(lambda: (w.entity(uid) or {}).get("target") == zombie, 20, "pet to target the zombie")
        ok = True
    except TimeoutError:
        ok = False
    sc.check("pet targeted the zombie that attacked its owner", ok, f"zombie uid {zombie}")
    gone = lambda: w.entity(zombie) is None or w.entity(zombie)["health"] <= 0
    try:
        wait_until(gone, 25, "zombie to die")
        dead = True
    except TimeoutError:
        dead = False
    sc.check("the zombie did not survive the pet", dead, "")
    sc.check("the owner was only hurt by the zombie, not the pet", not w.targeting(t0, {w.uid(w.A)}), "")


# --- ecosystem ------------------------------------------------------------------

@scenario("eco-lion-hunts-sheep", "A wild lion hunts an unnamed sheep")
def lion_hunts_sheep(w, sc):
    w.reset()
    w.away(w.B)
    sheep = w.summon("minecraft:sheep", 34, 20)
    w.summon("gameops:lion", 28, 20)
    try:
        wait_until(lambda: w.entity(sheep) is None, 90, "sheep to be hunted")
        gone = True
    except TimeoutError:
        gone = False
    sc.check("the unnamed sheep was taken", gone, "within 90 s of meeting a wild lion")


@scenario("eco-tiger-spares-named-cow", "A wild tiger takes an unnamed pig but spares a named cow")
def tiger_spares_named(w, sc):
    w.reset()
    w.away(w.B)
    pig = w.summon("minecraft:pig", 34, 20)
    cow = w.summon("minecraft:cow", 36, 22, name="Bessie")
    w.summon("gameops:tiger", 28, 20)
    try:
        wait_until(lambda: w.entity(pig) is None, 90, "pig to be hunted")
        control = True
    except TimeoutError:
        control = False
    sc.check("control: the unnamed pig was taken", control, "otherwise the tiger never hunted and sparing proves nothing")
    time.sleep(30)
    c = w.entity(cow)
    sc.check("the named cow is still alive", c is not None and c["health"] > 0, "30 s after the pig was gone")


@scenario("eco-lion-vs-hyena", "A wild lion and a wild hyena fight on sight")
def lion_vs_hyena(w, sc):
    w.reset()
    w.away(w.B)
    lion = w.summon("gameops:lion", 30, 30)
    hyena = w.summon("gameops:hyena", 36, 30)
    t0 = time.time()
    def fought():
        l, h = w.entity(lion), w.entity(hyena)
        return h is None or l is None or h["health"] < 30 or l["health"] < 50
    try:
        wait_until(fought, 60, "the fight")
        ok = True
    except TimeoutError:
        ok = False
    sc.check("one of them was hurt within 60 s", ok, "")
    seen = {(e["type"], e["target"]) for _t, s in w.watch.since(t0) for e in merge_entities(s.values()) if e.get("target")}
    sc.check("they targeted each other", ("gameops:lion", w.entity(hyena)["unique_id"] if w.entity(hyena) else hyena) in seen
             or ("gameops:hyena", lion) in seen or ("gameops:lion", hyena) in seen, sorted(seen))


@scenario("eco-tamed-lion-spares-livestock", "A tamed lion leaves livestock alone")
def tamed_lion_livestock(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame_standing(w.A, "lion")
    stock = [w.summon("minecraft:pig", 14, 18), w.summon("minecraft:sheep", 18, 18), w.summon("minecraft:cow", 12, 20)]
    t0 = w.observe(45)
    sc.check("pig, sheep and cow all alive after 45 s", all(w.entity(s) and w.entity(s)["health"] > 0 for s in stock), "")
    sc.check("the pet never targeted livestock", not w.targeting(t0, set(stock)), "")
    sc.check("the lion is still tamed", w.entity(uid)["tamed"], "")


@scenario("eco-pets-attack-wild-rivals-only", "Pets fight wild rivals, never another player's pet")
def pets_wild_rivals_only(w, sc):
    w.reset()
    # Each animal is tamed out of the other's sight and left sitting, because a standing
    # pet fights any wild rival it can see, and a rival being tamed mid-fight is not the question.
    w.tp(w.B, (56, 56))
    hyena, _ = w.tame(w.B, "hyena", near=(60, 60))
    lion, _ = w.tame_standing(w.A, "lion")
    w.tp(w.B, (20, 14))
    w.stand(w.B, hyena)
    w.tp(w.A, A_POS)
    t0 = w.observe(25)
    sc.check("A's lion and B's hyena ignore each other", not w.targeting(t0, {lion, hyena}),
             f"lion hp {(w.entity(lion) or {}).get('health')}, hyena hp {(w.entity(hyena) or {}).get('health')}")
    sc.check("neither pet was hurt", all(w.entity(u) and w.entity(u)["health"] >= h for u, h in ((lion, 50), (hyena, 30))), "")
    wild = w.summon("gameops:hyena", 16, 22)
    try:
        wait_until(lambda: (w.entity(lion) or {}).get("target") == wild, 30, "lion to go for the wild hyena")
        ok = True
    except TimeoutError:
        ok = False
    sc.check("A's lion goes for the wild hyena", ok, f"wild hyena uid {wild}")


# --- player safety: no animal or pet may ever go for a player ----------------------

def _safe(w, sc, t0, players, evs, species=None):
    uids = {w.uid(p) for p in players}
    sc.check("no animal ever targeted a player", not w.targeting(t0, uids), w.targeting(t0, uids))
    bad = w.attacks_on_players(evs)
    if bad is not None:
        sc.check("the pack logged no animal hit on a player", not bad, bad[:3])


@scenario("safety-b-hits-a", "Player B hits the owner: the owner's pet ignores B")
def safety_b_hits_a(w, sc):
    w.reset()
    w.away(w.B, (30, 10))
    uid, _ = w.tame_standing(w.A, "lion")
    w.tp(w.A, A_POS)
    w.tp(w.B, (A_POS[0] + 6, A_POS[1]))
    t0 = time.time()
    w.src.reset_window()
    w.hit(w.B, "botA", 8)
    time.sleep(3)
    sc.check("the blows landed on A", w.min_health(t0, w.A) < 20, f"A's lowest health {w.min_health(t0, w.A)}")
    sc.check("B came to no harm", w.min_health(t0, w.B) == 20, f"B's lowest health {w.min_health(t0, w.B)}")
    _safe(w, sc, t0, [w.A, w.B], w.src.events())
    sc.check("the pet is still tamed and unhurt", w.entity(uid)["tamed"] and w.entity(uid)["health"] >= 50, "")


@scenario("safety-a-hits-b", "The owner hits player B: the pet ignores B")
def safety_a_hits_b(w, sc):
    w.reset()
    uid, _ = w.tame_standing(w.A, "lion")
    w.tp(w.A, A_POS)
    w.tp(w.B, (A_POS[0] + 6, A_POS[1]))
    t0 = time.time()
    w.src.reset_window()
    w.hit(w.A, "botB", 8)
    time.sleep(3)
    sc.check("the blows landed on B", w.min_health(t0, w.B) < 20, f"B's lowest health {w.min_health(t0, w.B)}")
    sc.check("only A's fist hurt B (a lion bite would be 7)", w.min_health(t0, w.B) >= 8, f"B's lowest health {w.min_health(t0, w.B)}")
    _safe(w, sc, t0, [w.A, w.B], w.src.events())


@scenario("safety-a-hits-own-pet", "The owner hits their own pet: it does not fight back")
def safety_a_hits_pet(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame_standing(w.A, "tiger")
    w.tp(w.A, A_POS)
    t0 = time.time()
    w.src.reset_window()
    w.hit_entity(w.A, w.entity(uid)["id"], 8)
    time.sleep(3)
    sc.check("the blows landed on the pet", w.entity(uid)["health"] < 60, f"pet health {w.entity(uid)['health']}")
    sc.check("the owner took no damage", w.min_health(t0, w.A) == 20, f"A's lowest health {w.min_health(t0, w.A)}")
    _safe(w, sc, t0, [w.A, w.B], w.src.events())


@scenario("safety-wild-among-players", "Wild animals never attack players standing among them")
def safety_wild_among(w, sc):
    w.reset()
    for i, sp in enumerate(SPECIES):
        w.summon(f"gameops:{sp}", 8 + 3 * i, 8 + (i % 2) * 8)
    sc.check("all six species stood among the players", len({e["type"] for e in w.entities() if e["type"].startswith("gameops:")}) == 6, "")
    w.src.reset_window()
    t0 = w.observe(45)
    sc.check("A is unhurt", w.min_health(t0, w.A) == 20, f"lowest {w.min_health(t0, w.A)}")
    sc.check("B is unhurt", w.min_health(t0, w.B) == 20, f"lowest {w.min_health(t0, w.B)}")
    _safe(w, sc, t0, [w.A, w.B], w.src.events())


@scenario("safety-wild-provoked", "A wild animal that is hit never attacks the player who hit it")
def safety_wild_provoked(w, sc):
    w.reset()
    w.away(w.B)
    victims = []
    for i, sp in enumerate(("brown_bear", "tiger", "hyena")):
        victims.append(w.summon(f"gameops:{sp}", 12 + 4 * i, 14))
    w.src.reset_window()
    t0 = time.time()
    for v in victims:
        e = w.entity(v)
        if e:
            w.hit_entity(w.A, e["id"], 6)
    time.sleep(5)
    sc.check("the blows landed", any((w.entity(v) or {"health": 0})["health"] < 999 and (w.entity(v) or {"health": 0})["health"] > 0
                                     and w.entity(v)["health"] < {"brown_bear": 80, "tiger": 60, "hyena": 30}[w.entity(v)["type"].split(":")[1]]
                                     for v in victims), "")
    sc.check("A took no damage", w.min_health(t0, w.A) == 20, f"lowest {w.min_health(t0, w.A)}")
    _safe(w, sc, t0, [w.A, w.B], w.src.events())


@scenario("safety-sitting-pet-rival", "A sitting pet next to a wild rival does not attack")
def safety_sitting_rival(w, sc):
    w.reset()
    w.away(w.B)
    uid, _ = w.tame(w.A, "lion")
    w.sit(w.A, uid)
    spot = w.entity(uid)["pos"]
    rival = w.summon("gameops:hyena", int(spot[0]) + 3, int(spot[2]))
    t0 = w.observe(25)
    sc.check("the sitting pet did not target the rival", not w.targeting(t0, {rival}, {"gameops:lion"}), "")
    sc.check("the rival was not hurt by the pet", (w.entity(rival) or {"health": 0})["health"] >= 30,
             f"hyena health {(w.entity(rival) or {}).get('health')}")
    moved = w.dist(spot, w.entity(uid)["pos"]) if w.entity(uid) else None
    sc.check("the pet stayed where it sat", moved is not None and moved < 1.5, f"moved {moved}")


# --- auras and bursts -----------------------------------------------------------

def _needs_events(w, sc):
    """Auras on the animal itself and the burst phases leave no mark a player can read, only the pack's own log."""
    if w.src.has_events:
        return False
    sc.skip("the pack under test emits no gameops:event lines (needs the logging release)")
    return True


def _poll(fn, timeout, poll=1.0):
    end = time.time() + timeout
    while time.time() < end:
        got = fn()
        if got:
            return got
        time.sleep(poll)
    return None


def _effect_scenario(species, effect, expect_amp, _title):
    def run(w, sc):
        w.reset()
        uid, _ = w.tame_standing(w.A, species)
        w.tp(w.A, A_POS)
        got = _poll(lambda: (w.src.effects("botA") or {}).get(effect) == expect_amp, 45)
        sc.check(f"the owner gets {effect.capitalize()}", got, f"{effect} amplifier {expect_amp} expected")
        sc.check("the stranger does not", (w.src.effects("botB") or {}).get(effect) is None, "")
        w.sit(w.A, uid)
        w.pv.cmd("effect", "botA", "clear")
        time.sleep(1)
        time.sleep(12)
        sc.check("a sitting pet gives nothing", (w.src.effects("botA") or {}).get(effect) is None, "")
        evs = w.src.events()
        if evs is not None:
            auras = [e for e in evs if e["ev"] == "aura" and e["id"] == str(uid)]
            sc.check("the pack logged the aura", bool(auras) and auras[0]["effect"] == effect, auras[:1])
    return run


scenario("aura-lion-roar", "A tamed lion's roar gives its owner Strength")(
    _effect_scenario("lion", "strength", 0, "lion"))
scenario("aura-bear-guard", "A tamed brown bear guards its owner with Resistance")(
    _effect_scenario("brown_bear", "resistance", 0, "bear"))


@scenario("aura-hyena-pack", "Tamed hyenas strengthen themselves in packs of two and four")
def aura_hyena_pack(w, sc):
    if _needs_events(w, sc):
        return
    w.reset()
    w.away(w.B)
    ids = []
    for i in range(4):
        uid, _ = w.tame(w.A, "hyena", near=(A_POS[0] + 4 + i, A_POS[1] + 3))
        ids.append(uid)
    w.tp(w.A, A_POS)

    def auras(since):
        return [e for e in (w.src.events(since) or []) if e["ev"] == "aura" and e["type"] == "gameops:hyena"]

    # Freshly tamed pets stand, so all but one are sat down to build the pack up from one.
    for u in ids[1:]:
        w.sit(w.A, u)
    mark = w.src.reset_window()
    time.sleep(10)
    sc.check("one standing hyena is no pack", not auras(mark), auras(mark)[:1])
    mark = w.src.reset_window()
    w.stand(w.A, ids[1])
    got = _poll(lambda: [e for e in auras(mark) if e["amplifier"] == 0], 45)
    sc.check("two standing hyenas give Strength I", bool(got), got[:1] if got else "no aura line")
    mark = w.src.reset_window()
    w.stand(w.A, ids[2])
    w.stand(w.A, ids[3])
    got = _poll(lambda: [e for e in auras(mark) if e["amplifier"] == 1], 60)
    sc.check("four standing hyenas give Strength II", bool(got), got[:1] if got else "no aura line")


@scenario("aura-leopard-night", "A tamed black leopard strengthens itself at night only")
def aura_leopard_night(w, sc):
    if _needs_events(w, sc):
        return
    w.reset()
    w.away(w.B)
    uid, _ = w.tame_standing(w.A, "black_leopard")
    mark = w.src.reset_window()
    time.sleep(12)
    day = [e for e in (w.src.events(mark) or []) if e["ev"] == "aura" and e["id"] == str(uid)]
    sc.check("no aura by day", not day, day[:1])
    w.pv.cmd("time", "set", 14000)
    mark = w.src.reset_window()
    got = _poll(lambda: [e for e in (w.src.events(mark) or []) if e["ev"] == "aura" and e["id"] == str(uid)], 45)
    sc.check("aura at night", bool(got) and got[0]["effect"] == "strength", got[:1] if got else "no aura line")


def _burst_scenario(species, ability):
    def run(w, sc):
        if _needs_events(w, sc):
            return
        w.reset()
        w.away(w.B)
        uid, _ = w.tame_standing(w.A, species)
        w.tp(w.A, A_POS)

        def phases(since):
            return [(e["ev"], e.get("phase")) for e in (w.src.events(since) or []) if e["id"] == str(uid) and e["ev"] in ("burst", "ability")]

        mark = w.src.reset_window()
        time.sleep(10)
        sc.check("no burst on a timer alone", not phases(mark), phases(mark))
        w.summon("minecraft:zombie", A_POS[0] + 5, A_POS[1])
        started = _poll(lambda: ("burst", "start") in phases(mark), 30)
        sc.check(f"{ability} starts when the pet lands a hit", started, phases(mark))
        ended = _poll(lambda: ("burst", "end") in phases(mark), 20)
        sc.check(f"{ability} ends into the tired phase", ended, phases(mark))
        ready = _poll(lambda: ("ability", "ready") in phases(mark), 40)
        sc.check(f"{ability} is ready again afterwards", ready, phases(mark))
    return run


scenario("burst-tiger", "A tiger's charge starts on a landed hit and then tires")(_burst_scenario("tiger", "charge"))
scenario("burst-cheetah", "A cheetah's sprint starts on a landed hit and then tires")(_burst_scenario("cheetah", "sprint"))
