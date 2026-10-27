package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// clickPoll is how often a clicker looks at the page.
const clickPoll = 100 * time.Millisecond

// clickRetry is how long a page may still ask for a click before it is
// clicked again: long enough that a click that took effect has left the page
// (Safari once resent a form POST at 3 s), within the watchdog's 10 s step.
const clickRetry = 6 * time.Second

// clickView returns, as JSON, the page's title and URL and the middle of
// its viewport (CSS pixels).
const clickView = `JSON.stringify([document.title, location.href, innerWidth / 2, innerHeight / 2])`

// pageView is what a clicker sees of a page: its title and URL, and the
// point to click (its middle).
type pageView struct {
	title, url string
	x, y       float64
}

// clicker clicks the middle of each page whose title is Target.ClickTitle,
// once per URL (again after clickRetry), through the browser's own input
// path, so the page gets user activation (a script's click() does not).
type clicker struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   io.Closer
	err    error
}

// startClicker polls look until closed and clicks with click. A look error
// is a page between documents: it is read again. conn, if not nil, is closed
// with the clicker.
func startClicker(ctx context.Context, title string, look func(context.Context) (pageView, error), click func(ctx context.Context, x, y float64) error, conn io.Closer) *clicker {
	// The clicker lives as long as the session; Close ends it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &clicker{
		cancel: cancel,
		done:   make(chan struct{}),
		conn:   conn,
	}
	go func() {
		defer close(c.done)
		var clicked string
		var at time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(clickPoll):
			}
			v, err := look(ctx)
			if err != nil || v.title != title || (v.url == clicked && time.Since(at) < clickRetry) {
				continue
			}
			if err := click(ctx, v.x, v.y); err != nil && ctx.Err() == nil {
				c.err = errors.Join(c.err, fmt.Errorf("clicking %s: %w", v.url, err))
			}
			clicked, at = v.url, time.Now()
		}
	}()

	return c
}

// Close stops the clicker and returns any click that failed.
func (c *clicker) Close() error {
	c.cancel()
	<-c.done
	err := c.err
	if c.conn != nil {
		err = errors.Join(err, c.conn.Close())
	}

	return err
}

// parseView reads clickView's result.
func parseView(s string) (pageView, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return pageView{}, err
	}
	if len(raw) != 4 {
		return pageView{}, fmt.Errorf("page view %s", s)
	}
	var v pageView
	for i, into := range []any{&v.title, &v.url, &v.x, &v.y} {
		if err := json.Unmarshal(raw[i], into); err != nil {
			return pageView{}, err
		}
	}

	return v, nil
}

