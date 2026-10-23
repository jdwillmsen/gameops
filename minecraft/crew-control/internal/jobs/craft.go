package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

const (
	minCraftWant, maxCraftWant = 1, 64
	// Most of one item a single craft step may leave in the unit's hands;
	// a bigger run splits into further steps.
	maxCraftFill = 64
)

// CraftSpec asks for Want's single item to be crafted and left in the chest
// Place.
type CraftSpec struct {
	Place string         `json:"place"`
	Want  map[string]int `json:"want"`
}

func (s CraftSpec) validate() error {
	if len(s.Want) != 1 {
		return badSpec("craft wants exactly one item")
	}
	for it, n := range s.Want {
		if _, ok := recipes[it]; !ok {
			return unplannable("no recipe for " + it)
		}
		if n < minCraftWant || n > maxCraftWant {
			return badSpec(fmt.Sprintf("want for %s must be %d to %d", it, minCraftWant, maxCraftWant))
		}
	}
	return nil
}

func onlyWant(want map[string]int) (string, int) {
	for it, n := range want {
		return it, n
	}
	return "", 0
}

// craftPlan is a recipe expansion: how many whole crafts each recipe takes,
// in bottom-up order, and the raw leaves the tree bottoms out in.
type craftPlan struct {
	crafts map[string]int
	order  []string
	raws   map[string]int
	table  bool
}

// expand adds the whole crafts that yield n of item. Each site that asks
// for an item rounds up to whole crafts before its need is merged with the
// others, so no input is left partly covered by an earlier site's output.
func (p *craftPlan) expand(item string, n int) {
	r, ok := recipes[item]
	if !ok {
		p.raws[item] += n
		return
	}
	if r.Table {
		p.table = true
	}
	c := (n + r.Makes - 1) / r.Makes
	for _, in := range sortedItems(r.Inputs) {
		p.expand(in, r.Inputs[in]*c)
	}
	if _, seen := p.crafts[item]; !seen {
		p.order = append(p.order, item)
	}
	p.crafts[item] += c
}

// PlanCraft turns a craft goal into the steps one unit runs in a row: raw
// materials from stocked chests or gather areas, the crafts themselves
// bottom-up, and the finished item left in the target chest.
func PlanCraft(ctx context.Context, l ledger.Ledger, spec CraftSpec, free []ledger.UnitStatus, since time.Time) ([]Step, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	target, err := targetChest(ctx, l, spec.Place)
	if err != nil {
		return nil, err
	}
	item, want := onlyWant(spec.Want)
	p := &craftPlan{crafts: map[string]int{}, raws: map[string]int{}}
	p.expand(item, want)
	if p.table {
		// Table placement has no step template yet, so nothing can run
		// against one.
		return nil, unplannable("needs a crafting table")
	}

	// Stock the raws can be pulled from: any fresh-stocked chest but the
	// target, whose contents the goal is about to change.
	places, err := l.Places(ctx)
	if err != nil {
		return nil, err
	}
	type source struct {
		place ledger.Place
		items map[string]int
	}
	var sources []source
	var gathers []struct {
		item string
		qty  int
		area ledger.Place
	}
	gathered := map[string]int{}
	for _, it := range sortedItems(p.raws) {
		remaining := p.raws[it]
		for _, pl := range places {
			if pl.Kind != ledger.KindChest || pl.ID == target.ID {
				continue
			}
			rows, err := l.Stock(ctx, pl.ID)
			if err != nil {
				return nil, err
			}
			n := min(counts(rows, since)[it], remaining)
			if n <= 0 {
				continue
			}
			i := slices.IndexFunc(sources, func(s source) bool { return s.place.ID == pl.ID })
			if i < 0 {
				sources = append(sources, source{pl, map[string]int{}})
				i = len(sources) - 1
			}
			sources[i].items[it] += n
			remaining -= n
		}
		if remaining <= 0 {
			continue
		}
		var area *ledger.Place
		for i := range places {
			if places[i].Kind == ledger.KindArea && slices.Contains(places[i].Items, it) {
				area = &places[i]
				break
			}
		}
		if area == nil {
			return nil, unplannable("no source for " + it)
		}
		gathers = append(gathers, struct {
			item string
			qty  int
			area ledger.Place
		}{it, remaining, *area})
		gathered[it] = remaining
	}

	// One unit carries the whole plan: the steps chain through each other's
	// arrivals, so a second unit could only race the first.
	unit := oneFreeUnit(free)
	var steps []Step
	last := ""
	add := func(key string, tmpl json.RawMessage) {
		st := Step{Key: key, Unit: unit, Template: tmpl}
		if last != "" {
			st.DependsOn = []string{last}
		}
		steps = append(steps, st)
		last = key
	}
	for _, g := range gathers {
		for i, q := range chunks(g.qty, 1) {
			add(fmt.Sprintf("gather-%s-%d", g.item, i+1), gatherTemplate(g.item, q, g.area.Box, target.Box.Min))
		}
	}
	for _, s := range sources {
		add("withdraw-"+s.place.ID, withdrawTemplate(s.items, s.place.Box.Min))
	}
	if len(gathered) > 0 {
		add("withdraw-"+target.ID, withdrawTemplate(gathered, target.Box.Min))
	}
	for _, it := range p.order {
		makes, crafts := recipes[it].Makes, p.crafts[it]
		fill := max(maxCraftFill/makes, 1)
		parts := chunks(crafts, (crafts+fill-1)/fill)
		for i, k := range parts {
			key := fmt.Sprintf("craft-%s", it)
			if len(parts) > 1 {
				key = fmt.Sprintf("craft-%s-%d", it, i+1)
			}
			add(key, craftTemplate(it, makes*k))
		}
	}
	add("deposit", depositTemplate(map[string]int{item: want}, target.Box.Min))
	add("check", inspectTemplate(target.Box.Min))
	return steps, nil
}

