package server

import (
	"io"
	"net/http"
	"sync"

	"github.com/roachadam/mimicry"
	"github.com/roachadam/mimicry/cookies"
	"github.com/roachadam/mimicry/internal/rpc/convert"
	"github.com/roachadam/mimicry/profile"
)

// objects are a connection's objects by id. Ids start at 1 and are never
// reused within the connection.
type objects struct {
	mu   sync.Mutex
	last uint64
	byID map[uint64]any
}

// transport is a client's or a transport's object: the Transport, the
// http.Client NewClient built on it (nil for NewTransport), and what it
// holds open.
type transport struct {
	t      *mimicry.Transport
	client *http.Client
	// verify is its VerifyConnection's state; nil without one.
	verify *verifier
	// keyLog is Config.key_log_file, open; nil without one.
	keyLog io.Closer
}

// parsedProfile is a profile ParseProfile made.
type parsedProfile struct {
	p profile.Profile
}

// add stores v and returns its id.
func (o *objects) add(v any) uint64 {
	return o.addNamed(v, nil)
}

// addNamed stores v and returns its id, first telling named (when not
// nil) the id, under the lock: v knows its id before any call can look it
// up.
func (o *objects) addNamed(v any, named func(id uint64)) uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.byID == nil {
		o.byID = map[uint64]any{}
	}

	o.last++

	if named != nil {
		named(o.last)
	}

	o.byID[o.last] = v

	return o.last
}

// remove takes id's object out, or returns false.
func (o *objects) remove(id uint64) (any, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	v, ok := o.byID[id]
	delete(o.byID, id)

	return v, ok
}

// lookup returns id's object as a T, or an INVALID error naming what was
// wanted.
func lookup[T any](o *objects, id uint64, what string) (T, error) {
	o.mu.Lock()

	v, ok := o.byID[id]
	o.mu.Unlock()

	var zero T

	if !ok {
		return zero, convert.Invalid("no object %d (%s)", id, what)
	}

	t, ok := v.(T)
	if !ok {
		return zero, convert.Invalid("object %d is a %s, not a %s", id, kindOf(v), what)
	}

	return t, nil
}

// kindOf names v's kind of object, for errors.
func kindOf(v any) string {
	switch v.(type) {
	case *transport:
		return "client or transport"
	case *cookies.Jar:
		return "jar"
	case *mimicry.TicketCache:
		return "ticket cache"
	case *mimicry.AltSvcCache:
		return "Alt-Svc cache"
	case *mimicry.MemoryClientHintStore:
		return "client hint store"
	case *parsedProfile:
		return "profile"
	case *webSocket:
		return "WebSocket"
	default:
		return "object"
	}
}

// free lets go of v: a transport's idle connections and key log close, a
// WebSocket closes. Other objects hold nothing open.
func free(v any) {
	switch v := v.(type) {
	case *transport:
		if v.t != nil {
			v.t.CloseIdleConnections()
		}

		if v.keyLog != nil {
			_ = v.keyLog.Close()
		}

	case *webSocket:
		v.close()
	}
}

// freeAll frees every object, WebSockets at once (each may wait for its
// close handshake), and returns when all are.
func (o *objects) freeAll() {
	o.mu.Lock()

	all := o.byID

	o.byID = nil
	o.mu.Unlock()

	var wg sync.WaitGroup
	for _, v := range all {
		wg.Go(func() {
			free(v)
		})
	}

	wg.Wait()
}
