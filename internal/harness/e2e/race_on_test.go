//go:build race

package e2e

// raceEnabled: the race detector slows this process's QUIC dials (its
// hello and keys) by about as much as a browser's dial delay.
const raceEnabled = true
