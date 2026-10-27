package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Live is a browser on the public internet, driven one top-level navigation
// at a time, for cmd/realcheck to compare against mimicry on public
// fingerprinting services. Chrome and Firefox run on a throwaway profile with
// their defaults; Safari runs in a new window of the user's own Safari and
// cannot report a page's status or cookies.
type Live struct {
	name    string
	session *Session
	rpc     *rpc
	// context is Firefox's browsing context; window Safari's window id.
	context string
	window  string
}

// Page is what a visit showed.
type Page struct {
	// URL is where the page ended up, Title its title.
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	// Text is the body's text; for a JSON response the JSON as served.
	Text string `json:"text"`
	// Status and Protocol are the document's response status and ALPN
	// protocol from the Navigation Timing entry (0 and "" for Safari).
	Status   int    `json:"status,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	// Cookies are the names of the cookies the browser holds for the URL
	// (none listed for Safari).
	Cookies []string `json:"cookies,omitempty"`
}

// liveTimeout bounds one visit.
const liveTimeout = 30 * time.Second

// pageScript returns the page as JSON. Firefox shows JSON in its viewer,
// whose JSONView.json holds the text as served; other browsers show the
// text itself.
const pageScript = `JSON.stringify((() => {
  const n = performance.getEntriesByType("navigation")[0] || {};
  const raw = (typeof JSONView !== "undefined" && JSONView.json) ? JSONView.json.data : null;
  return {
    url: location.href,
    title: document.title,
    text: raw !== null ? raw : (document.body ? document.body.innerText : ""),
    status: n.responseStatus || 0,
    protocol: n.nextHopProtocol || "",
    ready: document.readyState,
    origin: performance.timeOrigin,
  };
})())`

// OpenLive starts b for Visit.
func (b *Browser) OpenLive(ctx context.Context) (*Live, error) {
	l := &Live{
		name: b.Name,
	}
	if b.Shared {
		// The new window need not be the front one (Safari in the
		// background): it is the one whose id was not there before.
		out, err := osascript(ctx, `tell application "Safari"
	set before to id of every window
	make new document with properties {URL:"about:blank"}
	repeat with w in (every window)
		if before does not contain (id of w) then return id of w
	end repeat
	error "no new Safari window"
end tell`)
		if err != nil {
			return nil, err
		}
		l.window = out

		return l, nil
	}
	dir, err := os.MkdirTemp("", "mimicry-live-"+b.Name+"-")
	if err != nil {
		return nil, err
	}
	var args []string
	port := 0
	switch b.Name {
	case "chrome":
		args = []string{
			"--user-data-dir=" + dir,
			"--no-first-run",
			"--no-default-browser-check",
			"--remote-debugging-port=0",
			"--remote-allow-origins=" + debugOrigin,
			"about:blank",
		}
	case "firefox":
		if port, err = freePort(); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		prefs := []string{
			`user_pref("browser.shell.checkDefaultBrowser", false);`,
			`user_pref("browser.startup.homepage_override.mstone", "ignore");`,
			`user_pref("browser.aboutwelcome.enabled", false);`,
			`user_pref("datareporting.policy.dataSubmissionEnabled", false);`,
			`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
			// The remote agent otherwise applies automation preferences,
			// some of which change networking.
			`user_pref("remote.prefs.recommended", false);`,
		}
		if err := os.WriteFile(filepath.Join(dir, "user.js"), []byte(strings.Join(prefs, "\n")+"\n"), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		args = []string{
			"--profile", dir,
			"--new-instance",
			"--remote-debugging-port", strconv.Itoa(port),
			"--remote-allow-origins", debugOrigin,
			"about:blank",
		}
	default:
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%s cannot be driven", b.Name)
	}
	s, err := b.start(ctx, dir, args)
	if err != nil {
		return nil, err
	}
	l.session = s
	if err := l.connect(ctx, b, dir, port); err != nil {
		return nil, errors.Join(err, l.Close())
	}

	return l, nil
}

