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
tacticsctl training on     # enable: writes "on" to <dir>/training
tacticsctl training off    # disable, same as: tacticsctl kill on
tacticsctl status          # training state, lab/production live profiles
```

**Kill switch: `<dir>/training`.** Training runs only while that file holds
exactly `on`. Missing, empty, unreadable or anything else is off. Turning it off
stops the trainer and the bridge at the next check, and every lab unit puts the
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

A duel scenario needs two squads (`--squad-b lab-bravo` for the `--team-b`
units), because a squad has one profile. The bridge puts the shipped default
back on every exit path. `--profile-dir <dir>` remains as the file-based
alternative to `--squad-url`.

## Promotion to production

Only `promote` and `rollback production` write production, and a promotion
needs a human:

1. The candidate beats the default over the required mirrored lab fights and
   carries its scorecard evidence path.
2. `tacticsctl lab-set <profile>` records it as the lab's profile; it must have
   been the lab's profile before it can be promoted.
3. **A human approves it with their own key**, at a terminal:
   `tacticsctl approve <profile> --by <name> --key <private-key-file>`.
   The approval names the profile's exact hash. `approve` and `keygen` refuse
   without a terminal. **Agents never approve, hold an approver key or write the
   trust directory.**
4. `tacticsctl --trust <trust-dir> promote <profile> --to production`. The trust
   directory (approvers' public keys) must be outside the state directory and
   closed to group and others; a promote with the default location refuses.
5. Roll back with `tacticsctl rollback production` (to the parent), or
   `--to default`.

Promotion is an R3 change: it needs the owner's approval. The lab squad route
never carries a production profile, and a production unit never fetches one from
it.
