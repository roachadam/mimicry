package h2trace

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Load reads a trace file: a JSON array of traces, one per sample.
func Load(path string) ([]*Trace, error) {
	data, err := os.ReadFile(path) //nolint:gosec // reading the named trace file is the point
	if err != nil {
		return nil, err
	}
	var traces []*Trace
	if err := json.Unmarshal(data, &traces); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(traces) == 0 {
		return nil, fmt.Errorf("%s: no traces", path)
	}
	return traces, nil
}

// Variation is a feature the samples do not agree on, with each sample's
// value in order ("-" when a sample lacks it).
type Variation struct {
	Feature string
	Values  []string
}

// Slack is how far two measurements may differ and still count as the same:
// 2% and at least one 16384-byte frame. Credit is returned once a threshold
// is crossed, so an increment depends on where the server's DATA frames fell
// and, for Safari, on how many arrived between its checks (up to five,
// measured).
func Slack(v uint32) uint32 {
	return max(16384, v/50)
}

// Compare lists the features on which the samples' traces disagree. Exact
// features: the order of requests, each request's stream, HPACK instruction
// shapes (values are left out, since :authority carries the port) and HEADERS
// priority, and the order of every frame other than DATA and WINDOW_UPDATE.
// Measured features, equal within Slack: each request's largest DATA frame
// and median WINDOW_UPDATE, and the connection's median WINDOW_UPDATE.
func Compare(samples []*Trace) []Variation {
	exact := make([]map[string]string, len(samples))
	measured := make([]map[string]uint32, len(samples))
	keys := map[string]bool{}
	for i, t := range samples {
		exact[i], measured[i] = Features(t)
		for k := range exact[i] {
			keys[k] = true
		}
		for k := range measured[i] {
			keys[k] = true
		}
	}
	var out []Variation
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		values := make([]string, len(samples))
		varies := false
		for i := range samples {
			if v, ok := exact[i][k]; ok {
				values[i] = v
				varies = varies || v != exact[0][k]
				continue
			}
			v, ok := measured[i][k]
			if !ok {
				values[i] = "-"
				varies = true
				continue
			}
			values[i] = strconv.FormatUint(uint64(v), 10)
			first, ok := measured[0][k]
			varies = varies || !ok || absDiff(v, first) > Slack(first)
		}
		if varies {
			out = append(out, Variation{
				Feature: k,
				Values:  values,
			})
		}
	}
	return out
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// Features extracts the features Compare compares, keyed by name: exact ones
// as strings, measured ones as numbers.
func Features(t *Trace) (map[string]string, map[string]uint32) {
	exact := map[string]string{}
	measured := map[string]uint32{}
	paths := map[uint32]string{}
	order := make([]string, 0, len(t.Requests))
	for _, r := range t.Requests {
		key := r.Path
		for n := 2; slices.Contains(order, key); n++ {
			key = r.Path + "#" + strconv.Itoa(n)
		}
		order = append(order, key)
		paths[r.Stream] = key
		exact["request["+key+"].stream"] = strconv.FormatUint(uint64(r.Stream), 10)
		exact["request["+key+"].hpack"] = shapes(r.Instructions)
	}
	exact["requests"] = strings.Join(order, " ")

	var frames []string
	dataMax := map[string]uint32{}
	updates := map[string][]uint32{}
	headersSeen := false
	var prev Frame
	for _, f := range t.Frames {
		where := "conn"
		if f.Stream != 0 {
			where = paths[f.Stream]
			if where == "" {
				where = "stream " + strconv.FormatUint(uint64(f.Stream), 10)
			}
		}
		switch f.Type {
		case "DATA":
			dataMax[where] = max(dataMax[where], f.Length)
		case "WINDOW_UPDATE":
			switch {
			case prev.Type == "HEADERS" && prev.Stream == f.Stream && f.Stream != 0:
				// A top-up sent with the request (Firefox), not credit for
				// data received.
				exact["request["+where+"].window_top_up"] = strconv.FormatUint(uint64(f.Increment), 10)
			case headersSeen:
				// Credit returns only: the preface's update comes before
				// any request.
				updates[where] = append(updates[where], f.Increment)
			}
		default:
			s := f.Type + "@" + where
			if f.Priority != nil {
				p := f.Priority
				parent := "0"
				if p.DependsOn != 0 {
					parent = paths[p.DependsOn]
				}
				prio := fmt.Sprintf("(dep %s excl %t weight %d)", parent, p.Exclusive, p.Weight)
				s += prio
				if f.Type == "HEADERS" {
					exact["request["+where+"].priority"] = prio
				}
			}
			if f.ErrCode != "" {
				s += "(" + f.ErrCode + ")"
			}
			frames = append(frames, s)
			if f.Type == "HEADERS" {
				headersSeen = true
			}
		}
		prev = f
	}
	exact["frames"] = strings.Join(frames, " ")
	for where, m := range dataMax {
		measured["data_max["+where+"]"] = m
	}
	for where, incs := range updates {
		s := slices.Clone(incs)
		slices.Sort(s)
		measured["window_update_median["+where+"]"] = s[len(s)/2]
	}
	return exact, measured
}

// shapes renders instructions without their values.
func shapes(ins []Instruction) string {
	parts := make([]string, len(ins))
	for i, in := range ins {
		if in.Rep == SizeUpdate {
			parts[i] = "size_update(" + strconv.FormatUint(in.Size, 10) + ")"
			continue
		}
		h := ""
		if in.HuffmanName {
			h += "h"
		}
		if in.HuffmanValue {
			h += "H"
		}
		parts[i] = fmt.Sprintf("%s:%d%s:%s", in.Rep, in.Index, h, in.Name)
	}
	return strings.Join(parts, " ")
}
