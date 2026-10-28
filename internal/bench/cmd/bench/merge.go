package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// merge writes one report from the layer 6 points of several, each
// labelled (label=report.json), so that builds of mimicry measured with the
// same settings compare row by row: each table's rows carry their run's
// label, side by side. The environment and settings are the last report's,
// the mimicry commit every run's.
func merge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	out := fs.String("out", "reports", "directory for the merged report")
	name := fs.String("name", "merged", "the merged report's file name before the date")
	date := fs.String("date", "", "the merged report's date (default: the last report's)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: bench merge [flags] label=report.json label=report.json...")
		fs.PrintDefaults()
	}

	_ = fs.Parse(args)

	if fs.NArg() < 2 {
		fs.Usage()

		return errors.New("merge: needs two reports or more")
	}

	var (
		merged  report
		commits []string
	)

	for _, arg := range fs.Args() {
		label, path, ok := strings.Cut(arg, "=")
		if !ok || label == "" {
			return fmt.Errorf("merge: %q is not label=report.json", arg)
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var r report
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("merge: %s: %w", path, err)
		}

		for i := range r.Multi {
			r.Multi[i].Run = label
		}

		commits = append(commits, label+": "+r.Env.MimicryCommit)

		merged.Date = r.Date
		merged.Env = r.Env
		merged.Settings = r.Settings
		merged.Multi = append(merged.Multi, r.Multi...)
	}

	merged.Name = *name
	merged.Env.MimicryCommit = strings.Join(commits, "; ")

	if *date != "" {
		merged.Date = *date
	}

	return merged.write(*out)
}