// oneFreeUnit picks the free unit with the least unfinished work, in roster
// order on a tie. Without a free unit the choice falls to the scheduler.
func oneFreeUnit(free []ledger.UnitStatus) string {
	best, load := "", 0
	for _, u := range free {
		if !u.Free() {
			continue
		}
		if n := u.Queued + u.Delivered + u.Running; best == "" || n < load {
			best, load = u.ID, n
		}
	}
	return best
}

// CheckCraft compares the target's count of the wanted item with the want.
// Only a count seen since the job began is believed: an older one says
// nothing about what the job did, so it asks for a fresh look.
func checkCraft(ctx context.Context, l ledger.Ledger, spec CraftSpec, since time.Time, free []ledger.UnitStatus) (bool, string, []Step, error) {
	if err := spec.validate(); err != nil {
		return false, "", nil, err
	}
	target, err := targetChest(ctx, l, spec.Place)
	if err != nil {
		return false, "", nil, err
	}
	rows, err := l.Stock(ctx, target.ID)
	if err != nil {
		return false, "", nil, err
	}
	item, want := onlyWant(spec.Want)
	have := counts(rows, since)[item]
	fresh := false
	for _, r := range rows {
		if r.Item == item && !r.SeenAt.Before(since) {
			fresh = true
			break
		}
	}
	if !fresh {
		return false, "no fresh count for " + target.ID, []Step{
			{Key: topUpPrefix + "check", Template: inspectTemplate(target.Box.Min)},
		}, nil
	}
	short := fmt.Sprintf("have %d of %d %s", have, want, item)
	if have >= want {
		return true, "", nil, nil
	}
	steps, err := PlanCraft(ctx, l, spec, free, since)
	if errors.Is(err, ledger.ErrUnavailable) {
		return false, "", nil, err
	}
	if err != nil {
		// A gap the planner cannot close fails the job with the shortfall.
		return false, short, nil, nil
	}
	for i := range steps {
		steps[i].Key = topUpPrefix + steps[i].Key
		for j, d := range steps[i].DependsOn {
			steps[i].DependsOn[j] = topUpPrefix + d
		}
	}
	return false, short, steps, nil
}

// CraftCheck is the check for craft jobs. units supplies the free units that
// size a top-up round.
func CraftCheck(l ledger.Ledger, units func(context.Context) ([]ledger.UnitStatus, error)) Check {
	return func(ctx context.Context, j Job) (bool, string, []Step, error) {
		var spec CraftSpec
		if err := json.Unmarshal(j.Spec, &spec); err != nil {
			return false, "", nil, fmt.Errorf("craft spec: %w", err)
		}
		var free []ledger.UnitStatus
		if all, err := units(ctx); err == nil {
			for _, u := range all {
				if u.Free() {
					free = append(free, u)
				}
			}
		}
		return checkCraft(ctx, l, spec, j.Created, free)
	}
}
