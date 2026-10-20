package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

const (
	// freshFor is how long a ledger count is trusted without looking again.
	freshFor = 10 * time.Minute

	minWant, maxWant = 1, 256
	// perUnit is the amount one unit is given before a second helps; maxChunk
	// is the most one gather step may ask for.
	perUnit, maxChunk = 16, 64

	topUpPrefix = "r1-"
)

// gatherable lists the items some unit template can collect.
var gatherable = []string{"cobblestone", "oak_log"}

// ErrUnplannable marks a goal the world cannot meet as asked, such as a
// missing place or an item nothing produces; the message is safe to show.
var ErrUnplannable = errors.New("jobs: cannot plan")

type unplannable string

func (e unplannable) Error() string        { return string(e) }
func (e unplannable) Is(target error) bool { return target == ErrUnplannable }

type badSpec string

func (e badSpec) Error() string        { return string(e) }
func (e badSpec) Is(target error) bool { return target == ErrInvalid }

// StockSpec asks for Want[item] of each item to be in the chest Place.
type StockSpec struct {
	Place string         `json:"place"`
	Want  map[string]int `json:"want"`
}

func sortedItems(want map[string]int) []string {
	items := make([]string, 0, len(want))
	for it := range want {
		items = append(items, it)
	}
	sort.Strings(items)
	return items
}

func (s StockSpec) validate() error {
	if len(s.Want) == 0 {
		return badSpec("want nothing")
	}
	for _, it := range sortedItems(s.Want) {
		if !slices.Contains(gatherable, it) {
			return unplannable("no source for " + it)
		}
		if n := s.Want[it]; n < minWant || n > maxWant {
			return badSpec(fmt.Sprintf("want for %s must be %d to %d", it, minWant, maxWant))
		}
	}
	return nil
}

func targetChest(ctx context.Context, l ledger.Ledger, id string) (ledger.Place, error) {
	p, ok, err := l.Place(ctx, id)
	if err != nil {
		return ledger.Place{}, err
	}
	if !ok {
		return ledger.Place{}, unplannable("place " + id + " not found")
	}
	if p.Kind != ledger.KindChest {
		return ledger.Place{}, unplannable("place " + id + " is not a chest")
	}
	return p, nil
}

// counts sums the stock seen at or after since.
func counts(rows []ledger.Stock, since time.Time) map[string]int {
	have := map[string]int{}
	for _, r := range rows {
		if !r.SeenAt.Before(since) {
			have[r.Item] = r.Count
		}
	}
	return have
}

// staleFor reports whether any wanted item lacks a row seen at or after
// since. Items are judged one by one: a partial report restamps only the
// items it carried.
func staleFor(rows []ledger.Stock, want map[string]int, since time.Time) bool {
	seen := map[string]bool{}
	for _, r := range rows {
		if !r.SeenAt.Before(since) {
			seen[r.Item] = true
		}
	}
	for item := range want {
		if !seen[item] {
			return true
		}
	}
	return false
}

// chunks splits n into parts as equal as possible: k is the wish, raised
// until no part exceeds maxChunk.
func chunks(n, k int) []int {
	k = max(k, (n+maxChunk-1)/maxChunk)
	k = min(max(k, 1), n)
	out := make([]int, k)
	for i := range out {
		out[i] = n / k
		if i < n%k {
			out[i]++
		}
	}
	return out
}

// PlanStock turns a stock goal into steps. free only sizes the split: chunks
// are never pinned, so the scheduler gives each to whichever unit frees up.
func PlanStock(ctx context.Context, l ledger.Ledger, spec StockSpec, free []ledger.UnitStatus, since time.Time) ([]Step, error) {
	return planStock(ctx, l, spec, free, since, time.Now())
}

