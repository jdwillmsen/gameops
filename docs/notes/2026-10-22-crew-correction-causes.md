# Root causes of crew movement corrections

Follow-up to `2026-10-21-crew-corrections.md`: the diagnostic event that note
introduced has now been collected, and the candidates could be settled against
code and unit tests. One hour of events from one unit was analysed (kept out of
the repo; the event carries block names and distances, never coordinates).

## What the events showed

42 correction events. 20 of them reported a distance of 0 — the server's
corrected position equalled the prediction's. 17 of those could not find the
tick they named (their block context was unknown too), and 3 were corrections
the prediction had already matched, arriving in bursts of 4–7 per second during
active work. The 22 events with a distance above 0 split into three
respawn-like teleports of hundreds of blocks along one axis (rejected below),
four tiny 0.02–0.04 steps during walking, fighting and a chest approach, and
the rest 0.6–1.0 with the unit's feet inside a block it was mining or felling.

## Confirmed causes, all fixed

**Chest collision was the block table's, not the server's.** The dragonfly
block table makes a chest 0.95 tall, inset a fortieth. A walking prediction
built on that box stops 1/80 of a block short of the real face — matching the
tiny correction on the chest approach — and, worse, a unit the server
corrected to rest at 0.875 on a chest had no support under the taller box, so
the prediction fell through the chest entirely. Fixed by resolving chest,
trapped_chest and ender_chest to the true 0.875-tall, sixteenth-inset box in
`internal/world/shape/partial.go` before the table is asked.

**No-move corrections reset the unit's velocity.** Any
`CorrectPlayerMovePrediction` rebuilt the pose from scratch. A correction
repeating the position the unit already sent — the server overruling only the
ground flag or the facing — then threw away a mid-jump or mid-fall vertical
velocity and zeroed or rewound a walk's momentum. The next short input earned
another correction, which is the burst pattern: every tick of a jump over
dug-out mine-shaft ground could be answered by a correction that cut the arc
short. Such corrections now adopt flags and facing only; only one that really
moves the unit rewrites the motion (`internal/act/act.go`).

**The sent-input history was too short to compare a third of corrections.**
The 64-tick window outlives a nominal round trip, but corrections in the wild
name ticks several seconds old (repeats of ones already adopted, answers
delayed past lag). Those fell back to the newest pose, which made them read as
no-ops and hid their real distance. The window is now 256 ticks and the log
still says honestly when a named tick is gone.

## Rejected causes

**Respawn teleports.** The three large corrections all moved the unit along
one axis far beyond any walking distance, onto the block of the spawn
platform, after a period of low activity. Respawn and dimension moves are
server-driven; the prediction is expected to lose them.

**Feet inside a block being mined or felled.** The mid-size corrections with
the unit's feet in stone or oak log pair with digs that had just completed.
The block store follows the server's updates; while an update is in flight
the prediction and the server legitimately disagree by the block it is
standing in. No client-side defect was found, and the events stop once the
update lands.

**The 0.02–0.04 walking drift as its own cause.** With the no-move fix in
place these are explained: a walk whose momentum a no-move correction had
just zeroed or rewound walks one short tick, and the server nudges it. The
drift lines in the sample all sit inside the same correction bursts.

## What was deliberately left alone

Adopting a moving correction's delta as carry — the lab measured that this
resumes walking without a second correction, and tests pin it. The dig-chase
behaviour around freshly broken blocks. Rejected respawn handling.

Each fix landed as its own commit with the failing test that proved the cause
first; `go test -race`, `go vet` and `gofmt` are clean module-wide.
