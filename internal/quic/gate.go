// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package quic

import (
	"context"
	"sync"
)

// An gate is a monitor (mutex + condition variable) with one bit of state.
//
// The condition may be either set or unset.
// Lock operations may be unconditional, or wait for the condition to be set.
// Unlock operations record the new state of the condition.
//
// mimicry: a sync.Mutex and a wake-up channel, where upstream's gate is two
// channels whose token passes straight to a blocked waiter. A goroutine woken
// that way owned the gate before it ran, so the conn's loop, delivering the
// next frame for the same stream, blocked until the reader had been
// scheduled, run and unlocked: the loop spent half its time waiting on
// readers' ingates under HTTP/3 load (bench 2026-10-28), serializing the
// connection on one core. Here a woken waiter takes the lock only once it
// runs, as a sync.Mutex waiter does.
type gate struct {
	mu sync.Mutex
	// set is the condition, guarded by mu.
	set bool
	// wake holds a token when the condition has been set since a waiter
	// last looked.
	wake chan struct{}
}

// newGate returns a new, unlocked gate with the condition unset.
func newGate() gate {
	return gate{
		wake: make(chan struct{}, 1),
	}
}

// lock acquires the gate unconditionally.
// It reports whether the condition is set.
func (g *gate) lock() (set bool) {
	g.mu.Lock()
	return g.set
}

// waitAndLock waits until the condition is set before acquiring the gate.
// If the context expires, waitAndLock returns an error and does not acquire the gate.
func (g *gate) waitAndLock(ctx context.Context) error {
	for {
		g.mu.Lock()
		if g.set {
			return nil
		}
		g.mu.Unlock()
		select {
		case <-g.wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// lockIfSet acquires the gate if and only if the condition is set.
func (g *gate) lockIfSet() (acquired bool) {
	if !g.mu.TryLock() {
		return false
	}
	if g.set {
		return true
	}
	g.mu.Unlock()
	return false
}

// unlock sets the condition and releases the gate.
func (g *gate) unlock(set bool) {
	g.set = set
	g.mu.Unlock()
	if set {
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}
}

// unlockFunc sets the condition to the result of f and releases the gate.
// Useful in defers.
func (g *gate) unlockFunc(f func() bool) {
	g.unlock(f())
}
