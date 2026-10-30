# Warrior tactics training — operator guide

How warrior profiles are trained, delivered to the crew lab and promoted. The
design is `specs/2026-10-27-warrior-self-improvement-design.md`; this is the
runbook. Everything here is lab-only: a production unit never fetches a
profile.

## What reaches a unit

A warrior fights by a **profile** (numbers per opponent family, plus tactic
order). With nothing set it fights by the shipped defaults. A lab unit's profile
comes from its squad's `GET /v1/squads/{id}/profile` on crew-control; the
trainer's bridge writes it with `PUT` on the same route.

A unit fetches and applies a profile only when **all** hold:

1. its squad id starts with `lab-` (see below);
2. `TACTICS_CONTROL_DIR` is set on the unit to the tacticsctl state directory
   (unset, the default, means the unit never asks and nothing changes);
3. the kill switch in that directory says training is on.

Otherwise the unit fights on the default. It polls every 5 s: a new profile
takes hold on the next tick of a fight in progress, no restart. A 404, or a
fetch that keeps failing past the client's five-minute stale window, also means
the default; shorter failures keep the last good profile.

## Lab squad naming

The lab-ness of a squad is its id: **a `lab-` prefix**. Squads live in memory
and are rebuilt from unit hellos, so the id is the only mark that survives a
restart, and crew-control refuses a profile `PUT` for any squad without it.

> **Deployment change needed (not made here):** the existing lab squad `alpha`
> must be renamed **`lab-alpha`** — set `CREW_SQUAD=lab-alpha` (the chart's
> `squad` value) on the lab warriors, and add `TACTICS_CONTROL_DIR` pointing at
> a volume the operator's `tacticsctl` also reads. Until then the lab units sit
> on the default whatever is trained. A unit already on a squad named `alpha`
> in production is deliberately not a lab squad and stays untouched.

## Start and stop training

State directory: `$TACTICSCTL_DIR`, default `~/.local/state/crew-tactics`. All
commands below take `--dir` to override it.

```sh
tacticsctl training on     # enable: HUMAN ONLY, needs a terminal; writes "on" to <dir>/training
tacticsctl training off    # disable, same as: tacticsctl kill on; works from anywhere
tacticsctl status          # training state, lab/production live profiles
```

Enabling training (`training on`, `kill off`), `keygen` and `approve` refuse
unless stdin is a real terminal (a TTY ioctl, so `< /dev/null` does not count).
Disabling never needs one.

**What the switch guarantees.** Agents run as the same uid as the owner, with
passwordless `sudo` and the docker group, so nothing here is a permission
boundary. The terminal checks on `training on` and `approve` are **accident
guards**: they stop a script or an agent from enabling training by mistake, not
from a hostile agent that writes the file directly. The switch's **off
direction is reliable and fail-safe**: a missing, empty, unreadable or garbled
file is off, and any actor can only push the state toward off by deleting or
corrupting it. The **on direction is advisory** against a hostile agent. Lab
safety therefore rests on the lab-only target checks and on the owner watching
the lab, not on the switch.

**Kill switch: `<dir>/training`.** Training runs only while that file holds
exactly `on`. Missing, empty, unreadable or anything else is off. Turning it off
stops the trainer and the bridge at the next check (the trainer asks before
every fight and exits 3; `--control-dir`, default the directory tacticsctl uses), and every lab unit puts the
default profile back within one poll (5 s) without asking the server. Prefer
`training off` to deleting the file, so the file records a human decision.

To train, with the switch on, run the trainer with the bridge as its evaluator
and a squad route for the sink:

```sh
export CONTROL_TOKEN=...   # the shared crew-control token; never put it in an argument
trainer run --family zombie --scenario <name> --store <store> \
  --evaluator "cmd:trainbridge --lab crew-lab --layout <layout.json> \
    --squad-url http://<crew-control> --squad lab-alpha --warriors A,B,C"
```

The trainer hands the evaluator its own `--control-dir` as `TACTICSCTL_DIR` and
its kill file (default `<store>/KILL`) as `TRAINER_STOP_FILE`; the bridge uses
them as the defaults of `--control-dir` and `--stop-file`, so either one stops
a fight in progress. An explicit flag in the evaluator command wins.

When a fight times out or the trainer is cancelled, the evaluator's whole process
group gets SIGTERM, and SIGKILL 15 s later if it is still there. The trainer
tells the evaluator when that SIGTERM will come in `TRAINER_DEADLINE` (RFC 3339);
the bridge ends its fight 15 s before it (or half the time left, if less) so
the profile is withdrawn and the arena cleared on a fresh context first, and a
SIGTERM that arrives anyway runs the same cleanup.

The trainer refuses an evaluator command whose `--lab`, `--squad` or `--squad-b`
is not the crew lab: the target must contain the token `crew-lab`, or be a
`lab-` squad id.