// chromeClicker clicks in Chrome's tab on host over the DevTools protocol
// (Input.dispatchMouseEvent, which goes through the browser's input
// handling as the mouse does). dir is the profile directory.
func chromeClicker(ctx context.Context, dir, host, title string) (io.Closer, error) {
	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	port, _, err := devToolsPort(setup, dir)
	if err != nil {
		return nil, err
	}
	ws, err := chromeTab(setup, port, host)
	if err != nil {
		return nil, err
	}
	r, err := dialRPC(setup, ws, func(rpcMessage) {})
	if err != nil {
		return nil, err
	}
	look := func(ctx context.Context) (pageView, error) {
		s, err := evaluateCDP(ctx, r, clickView)
		if err != nil {
			return pageView{}, err
		}

		return parseView(s)
	}
	click := func(ctx context.Context, x, y float64) error {
		for _, ev := range []map[string]any{
			{"type": "mouseMoved", "x": x, "y": y},
			{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
			{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
		} {
			if _, err := r.call(ctx, "Input.dispatchMouseEvent", ev); err != nil {
				return err
			}
		}

		return nil
	}

	return startClicker(ctx, title, look, click, r), nil
}

// chromeTab waits for Chrome's page on host and returns its debugging
// WebSocket URL.
func chromeTab(ctx context.Context, port, host string) (string, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json/list", nil)
		if err != nil {
			return "", err
		}
		var targets []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
			WS   string `json:"webSocketDebuggerUrl"`
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			err = json.NewDecoder(resp.Body).Decode(&targets)
			_ = resp.Body.Close()
		}
		for _, t := range targets {
			if t.Type == "page" && strings.Contains(t.URL, "://"+host+":") {
				return t.WS, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("chrome has no page on %s (last: %w)", host, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// firefoxClicker clicks in Firefox's first tab over WebDriver BiDi
// (input.performActions, which Firefox dispatches as the mouse's events).
// port is its remote debugging port.
func firefoxClicker(ctx context.Context, port int, title string) (io.Closer, error) {
	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := dialRPC(setup, "ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/session", func(rpcMessage) {})
	if err != nil {
		return nil, err
	}
	if _, err := r.call(setup, "session.new", map[string]any{
		"capabilities": map[string]any{},
	}); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	bc, err := firefoxContext(setup, r)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	look := func(ctx context.Context) (pageView, error) {
		s, err := evaluateBiDi(ctx, r, bc, clickView)
		if err != nil {
			return pageView{}, err
		}

		return parseView(s)
	}
	click := func(ctx context.Context, x, y float64) error {
		_, err := r.call(ctx, "input.performActions", map[string]any{
			"context": bc,
			"actions": []map[string]any{
				{
					"type": "pointer",
					"id":   "mouse",
					"parameters": map[string]string{
						"pointerType": "mouse",
					},
					"actions": []map[string]any{
						{"type": "pointerMove", "x": int(x), "y": int(y)},
						{"type": "pointerDown", "button": 0},
						{"type": "pointerUp", "button": 0},
					},
				},
			},
		})

		return err
	}

	return startClicker(ctx, title, look, click, r), nil
}

// safariClicker clicks in the user's Safari through the system's mouse
// events (CGEventPost), as the mouse itself: it brings the window of the
// tab with the title to the front and clicks the middle of the window,
// which the page fills. It needs Accessibility access.
func safariClicker(ctx context.Context, title string) io.Closer {
	look := func(ctx context.Context) (pageView, error) {
		out, err := osascript(ctx, `tell application "Safari"
	repeat with w in windows
		repeat with t in tabs of w
			if name of t is "`+title+`" then
				set current tab of w to t
				set index of w to 1
				if not frontmost then
					activate
					return ""
				end if
				set b to bounds of w
				return (URL of t) & linefeed & (((item 1 of b) + (item 3 of b)) div 2) & linefeed & (((item 2 of b) + (item 4 of b)) div 2)
			end if
		end repeat
	end repeat
	return ""
end tell`)
		if err != nil || out == "" {
			return pageView{}, err
		}
		lines := strings.Split(out, "\n")
		if len(lines) != 3 {
			return pageView{}, fmt.Errorf("safari window %q", out)
		}
		x, err := strconv.ParseFloat(lines[1], 64)
		if err != nil {
			return pageView{}, err
		}
		y, err := strconv.ParseFloat(lines[2], 64)
		if err != nil {
			return pageView{}, err
		}

		return pageView{
			title: title,
			url:   lines[0],
			x:     x,
			y:     y,
		}, nil
	}
	click := func(ctx context.Context, x, y float64) error {
		// Mouse moved, left button down, left button up (CGEventType 5,
		// 1, 2), each with click state 1 (field 1), posted at the HID
		// level (tap 0), after the window has come to the front.
		_, err := osascriptJS(ctx, fmt.Sprintf(`ObjC.import("CoreGraphics");
delay(0.3);
const p = $.CGPointMake(%g, %g);
for (const type of [5, 1, 2]) {
	const e = $.CGEventCreateMouseEvent(null, type, p, 0);
	$.CGEventSetIntegerValueField(e, 1, 1);
	$.CGEventPost(0, e);
	delay(0.05);
}`, x, y))

		return err
	}

	return startClicker(ctx, title, look, click, nil)
}

// safariCanClick reports whether this process may post the system's mouse
// events (Accessibility access), which safariClicker needs.
func safariCanClick(ctx context.Context) error {
	out, err := osascriptJS(ctx, `ObjC.import("ApplicationServices"); $.AXIsProcessTrusted()`)
	if err != nil {
		return err
	}
	if out != "true" {
		return errors.New("clicking in Safari posts the system's mouse events, which needs Accessibility access for the app running the capture tool " +
			"(System Settings > Privacy & Security > Accessibility; over ssh the session has none)")
	}

	return nil
}