func planStock(ctx context.Context, l ledger.Ledger, spec StockSpec, free []ledger.UnitStatus, since, now time.Time) ([]Step, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	target, err := targetChest(ctx, l, spec.Place)
	if err != nil {
		return nil, err
	}
	rows, err := l.Stock(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	have := counts(rows, since)
	stale := staleFor(rows, spec.Want, now.Add(-freshFor))

	var steps, work []Step
	var after []string
	if stale {
		steps = append(steps, Step{Key: "inspect-target", Template: inspectTemplate(target.Box.Min)})
		after = []string{"inspect-target"}
	}
	places, err := l.Places(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range sortedItems(spec.Want) {
		remaining := spec.Want[item] - have[item]
		if remaining <= 0 {
			continue
		}
		for _, p := range places {
			if remaining <= 0 {
				break
			}
			if p.Kind != ledger.KindChest || p.ID == target.ID {
				continue
			}
			src, err := l.Stock(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			var n int
			for _, r := range src {
				if r.Item == item && now.Sub(r.SeenAt) <= freshFor {
					n = r.Count
				}
			}
			n = min(n, remaining)
			if n <= 0 {
				continue
			}
			work = append(work, Step{Key: fmt.Sprintf("haul-%s-%s", item, p.ID), DependsOn: slices.Clone(after),
				Template: haulTemplate(map[string]int{item: n}, []ledger.Pos{p.Box.Min}, target.Box.Min)})
			remaining -= n
		}
		if remaining <= 0 {
			continue
		}
		var area *ledger.Place
		for i := range places {
			if places[i].Kind == ledger.KindArea && slices.Contains(places[i].Items, item) {
				area = &places[i]
				break
			}
		}
		if area == nil {
			return nil, unplannable("no source for " + item)
		}
		for i, q := range chunks(remaining, min(len(free), (remaining+perUnit-1)/perUnit)) {
			work = append(work, Step{Key: fmt.Sprintf("gather-%s-%d", item, i+1), DependsOn: slices.Clone(after),
				Template: gatherTemplate(item, q, area.Box, target.Box.Min)})
		}
	}
	steps = append(steps, work...)
	all := keysOf(steps)
	return append(steps, Step{Key: "check", DependsOn: all, Template: inspectTemplate(target.Box.Min)}), nil
}

func keysOf(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Key
	}
	return out
}

// CheckStock compares the target's count with the want. Only counts seen
// since the job began are believed: an older count says nothing about what
// the job did, so a wanted item counted only before then asks for a fresh
// look. topUp is one more round of work that could close the gap.
func CheckStock(ctx context.Context, l ledger.Ledger, spec StockSpec, since time.Time, free []ledger.UnitStatus) (ok bool, short string, topUp []Step, err error) {
	return checkStock(ctx, l, spec, since, free, time.Now())
}

func checkStock(ctx context.Context, l ledger.Ledger, spec StockSpec, since time.Time, free []ledger.UnitStatus, now time.Time) (bool, string, []Step, error) {
	target, err := targetChest(ctx, l, spec.Place)
	if err != nil {
		return false, "", nil, err
	}
	rows, err := l.Stock(ctx, target.ID)
	if err != nil {
		return false, "", nil, err
	}
	have := counts(rows, since)
	fresh := false
	for _, r := range rows {
		if !r.SeenAt.Before(since) {
			fresh = true
		} else if _, wanted := spec.Want[r.Item]; wanted {
			fresh = false
			break
		}
	}
	if !fresh {
		return false, "no fresh count for " + target.ID, []Step{
			{Key: topUpPrefix + "check", Template: inspectTemplate(target.Box.Min)},
		}, nil
	}
	var short []string
	for _, item := range sortedItems(spec.Want) {
		if have[item] < spec.Want[item] {
			short = append(short, fmt.Sprintf("have %d of %d %s", have[item], spec.Want[item], item))
		}
	}
	if len(short) == 0 {
		return true, "", nil, nil
	}
	steps, err := planStock(ctx, l, spec, free, since, now)
	if errors.Is(err, ledger.ErrUnavailable) {
		return false, "", nil, err
	}
	if err != nil {
		// A gap the planner cannot close fails the job with the shortfall.
		return false, strings.Join(short, "; "), nil, nil
	}
	for i := range steps {
		steps[i].Key = topUpPrefix + steps[i].Key
		for j, d := range steps[i].DependsOn {
			steps[i].DependsOn[j] = topUpPrefix + d
		}
	}
	return false, strings.Join(short, "; "), steps, nil
}

// StockCheck is the check for stock jobs. units supplies the free units that
// size a top-up round.
func StockCheck(l ledger.Ledger, units func(context.Context) ([]ledger.UnitStatus, error), now func() time.Time) Check {
	return func(ctx context.Context, j Job) (bool, string, []Step, error) {
		var spec StockSpec
		if err := json.Unmarshal(j.Spec, &spec); err != nil {
			return false, "", nil, fmt.Errorf("stock spec: %w", err)
		}
		var free []ledger.UnitStatus
		if all, err := units(ctx); err == nil {
			for _, u := range all {
				if u.Free() {
					free = append(free, u)
				}
			}
		}
		return checkStock(ctx, l, spec, j.Created, free, now())
	}
}
