# Crew movement corrections at the hub

## Finding

The available production logs do not identify why units receive 1–6 movement
corrections per minute. I read the last 30 minutes from all 20 `crew-01` through
`crew-20` deployments in `jdwillmsen-prd`. There were **0 correction events in
those logs**. This is not evidence of zero corrections: `internal/act/act.go`
increments `crew_movement_corrections_total` for
`CorrectPlayerMovePrediction`, but does not log a correction event. The emitted
skill and life events also do not include the unit's current block types or
movement state at correction time.

## Evidence by candidate cause

| Candidate | Corrections attributable from logs | Code evidence |
| --- | ---: | --- |
| Slabs, stairs, paths, fences or walls | 0 | The logs contain no correction context or block types. `internal/world/shape` models slab and stair geometry and tall fence/wall collision; path blocks use the block-table fallback. |
| Step-up height or standing delta | 0 | No correction-time step-up or delta is logged. `internal/act/pose.go` uses a 0.6 step height and deliberately sends a standing vertical delta when stepping onto a ledge. |
| On-ground state on partial shapes | 0 | No correction-time on-ground state or supporting block is logged. |
| Stair descent / jump and fall timing | 0 | No correction-time movement history is logged. The README documents stair descent as a known weak spot, but there is no evidence the observed hub corrections occurred on stairs or during descent. |
| Float rounding | 0 | No correction deltas or predicted-versus-server positions are logged for comparison. |

The counts above mean **no corrections can be assigned to a cause from these
logs**, not that each cause happened zero times. The window contains activity
events such as task starts/finishes and defence events, but none are paired
with correction timestamps. No evidence supports changing the collision model
or adding a shape-specific test with an expected Bedrock position yet.

## Conclusion

Diagnosis is inconclusive. The one concrete code-level lead is the already
documented stair-descent timing difference, but treating it as the hub cause
would be a guess. To distinguish these causes, the correction counter needs
correlated movement evidence (at minimum the active skill, support block
shape, on-ground/step-up state, and a quantized predicted-versus-server delta)
collected without logging world coordinates. No live-cluster changes were made.
