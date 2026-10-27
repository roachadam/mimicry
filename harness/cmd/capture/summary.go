package main

import (
	"encoding/json"
	"os"
)

// summaryLine is one line of the -summary file: a scenario's capture as
// written, for cmd/refresh, which checks every capture of a run names the
// same browser version and reports how long each scenario took.
type summaryLine struct {
	Browser string `json:"browser"`
	// Scenario is the capture's, with the .local-roots suffix where the
	// capture tool added it.
	Scenario  string  `json:"scenario"`
	Name      string  `json:"name"`
	UserAgent string  `json:"user_agent"`
	Samples   int     `json:"samples"`
	Seconds   float64 `json:"seconds"`
	// File is the first file written, in the output directory.
	File string `json:"file"`
}

// appendSummary appends l to path as one JSON line; no path, no summary.
func appendSummary(path string, l summaryLine) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