A duel scenario needs two squads (`--squad-b lab-bravo` for the `--team-b`
units), because a squad has one profile. A lab squad id is `lab-` followed by
lowercase letters, digits and dashes; anything else is refused before a request
is made, and crew-control applies the same rule.

The bridge treats a squad as a single-user resource:

- It takes an exclusive lock on `<control-dir>/squad-<id>.lock` for the whole
  fight and waits for another run's lock until its own fight timeout. Two
  trainers on one squad would otherwise overwrite each other's candidate and
  score the wrong profile.
- After the `PUT` it reads the profile back and requires the squad to serve
  exactly the pushed profile (same hash). With `--status-url` it then polls each
  unit's `/v1/state` `tactics.hash` (every `--poll`) until it equals the pushed
  profile's hash, for at most `--apply-wait` (default 20 s), and reads the squad
  route once more. `tactics` also carries the profile name, when the unit swapped
  it in (`applied_at`) and the kill switch as the unit last read it
  (`training_enabled`); a unit with training off for it reports the default.
  A unit that is offline, on a build without `tactics`, or on another profile
  fails the check. Without `--status-url` only the squad route is read, with
  `--apply-wait` as the pause between its two reads, which confirms what the
  route serves, not what the unit runs. If any read differs the bridge fights
  nothing, prints one aborted card and exits 1.
- `{name}` in `--status-url` is the warrior's name; crew-control's unit proxy
  resolves a name to its roster id when exactly one unit has it.
- It puts the shipped default back on every exit path, including a `PUT` whose
  answer was lost.

`--profile-dir <dir>` is not an evaluation path: no unit reads files from it.
It exists only behind `--dry-run`, which writes the candidate as
`<dir>/<unit>.json`, prints what it wrote, fights nothing, prints no scorecards
and exits 2. Without `--dry-run`, `--profile-dir` is refused.

A duel clears inventories and teleports, so only the units in
`--duelists-allow` (default `Rusty,Slate,Tinder`) may be on either team;
`--crew-names` only says who may be online and never widens that list. A
duelist that disappears from the world for three polls aborts the round (an
aborted card and exit 1), since a disconnect says nothing about the profile. The
shipped scenario book trains a ravager under `brute`, the family the warrior
fights it as, and the boss family through `warden-trial`.

## Duels

A duel needs the units to hear the start signal, and they hear it from their lab
squads. The director `PUT`s a duel state to each side's squad
(`/v1/squads/<lab-id>/duel`, token in `CONTROL_TOKEN`, lab squads only): `armed`
when the roster is set, `go` at the start with the players already out,
`idle` or `over` to end it. The signal is a squad's own members as the fighters
and the other squad's as the enemies, so it follows the teams when the sides
swap between rounds. A lab unit whose training switch is on reads it and may
hit a player only while the state is `go`, the player is an enemy and not yet
out; an unreadable squad, an unknown state or a `go` that stops being renewed
for a few seconds closes the rule again, and `idle`/`over` removes it. The
director withdraws the signal on every exit path, an interrupt included, and
the squad forgets a `go` nobody renewed after twelve minutes.

`arenadirector duel` takes `--squad-url`, `--red-squad` and `--blue-squad`
(all three or none; lab ids only, two different squads, the `--red` units in the
first and the `--blue` units in the second). Without them it only tags and
scores. `trainbridge` hands the same signal to the squads it already pushes the
profiles to (`--squad`, `--squad-b`), so a duel there needs no extra flag.

## Overnight run

`trainloop` wraps the trainer for a whole night: it rotates the requested
opponent families (train one family per round, then the next), runs the trainer
as a library with the bridge as its evaluator, prints a `trainreport` summary
after every family and at the end, and never promotes anything — champions land
in the trainer store and wait for approval as above.

Every cap is mandatory, so an overnight run cannot start unbounded. The example is a short first run: a lab fight takes minutes, so raise the caps only after watching one finish.

```sh
export CONTROL_TOKEN=...   # the shared crew-control token; never put it in an argument
trainloop overnight \
  --families zombie,skeleton --scenarios /path/to/scenarios.yaml \
  --store ~/.local/share/crew-tactics-store --control-dir "$TACTICSCTL_DIR" \
  --bridge "$(command -v trainbridge)" --lab crew-lab \
  --layout <layout.json> --squad-url http://<crew-control> --squad lab-alpha \
  --max-hours 1h --max-rounds 2 --gens-per-round 5 \
  --fights-per-gen 4 --max-total-fights 60 --max-consecutive-failures 3
```

Run it inside `systemd-run --user` or tmux so an SSH drop does not SIGINT it;
a lost terminal stops the campaign at the next check with exit 3 and the state
file keeps the budget spent. The command refuses to start while any player
outside `--crew-names` is online, the same pre-flight `arenadirector` gives
every command that changes the server.