// connect opens the debugging connection: Chrome's to its tab, after the
// warm-up a fresh profile needs; Firefox's BiDi session.
func (l *Live) connect(ctx context.Context, b *Browser, dir string, port int) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second+b.warmup)
	defer cancel()
	ignore := func(rpcMessage) {}
	if l.name == "firefox" {
		r, err := dialRPC(ctx, "ws://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+"/session", ignore)
		if err != nil {
			return err
		}
		l.rpc = r
		if _, err := r.call(ctx, "session.new", map[string]any{
			"capabilities": map[string]any{},
		}); err != nil {
			return err
		}
		l.context, err = firefoxContext(ctx, r)

		return err
	}
	debugPort, _, err := devToolsPort(ctx, dir)
	if err != nil {
		return err
	}
	select {
	case <-time.After(b.warmup):
	case <-ctx.Done():
		return ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+debugPort+"/json/list", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var targets []struct {
		Type string `json:"type"`
		WS   string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return err
	}
	for _, t := range targets {
		if t.Type == "page" {
			l.rpc, err = dialRPC(ctx, t.WS, ignore)
			return err
		}
	}

	return errors.New("chrome has no page to drive")
}

// Visit navigates to url as a typed URL and returns the page once its
// document is interactive (Safari: once its text stops changing).
func (l *Live) Visit(ctx context.Context, url string) (*Page, error) {
	ctx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()
	switch l.name {
	case "safari":
		return l.visitSafari(ctx, url)
	case "firefox":
		if _, err := l.rpc.call(ctx, "browsingContext.navigate", map[string]string{
			"context": l.context,
			"url":     url,
			"wait":    "interactive",
		}); err != nil {
			return nil, err
		}

		return l.page(ctx, 0)
	default:
		start := float64(time.Now().UnixMilli())
		res, err := l.rpc.call(ctx, "Page.navigate", map[string]string{
			"url": url,
		})
		if err != nil {
			return nil, err
		}
		var nav struct {
			ErrorText string `json:"errorText"`
		}
		if json.Unmarshal(res, &nav) == nil && nav.ErrorText != "" {
			return nil, fmt.Errorf("navigating to %s: %s", url, nav.ErrorText)
		}

		return l.page(ctx, start)
	}
}

// Read returns the page as it is now, without navigating.
func (l *Live) Read(ctx context.Context) (*Page, error) {
	ctx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()
	if l.name == "safari" {
		return readSafari(ctx, "current tab of window id "+l.window)
	}

	return l.page(ctx, 0)
}

// page reads the page through the debugging protocol, waiting for a
// document that started after since (ms since the epoch; 0: any) to be
// interactive.
func (l *Live) page(ctx context.Context, since float64) (*Page, error) {
	for {
		var p struct {
			Page
			Ready  string  `json:"ready"`
			Origin float64 `json:"origin"`
		}
		s, err := l.evaluate(ctx)
		if err == nil {
			err = json.Unmarshal([]byte(s), &p)
		}
		if err == nil && p.Ready != "loading" && p.Origin >= since-1000 {
			page := p.Page
			page.Cookies, err = l.cookies(ctx, page.URL)

			return &page, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the page: %w (last: %w)", ctx.Err(), err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// evaluate runs pageScript in the page.
func (l *Live) evaluate(ctx context.Context) (string, error) {
	if l.name == "firefox" {
		return evaluateBiDi(ctx, l.rpc, l.context, pageScript)
	}

	return evaluateCDP(ctx, l.rpc, pageScript)
}

// evaluateBiDi runs expression, which returns a string, in browsing
// context bc of a WebDriver BiDi session.
func evaluateBiDi(ctx context.Context, r *rpc, bc, expression string) (string, error) {
	res, err := r.call(ctx, "script.evaluate", map[string]any{
		"expression":   expression,
		"target":       map[string]string{"context": bc},
		"awaitPromise": false,
	})
	if err != nil {
		return "", err
	}
	var v struct {
		Type   string `json:"type"`
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &v); err != nil {
		return "", err
	}
	if v.Type != "success" {
		return "", fmt.Errorf("script failed: %s", v.ExceptionDetails)
	}

	return v.Result.Value, nil
}

// evaluateCDP runs expression, which returns a string, in the page of a
// DevTools connection.
func evaluateCDP(ctx context.Context, r *rpc, expression string) (string, error) {
	res, err := r.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var v struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &v); err != nil {
		return "", err
	}
	if len(v.ExceptionDetails) > 0 {
		return "", fmt.Errorf("script failed: %s", v.ExceptionDetails)
	}

	return v.Result.Value, nil
}

// cookies lists the names of the cookies the browser holds for url.
func (l *Live) cookies(ctx context.Context, url string) ([]string, error) {
	var res json.RawMessage
	var err error
	if l.name == "firefox" {
		res, err = l.rpc.call(ctx, "storage.getCookies", map[string]any{
			"partition": map[string]string{
				"type":    "context",
				"context": l.context,
			},
		})
	} else {
		res, err = l.rpc.call(ctx, "Network.getCookies", map[string]any{
			"urls": []string{url},
		})
	}
	if err != nil {
		return nil, err
	}
	var r struct {
		Cookies []struct {
			Name string `json:"name"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	names := make([]string, len(r.Cookies))
	for i, c := range r.Cookies {
		names[i] = c.Name
	}

	return names, nil
}

// visitSafari goes through about:blank, so a page's text is never the
// previous one's, then to url, and returns its text once two reads half a
// second apart agree.
func (l *Live) visitSafari(ctx context.Context, url string) (*Page, error) {
	tab := "current tab of window id " + l.window
	if _, err := osascript(ctx, `tell application "Safari" to set URL of `+tab+` to "about:blank"`); err != nil {
		return nil, err
	}
	for {
		p, err := safariTab(ctx, tab)
		if err != nil {
			return nil, err
		}
		if p.URL == "about:blank" && strings.TrimSpace(p.Text) == "" {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for about:blank: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	if _, err := osascript(ctx, `tell application "Safari" to set URL of `+tab+` to "`+url+`"`); err != nil {
		return nil, err
	}

	return readSafari(ctx, tab)
}

// readSafari returns tab's page once two reads half a second apart agree
// on a page that is not blank: one with text, or with a title only (a
// challenge drawn in a frame has no text of its own).
func readSafari(ctx context.Context, tab string) (*Page, error) {
	var last *Page
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the page: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		p, err := safariTab(ctx, tab)
		if err != nil {
			return nil, err
		}
		if p.URL == "about:blank" || p.URL == "" || strings.TrimSpace(p.Text+p.Title) == "" {
			continue
		}
		if last != nil && last.URL == p.URL && last.Title == p.Title && last.Text == p.Text {
			return p, nil
		}
		last = p
	}
}

// safariTab reads a Safari tab's URL, title and text.
func safariTab(ctx context.Context, tab string) (*Page, error) {
	out, err := osascript(ctx, `tell application "Safari"
	set t to `+tab+`
	return (URL of t) & linefeed & (name of t) & linefeed & (text of t)
end tell`)
	if err != nil {
		return nil, err
	}
	at, rest, _ := strings.Cut(out, "\n")
	title, text, _ := strings.Cut(rest, "\n")

	return &Page{
		URL:   at,
		Title: title,
		Text:  text,
	}, nil
}

// Close stops the browser and removes its profile; for Safari, closes its
// window.
func (l *Live) Close() error {
	var errs []error
	if l.rpc != nil {
		errs = append(errs, l.rpc.Close())
	}
	if l.session != nil {
		errs = append(errs, l.session.Close())
	}
	if l.window != "" {
		_, err := osascript(context.Background(), `tell application "Safari" to close window id `+l.window)
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// osascript runs an AppleScript and returns its output, trimmed.
func osascript(ctx context.Context, script string) (string, error) {
	return runOSA(ctx, "AppleScript", script)
}

// osascriptJS runs a JavaScript for Automation script (which reaches the
// system's C APIs) and returns its output, trimmed.
func osascriptJS(ctx context.Context, script string) (string, error) {
	return runOSA(ctx, "JavaScript", script)
}

// runOSA runs script in OSA language lang.
func runOSA(ctx context.Context, lang, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-l", lang, "-e", script).Output() //nolint:gosec // our own scripts
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}

		return "", err
	}

	return strings.TrimRight(string(out), "\n"), nil
}
