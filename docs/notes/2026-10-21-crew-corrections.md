# Crew movement corrections at the hub

## Finding

The original production logs did not identify why units receive 1–6 movement
corrections per minute. I read the last 30 minutes from all 20 `crew-01` through
`crew-20` deployments in `jdwillmsen-prd`. There were **0 correction events in
those logs**. This is not evidence of zero corrections: `internal/act/act.go`
increments `crew_movement_corrections_total` for
`CorrectPlayerMovePrediction`, but did not log a correction event. The
`movement_corrected` event now records quantized movement state and nearby
block names without absolute coordinates.

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

Diagnosis remains inconclusive until the new event is collected in production.
The one concrete code-level lead is the already documented stair-descent timing
difference, but treating it as the hub cause would be a guess. A 30-minute log
pull grouped by support block, feet block, block ahead, and active skill will
identify which movement situations correlate with corrections. Each event
describes the input the server named in its correction (`tick_found: false`
means that tick had aged out and the newest pose was used instead). Groups are
ranked by logged lines; `corrections` adds each line's `suppressed` count, which
covers the preceding 10 s in possibly different situations, so it is only
approximate. Non-JSON log lines are skipped:

```sh
for n in $(seq -w 1 20); do kubectl -n jdwillmsen-prd logs "deploy/crew-$n" --since=30m; done | jq -R 'fromjson? | select(.event == "movement_corrected")' | jq -s 'group_by([.below, .feet, .ahead, .skill]) | map({below: .[0].below, feet: .[0].feet, ahead: .[0].ahead, skill: .[0].skill, lines: length, corrections: (length + (map(.suppressed // 0) | add))}) | sort_by(-.lines)'
```

No live-cluster changes were made.
