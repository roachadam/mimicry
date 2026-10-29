package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
)

// mergePasses writes one report from several passes of the same settings
// (bench.sh runs each as an invocation of its own, the clients in another
// order each time): every layer 3 and layer 6 point becomes the median of
// each of its values over the passes it was valid in, with each value's
// range (lowest, highest) beside it. Layers 4 and 5, run once, come from
// the first pass that has them, the micro results from the first pass.
// stock are passes run with the client's UDP receive buffers at stock
// Linux values (BENCH_UDP_STOCK), merged the same way into a section of
// their own.
func mergePasses(paths, stock []string, out, name, date string) error {
	passes, err := readReports(paths)
	if err != nil {
		return err
	}

	merged := report{
		Name:     name,
		Date:     passes[0].Date,
		Env:      passes[0].Env,
		Settings: passes[0].Settings,
		Micro:    passes[0].Micro,
		MicroRaw: passes[0].MicroRaw,
		Passes:   len(passes),
	}

	if date != "" {
		merged.Date = date
	}

	var commits []string

	for _, p := range passes {
		merged.PassOrders = append(merged.PassOrders, p.Settings.Clients)

		if !slices.Contains(commits, p.Env.MimicryCommit) {
			commits = append(commits, p.Env.MimicryCommit)
		}

		if merged.Idle == nil && len(p.Idle) > 0 {
			merged.Idle = p.Idle
			merged.Settings.IdleN, merged.Settings.IdleNs = p.Settings.IdleN, p.Settings.IdleNs
		}

		if merged.Pages == nil && len(p.Pages) > 0 {
			merged.Pages = p.Pages
			merged.Env.Netem = p.Env.Netem
			merged.Settings.Reps, merged.Settings.RepsLoss = p.Settings.Reps, p.Settings.RepsLoss
		}

		for _, l := range p.Settings.Layers {
			if !slices.Contains(merged.Settings.Layers, l) {
				merged.Settings.Layers = append(merged.Settings.Layers, l)
			}
		}
	}

	slices.Sort(merged.Settings.Layers)

	merged.Env.MimicryCommit = strings.Join(commits, "; ")

	if merged.Load, err = mergeLoad(passes); err != nil {
		return err
	}

	if merged.Multi, err = mergeMulti(passes); err != nil {
		return err
	}

	if len(stock) > 0 {
		sp, err := readReports(stock)
		if err != nil {
			return err
		}

		env := sp[0].Env

		merged.StockEnv = &env
		merged.StockPasses = len(sp)

		if merged.StockLoad, err = mergeLoad(sp); err != nil {
			return err
		}

		if merged.StockMulti, err = mergeMulti(sp); err != nil {
			return err
		}
	}

	return merged.write(out)
}

func readReports(paths []string) ([]*report, error) {
	out := make([]*report, 0, len(paths))

	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		r := new(report)
		if err := json.Unmarshal(b, r); err != nil {
			return nil, fmt.Errorf("merge: %s: %w", path, err)
		}

		out = append(out, r)
	}

	return out, nil
}

// mergeLoad merges the passes' layer 3 points, in the first pass's order of
// everything but the clients, whose order is the first pass's -clients.
func mergeLoad(passes []*report) ([]loadPoint, error) {
	type key struct {
		server, proto, op, cond string
		conc                    int
	}

	var (
		keys    []key
		clients = passes[0].Settings.Clients
		byKey   = map[key]map[string][]loadPoint{}
	)

	for _, p := range passes {
		for _, lp := range p.Load {
			k := key{
				server: lp.Server,
				proto:  string(lp.Proto),
				op:     lp.Op,
				cond:   lp.Condition,
				conc:   lp.Conc,
			}

			if byKey[k] == nil {
				byKey[k] = map[string][]loadPoint{}
				keys = append(keys, k)
			}

			if !slices.Contains(clients, lp.Client) {
				clients = append(clients, lp.Client)
			}

			byKey[k][lp.Client] = append(byKey[k][lp.Client], lp)
		}
	}

	var out []loadPoint

	for _, k := range keys {
		for _, c := range clients {
			pts := byKey[k][c]
			if len(pts) == 0 {
				continue
			}

			lp, err := mergeLoadPoint(pts)
			if err != nil {
				return nil, err
			}

			out = append(out, lp)
		}
	}

	return out, nil
}

