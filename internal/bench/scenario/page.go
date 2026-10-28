package scenario

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/roachadam/mimicry/internal/bench/clients"
	"github.com/roachadam/mimicry/internal/bench/site"
	"github.com/roachadam/mimicry/internal/bench/target"
)

// PageLoad is one load of the page: the HTML from www, then its 30
// subresources from www, static and api all at once (each client's own
// connection pooling decides how they go out).
type PageLoad struct {
	// Ms is the time from the first request to the last body read.
	Ms float64 `json:"ms"`
	// HTMLMs is the time to the HTML's last byte.
	HTMLMs float64 `json:"htmlMs"`
	// Conns is how many connections the server accepted meanwhile (TCP
	// and QUIC), -1 when its stats are unavailable.
	Conns     int64 `json:"conns"`
	TCPConns  int64 `json:"tcpConns"`
	QUICConns int64 `json:"quicConns"`
	// Protos counts responses per protocol.
	Protos map[target.Proto]int `json:"protos"`
	Errors int                  `json:"errors"`
	Error  string               `json:"error,omitempty"`
}

// PageResult is a page-load setup's repetitions: each on a new client
// (Cold), then once more on the same client (Warm: its connections, and
// what it learnt, such as Alt-Svc, kept).
type PageResult struct {
	Cold []PageLoad `json:"cold"`
	Warm []PageLoad `json:"warm"`
}

// Pages loads the page reps times with su's client, each on a new client.
// A returning setup's clients start from what an earlier visit left them:
// one page load on a client of its own, not measured.
func Pages(ctx context.Context, su Setup, reps int) (PageResult, error) {
	var res PageResult

	if su.Returning {
		c, err := su.New()
		if err != nil {
			return res, err
		}

		if pl := loadPage(ctx, su, c); pl.Errors > 0 {
			c.Close()

			return res, fmt.Errorf("the earlier visit: %d errors: %s", pl.Errors, pl.Error)
		}

		c.Close()
	}

	for range reps {
		c, err := su.New()
		if err != nil {
			return res, err
		}

		res.Cold = append(res.Cold, loadPage(ctx, su, c))
		res.Warm = append(res.Warm, loadPage(ctx, su, c))

		c.Close()
	}

	return res, nil
}

func loadPage(ctx context.Context, su Setup, c clients.Client) PageLoad {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pl := PageLoad{
		Conns:  -1,
		Protos: map[target.Proto]int{},
	}

	before, statsErr := su.Server.Stats(ctx)

	var mu sync.Mutex

	page := su.Server.URL(su.Proto, "www", "/")
	result := func(resp clients.Response, err error) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case err != nil:
			pl.Errors++
			if pl.Error == "" {
				pl.Error = err.Error()
			}

		case resp.Status != 200:
			pl.Errors++
			if pl.Error == "" {
				pl.Error = fmt.Sprintf("status %d", resp.Status)
			}

		default:
			pl.Protos[resp.Proto]++
		}
	}

	start := time.Now()

	result(c.Do(ctx, &clients.Request{
		Method: "GET",
		URL:    su.Server.URL(su.Proto, site.HTML.Host, site.HTML.Path),
		Kind:   site.HTML.Kind,
	}))

	pl.HTMLMs = ms(time.Since(start))

	var wg sync.WaitGroup

	for _, r := range site.Subresources() {
		wg.Go(func() {
			result(c.Do(ctx, &clients.Request{
				Method: "GET",
				URL:    su.Server.URL(su.Proto, r.Host, r.Path),
				Kind:   r.Kind,
				Page:   page,
			}))
		})
	}

	wg.Wait()

	pl.Ms = ms(time.Since(start))

	// Connections a client opens beside the page's requests (a QUIC
	// connection after Alt-Svc) may land just after the last response.
	time.Sleep(250 * time.Millisecond)

	after, err := su.Server.Stats(ctx)
	if statsErr == nil && err == nil {
		pl.TCPConns = after.TCPConns - before.TCPConns
		pl.QUICConns = after.QUICConns - before.QUICConns
		pl.Conns = pl.TCPConns + pl.QUICConns
	}

	return pl
}
