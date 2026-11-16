//go:build !windows

package server

// pipeGone are the platform's own errors for the SDK's end closed beyond
// EPIPE and ECONNRESET: none outside Windows.
var pipeGone []error