// mergeLoadPoint is one point's passes merged: the median of the valid
// ones, or the first when none is valid.
func mergeLoadPoint(pts []loadPoint) (loadPoint, error) {
	var (
		valid []loadPoint
		bound string
		udps  []udpDelta
	)

	for _, lp := range pts {
		if !lp.valid() {
			continue
		}

		valid = append(valid, lp)

		if bound == "" {
			bound = lp.Result.ServerBound
		}

		if lp.UDP != nil {
			udps = append(udps, *lp.UDP)
		}
	}

	if len(valid) == 0 {
		return pts[0], nil
	}

	res, ranges, err := medianJSON(results(valid, func(lp loadPoint) any { return lp.Result }))
	if err != nil {
		return loadPoint{}, err
	}

	out := valid[0]

	out.Passes = len(valid)
	out.Ranges = ranges
	out.Result = nil

	if err := json.Unmarshal(res, &out.Result); err != nil {
		return loadPoint{}, err
	}

	// A pass that found the server the limit makes the point server-bound.
	out.Result.ServerBound = bound

	if len(udps) > 0 {
		u, _, err := medianJSON(results(udps, func(u udpDelta) any { return u }))
		if err != nil {
			return loadPoint{}, err
		}

		out.UDP = nil
		if err := json.Unmarshal(u, &out.UDP); err != nil {
			return loadPoint{}, err
		}
	}

	return out, nil
}

// mergeMulti merges the passes' layer 6 points as mergeLoad does.
func mergeMulti(passes []*report) ([]multiPoint, error) {
	type key struct {
		k multiKey
		n int
	}

	var (
		keys    []key
		clients = passes[0].Settings.Clients
		byKey   = map[key]map[string][]multiPoint{}
	)

	for _, p := range passes {
		for _, mp := range p.Multi {
			k := key{
				k: mp.key(),
				n: mp.N,
			}

			if byKey[k] == nil {
				byKey[k] = map[string][]multiPoint{}
				keys = append(keys, k)
			}

			if !slices.Contains(clients, mp.Client) {
				clients = append(clients, mp.Client)
			}

			byKey[k][mp.Client] = append(byKey[k][mp.Client], mp)
		}
	}

	var out []multiPoint

	for _, k := range keys {
		for _, c := range clients {
			pts := byKey[k][c]
			if len(pts) == 0 {
				continue
			}

			var valid []multiPoint

			for _, mp := range pts {
				if mp.valid() {
					valid = append(valid, mp)
				}
			}

			if len(valid) == 0 {
				out = append(out, pts[0])

				continue
			}

			res, ranges, err := medianJSON(results(valid, func(mp multiPoint) any { return mp.Result }))
			if err != nil {
				return nil, err
			}

			mp := valid[0]

			mp.Passes = len(valid)
			mp.Ranges = ranges
			mp.Result = nil

			if err := json.Unmarshal(res, &mp.Result); err != nil {
				return nil, err
			}

			out = append(out, mp)
		}
	}

	return out, nil
}

func results[T any](v []T, get func(T) any) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = get(x)
	}

	return out
}

// medianJSON returns, as JSON, the first value with every number (nested
// objects' too) replaced by its median over vals, the mean of the middle
// two with an even count (rounded when both are whole, so integers stay
// integers); and each number's lowest
// and highest, by its JSON path ("rps", "cost.firstMs"), when there are
// two values or more. Arrays and strings are the first value's.
func medianJSON(vals []any) ([]byte, map[string][2]float64, error) {
	maps := make([]map[string]any, len(vals))

	for i, v := range vals {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, nil, err
		}

		if err := json.Unmarshal(b, &maps[i]); err != nil {
			return nil, nil, err
		}
	}

	var ranges map[string][2]float64
	if len(maps) > 1 {
		ranges = map[string][2]float64{}
	}

	medianInto(maps, "", ranges)

	b, err := json.Marshal(maps[0])

	return b, ranges, err
}

// medianInto writes into maps[0] the median of each number over maps.
func medianInto(maps []map[string]any, prefix string, ranges map[string][2]float64) {
	names := make([]string, 0, len(maps[0]))
	for name := range maps[0] {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		switch first := maps[0][name].(type) {
		case float64:
			nums := make([]float64, 0, len(maps))

			for _, m := range maps {
				if v, ok := m[name].(float64); ok {
					nums = append(nums, v)
				}
			}

			slices.Sort(nums)

			// The middle value, or the mean of the middle two (rounded
			// when they are whole: the field may be an integer).
			lo := nums[(len(nums)-1)/2]
			mid := lo

			if len(nums)%2 == 0 {
				hi := nums[len(nums)/2]

				mid = (lo + hi) / 2
				if lo == math.Trunc(lo) && hi == math.Trunc(hi) {
					mid = math.Round(mid)
				}
			}

			maps[0][name] = mid

			if ranges != nil && len(nums) > 1 {
				ranges[prefix+name] = [2]float64{nums[0], nums[len(nums)-1]}
			}

		case map[string]any:
			sub := []map[string]any{first}

			for _, m := range maps[1:] {
				if v, ok := m[name].(map[string]any); ok {
					sub = append(sub, v)
				}
			}

			medianInto(sub, prefix+name+".", ranges)
		}
	}
}
