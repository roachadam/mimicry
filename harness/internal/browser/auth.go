package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// debugOrigin is the Origin the WebSocket client sends; browsers refuse
// remote-debugging connections from origins not allowed on their command
// line.
const debugOrigin = "http://127.0.0.1"

// rpc speaks the message shape the DevTools protocol and WebDriver BiDi
// share over a WebSocket: commands {"id", "method", "params"} (plus
// "sessionId" for a DevTools target) answered by id, and events by method.
type rpc struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	next    int
	pending map[int]chan rpcMessage
	onEvent func(rpcMessage)
	done    chan struct{}
}

type rpcMessage struct {
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
	Message string          `json:"message,omitempty"`
}

// dialRPC connects to a debugging WebSocket, retrying until ctx ends while
// the browser starts up. Each event goes to onEvent on its own goroutine.
func dialRPC(ctx context.Context, endpoint string, onEvent func(rpcMessage)) (*rpc, error) {
	cfg, err := websocket.NewConfig(endpoint, debugOrigin)
	if err != nil {
		return nil, err
	}
	var ws *websocket.Conn
	for {
		if ws, err = cfg.DialContext(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connecting to %s: %w", endpoint, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	r := &rpc{
		ws:      ws,
		pending: map[int]chan rpcMessage{},
		onEvent: onEvent,
		done:    make(chan struct{}),
	}
	go r.read()
	return r, nil
}

func (r *rpc) read() {
	defer close(r.done)
	for {
		var m rpcMessage
		if err := websocket.JSON.Receive(r.ws, &m); err != nil {
			return
		}
		if m.ID != 0 {
			r.mu.Lock()
			ch := r.pending[m.ID]
			delete(r.pending, m.ID)
			r.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		go r.onEvent(m)
	}
}

// call sends a command and waits for its result.
func (r *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcMessage, 1)
	r.mu.Lock()
	r.next++
	id := r.next
	r.pending[id] = ch
	r.mu.Unlock()
	err = websocket.JSON.Send(r.ws, rpcMessage{
		ID:     id,
		Method: method,
		Params: raw,
	})
	if err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			return nil, fmt.Errorf("%s: %s %s", method, m.Error, m.Message)
		}
		return m.Result, nil
	case <-r.done:
		return nil, fmt.Errorf("%s: connection closed", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *rpc) Close() error {
	err := r.ws.Close()
	<-r.done
	return err
}

// chromeAuth answers Chrome's proxy authentication challenges with user
// over the DevTools protocol, as a user typing into the login dialog would.
// Interception is enabled on the browser target, which sees every request,
// browser-initiated navigations included (a tab's own session does not);
// each paused request is let go unchanged. dir is the profile directory,
// where Chrome names its debugging port.
func chromeAuth(ctx context.Context, dir string, user *url.Userinfo) (io.Closer, error) {
	// The answers to the browser's events live as long as the session;
	// Close ends them.
	life := context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var port, path string
	for {
		data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); err == nil && len(lines) == 2 {
			port, path = lines[0], lines[1]
			break
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("chrome wrote no DevToolsActivePort")
		case <-time.After(100 * time.Millisecond):
		}
	}
	password, _ := user.Password()
	var r *rpc
	ready := make(chan struct{})
	onEvent := func(m rpcMessage) {
		<-ready
		switch m.Method {
		case "Fetch.requestPaused":
			var p struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				_, _ = r.call(life, "Fetch.continueRequest", map[string]string{
					"requestId": p.RequestID,
				})
			}
		case "Fetch.authRequired":
			var p struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				_, _ = r.call(life, "Fetch.continueWithAuth", map[string]any{
					"requestId": p.RequestID,
					"authChallengeResponse": map[string]string{
						"response": "ProvideCredentials",
						"username": user.Username(),
						"password": password,
					},
				})
			}
		}
	}
	r, err := dialRPC(ctx, "ws://127.0.0.1:"+port+path, onEvent)
	if err != nil {
		return nil, err
	}
	close(ready)
	if _, err := r.call(ctx, "Fetch.enable", map[string]any{
		"handleAuthRequests": true,
		"patterns": []map[string]string{
			{
				"urlPattern": "*",
			},
		},
	}); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

// firefoxAuth answers Firefox's proxy authentication challenges with user
// over WebDriver BiDi, then navigates to pageURL: Firefox, started on
// about:blank, would otherwise meet the challenge before the intercept is
// in place.
func firefoxAuth(ctx context.Context, port int, pageURL string, user *url.Userinfo) (io.Closer, error) {
	life := context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	password, _ := user.Password()
	var r *rpc
	ready := make(chan struct{})
	onEvent := func(m rpcMessage) {
		<-ready
		if m.Method != "network.authRequired" {
			return
		}
		var p struct {
			Request struct {
				Request string `json:"request"`
			} `json:"request"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		_, _ = r.call(life, "network.continueWithAuth", map[string]any{
			"request": p.Request.Request,
			"action":  "provideCredentials",
			"credentials": map[string]string{
				"type":     "password",
				"username": user.Username(),
				"password": password,
			},
		})
	}
	r, err := dialRPC(ctx, "ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/session", onEvent)
	if err != nil {
		return nil, err
	}
	close(ready)
	steps := []struct {
		method string
		params any
	}{
		{"session.new", map[string]any{"capabilities": map[string]any{}}},
		{"session.subscribe", map[string]any{"events": []string{"network.authRequired"}}},
		{"network.addIntercept", map[string]any{"phases": []string{"authRequired"}}},
	}
	for _, s := range steps {
		if _, err := r.call(ctx, s.method, s.params); err != nil {
			return nil, errors.Join(err, r.Close())
		}
	}
	tree, err := r.call(ctx, "browsingContext.getTree", map[string]any{})
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	var t struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(tree, &t); err != nil || len(t.Contexts) == 0 {
		return nil, errors.Join(fmt.Errorf("firefox reported no browsing context: %s", tree), r.Close())
	}
	if _, err := r.call(ctx, "browsingContext.navigate", map[string]string{
		"context": t.Contexts[0].Context,
		"url":     pageURL,
		"wait":    "none",
	}); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

// freePort returns a TCP port free on 127.0.0.1 a moment ago.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // a moment's listen to pick a port
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, ln.Close()
}