Caps pay from what is left: `--max-hours` and `--max-total-fights` are
whole-campaign budgets, and each round is told the remaining amount. After
every round the campaign writes its state atomically to
`<store>/overnight-state.json` (it holds `StartedAt`, `ConsumedMs`,
`FightsUsed` and each finished round), and its scorecards append to
`<store>/fights.jsonl`, which the summaries read. Re-running the same command
resumes from the state file instead of resetting the caps, so an accidental
double start can only finish sooner, never over-spend. Delete the state file
deliberately, if you mean to start a fresh campaign with its own full budget.

Stopping an overnight run:

```sh
tacticsctl training off          # checked between rounds, and before every fight
touch <store>/KILL               # the campaign's own stop file (--stop-file)
```

Both stop the rotation between rounds with exit 3 and also end the fight in
progress, because they are handed to the trainer as its switch and kill file.
Exit codes: 0 finished inside its caps, 3 stopped by the switch, the stop file
or an interrupt, 1 the consecutive-failure cap (fix the lab or the bridge, then
start the same command again), 2 a malformed invocation — bad flags, a
scenario book without the family's scenario, or a corrupt state file. The
trainer's own generation cap ends only that round (`--gens-per-round` is
per-family, so the rotation keeps going while budget is left). `--scenarios`
accepts a YAML file, or a directory that holds exactly one; each round trains
against that family's highest tier in the book.

## Promotion to production

**Nothing consumes the production stage today.** No production unit fetches a
profile, and no deployment reads `<dir>/production`. Production safety is
therefore that production never reads it, plus the owner's GitHub R3 review of
any `deployments` change that would start to. `promote` and `rollback
production` only move a pointer in this store; they change no running unit.

Only `promote` and `rollback production` write production, and a promotion
needs a human:

1. The candidate beats the default over the required mirrored lab fights and
   carries its scorecard evidence path.
2. `tacticsctl lab-set <profile>` records it as the lab's profile; it must have
   been the lab's profile before it can be promoted.
3. **The owner approves it with their own key**, at a terminal:
   `tacticsctl approve <profile> --by <name> --key <private-key-file>`.
   The approval signs the profile's exact hash, the hash production runs now
   (or `none` for the default) and an expiry (`--valid-for`, default 24 h).
   `approve` and `keygen` refuse without a terminal. The key file must be a regular file owned by the caller
   with mode 600.
4. `tacticsctl --trust /etc/crew-tactics-trust --store <trainer-store> promote <profile> --to production`.
   Promote refuses unless the trust directory passes the checks below. With
   `--store` (or `TRAINER_STORE`) the profile's scorecards file must exist in
   the trainer store; without it only the path's shape is checked, and the
   command prints a note saying so.
5. `promote` refuses an approval that has expired, has no expiry or previous
   hash, or whose previous hash is not what production's `LIVE` pointer holds
   now, so an approval cannot be applied over a production state the owner did
   not see.
6. An approval is **single-use**: a successful promote moves it to
   `<dir>/approvals/used/`, kept as a record. A used file copied back is refused
   by the previous-hash check once production has moved, and by a spent-signature
   check where it has not (a rollback to the profile that was live before looks
   the same by hash). Promoting the same profile again needs a fresh `approve`.
7. Roll back with `tacticsctl rollback production` (to the parent), or
   `--to default`. Production only returns to a profile it already ran.

### Where the approver key lives

An approval proves something only if the private key was **generated and used
off this box**: on the owner's laptop, or as an `ed25519-sk` / FIDO hardware
key. A key under `~/.config/...` on the devbox is readable by every agent that
shares the owner's uid, and an agent can sign with it exactly as the owner can.
Do not generate or keep an approver key here. Until the key lives elsewhere, an
approval on this box is a record of intent, not evidence that a human agreed,
and **nothing may claim that agents cannot approve**.

`tacticsctl keygen --name <name> --out <dir>` makes a pair; it takes no default
output path and prints a reminder to run it off the devbox. Only the `.pub`
file belongs on this box.

### Trust directory: defence in depth, not a boundary

Promote refuses unless the trust directory, **every directory above it** and
each approver's `.pub` file are owned by root and not writable by group or
others (symlinks are resolved first, and the key is read from the path that was
checked). The directory must also be outside the state directory. This stops an
accidental or sloppy write from swapping a public key. It is **not a boundary
here**: an agent with `sudo` can change root-owned files, so it protects nothing
against a hostile one. The real protection is the off-box private key above and
the owner's R3 review of any change that makes production read a profile.

One-time setup, by a human, outside any agent session. Generate the key pair on
the owner's laptop, copy only `owner.pub` to the devbox, then:

```sh
sudo install -d -o root -m 0755 /etc/crew-tactics-trust
sudo install -m 0644 owner.pub /etc/crew-tactics-trust/owner.pub
export TACTICSCTL_TRUST=/etc/crew-tactics-trust
```

Approver names are lowercase letters, digits and `-`. `keygen` never overwrites
an existing key. There is deliberately no flag or environment variable that
relaxes the root-ownership check.

Promotion is an R3 change: it needs the owner's approval. The lab squad route
never carries a production profile, and a production unit never fetches one from
it.
