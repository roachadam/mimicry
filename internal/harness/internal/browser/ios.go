package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// IOSSafariName and IOSChromeName are how cmd/capture's -browsers names
// Safari and Chrome on an iPhone.
const (
	IOSSafariName = "ios-safari"
	IOSChromeName = "ios-chrome"
)

// IOSNames are the browsers on an iPhone cmd/capture's -browsers names.
func IOSNames() []string {
	return []string{
		IOSSafariName,
		IOSChromeName,
	}
}

// IOSProxyPort is the port of the capture tool's HTTP proxy the iPhone's
// Wi-Fi settings name for the proxy scenarios, on the Mac's address there:
// fixed, since a person types it.
const IOSProxyPort = 15354

// IOSDriver is how the capture tool sends a browser on the iPhone to a
// capture URL.
type IOSDriver string

const (
	// IOSDevicectl opens it in the phone's own browser through Xcode's
	// devicectl on this Mac (process launch with a payload URL, over USB),
	// which brings the browser to the front: nobody at the phone, and the
	// phone's own browser, as IOSManual's. Its tabs stay open.
	IOSDevicectl IOSDriver = "devicectl"
	// IOSManual has the maintainer open the URL in the phone's own
	// browser, as the macOS capture opens it in the user's own window, and
	// tap where a scenario clicks.
	IOSManual IOSDriver = "manual"
	// IOSSafaridriver opens it through safaridriver(1) on this Mac, which
	// drives Safari (only) on a paired iPhone over USB: a WebDriver
	// session per sample, in an automation window of its own with fresh
	// storage, whose header order is not the phone's own Safari's
	// (decision 0051, server.Scenario.AutomationDiffersOnIOS).
	IOSSafaridriver IOSDriver = "safaridriver"
)

// iosApp is a browser app on the iPhone.
type iosApp struct {
	// title names it to the maintainer; agent is the browser
	// capture.ParseUserAgent finds in its user agent.
	title, agent string
	// bundle is its bundle identifier, which devicectl launches.
	bundle string
	// system marks the browser that comes with iOS and gives the system's
	// version as its own (Safari); another's is the installed app's.
	system bool
	// safaridriver marks the browser safaridriver drives.
	safaridriver bool
	// payload is the URL devicectl hands it for a capture URL; nil for
	// the URL itself.
	payload func(url string) string
	// restarts are the scenarios before each of whose samples devicectl
	// ends the running app and launches it afresh.
	restarts []string
	// prompts are what someone at the phone answers in a scenario, by its
	// name: the app asks on screen and the page waits for the answer.
	prompts map[string]string
}

// iosAppNamed is the app of the iPhone browser name (IOSNames), nil for
// another name.
func iosAppNamed(name string) *iosApp {
	switch name {
	case IOSSafariName:
		return &iosApp{
			title:        "Safari",
			agent:        "safari",
			bundle:       "com.apple.mobilesafari",
			system:       true,
			safaridriver: true,
		}

	case IOSChromeName:
		return &iosApp{
			title:    "Chrome",
			agent:    "chrome",
			bundle:   "com.google.chrome.ios",
			payload:  chromePayloadURL,
			restarts: iosChromeRestarts,
			prompts: map[string]string{
				// Chrome asks before submitting a form over plain HTTP;
				// Safari does not.
				"kinds-plain": `tap "Send anyway" on each of Chrome's insecure-form warnings (Chrome asks before submitting a form over plain HTTP)`,
			},
		}
	}

	return nil
}

// chromePayloadURL is url in Chrome's own schemes, googlechromes:// for
// https:// and googlechrome:// for http://: Chrome ignores a devicectl
// payload URL with a plain https:// scheme.
func chromePayloadURL(url string) string {
	if rest, ok := strings.CutPrefix(url, "https://"); ok {
		return "googlechromes://" + rest
	}

	if rest, ok := strings.CutPrefix(url, "http://"); ok {
		return "googlechrome://" + rest
	}

	return url
}

// iosChromeRestarts are the scenarios for which devicectl ends Chrome on
// the iPhone and launches it afresh (--terminate-existing) before each
// sample. After a redirect chain's "too many redirects" page Chrome does
// not open the next sample's URL, so the chrome-157-ios captures of these
// two were taken with Chrome restarted per sample; the other scenarios
// were captured with Chrome left running, and a restart there would be
// unmeasured.
var iosChromeRestarts = []string{
	"redirects2",
	"redirects2-cookies",
}

// IOS is the browser name (IOSNames) on an iPhone on this Mac's network,
// sent to each capture URL by driver; IOSSafaridriver drives Safari only.
// device, when set, is the UDID or CoreDevice identifier of the iPhone
// devicectl or safaridriver must use.
func IOS(name string, driver IOSDriver, device string) (*Browser, error) {
	app := iosAppNamed(name)
	if app == nil {
		return nil, fmt.Errorf("%q is no browser on an iPhone (want %s)", name, strings.Join(IOSNames(), " or "))
	}

	switch driver {
	case IOSDevicectl, IOSManual, IOSSafaridriver:
	default:
		return nil, fmt.Errorf("unknown iOS driver %q (want %s, %s or %s)", driver, IOSDevicectl, IOSManual, IOSSafaridriver)
	}

	if driver == IOSSafaridriver && !app.safaridriver {
		return nil, fmt.Errorf("%s: safaridriver drives Safari only; open %s on the iPhone with -ios-driver %s or %s", name, app.title, IOSDevicectl, IOSManual)
	}

	if device != "" && driver == IOSManual {
		return nil, fmt.Errorf("a device is for the %s and %s drivers", IOSDevicectl, IOSSafaridriver)
	}

	return &Browser{
		Name: name,
		ios: &iosBrowser{
			app:    app,
			driver: driver,
			device: device,
		},
	}, nil
}

// IOSApp names the app of the browser name (IOSNames) on the iPhone to the
// maintainer ("Safari", "Chrome"); "" for another name.
func IOSApp(name string) string {
	if app := iosAppNamed(name); app != nil {
		return app.title
	}

	return ""
}

// IOSPrompt is what someone at the iPhone must answer while the browser
// name (IOSNames) loads scenario's page, which waits for it; "" when
// nothing is asked.
func IOSPrompt(name, scenario string) string {
	if app := iosAppNamed(name); app != nil {
		return app.prompts[scenario]
	}

	return ""
}

// OnIPhone reports whether b is a browser on an iPhone (IOS).
func (b *Browser) OnIPhone() bool {
	return b.ios != nil
}

// OpenedByHand reports whether someone opens b's capture URLs: a browser
// on an iPhone with IOSManual.
func (b *Browser) OpenedByHand() bool {
	return b.ios != nil && b.ios.driver == IOSManual
}

// IOSDriver is how b, a browser on an iPhone, is sent to its URLs; "" for
// another browser.
func (b *Browser) IOSDriver() IOSDriver {
	if b.ios == nil {
		return ""
	}

	return b.ios.driver
}

// Automated reports whether b is Safari on an iPhone driven through
// safaridriver's automation window, not the phone's own Safari.
func (b *Browser) Automated() bool {
	return b.IOSDriver() == IOSSafaridriver
}

// IOSNoClicks says why b, a browser on an iPhone, cannot take the click
// scenarios; "" when it can (IOSManual: a person taps).
func (b *Browser) IOSNoClicks() string {
	switch b.IOSDriver() {
	case IOSSafaridriver:
		return iosSafaridriverNoClicks
	case IOSDevicectl:
		return "devicectl only opens the URL: nothing taps the page; tap by hand with -ios-driver manual"
	case IOSManual:
	}

	return ""
}

type iosBrowser struct {
	app    *iosApp
	driver IOSDriver
	device string
	// launchID is the CoreDevice identifier devicectl launches the app on,
	// found on the first sample.
	launchID string
}

// iosSafaridriverNoClicks is why the click scenarios are not taken through
// safaridriver: on the iPhone, tried 2026-11-01, WebDriver's Element Click
// gave the page pointerdown and touchstart but no click, and the link's
// target was fetched with the page staying put (a long press's link preview
// does that [INFERENCE]); touch actions (Perform Actions) gave the page no
// event at all. A person taps them with IOSManual.
const iosSafaridriverNoClicks = "safaridriver's Element Click on the iPhone does not click (the page sees pointerdown and touchstart, no click; the link's target is fetched but not navigated to), and its touch actions reach no page; tap by hand with -ios-driver manual"

// IOSNoTaps is the iPhone's click refusal in plans, which take the click
// scenarios through devicectl or safaridriver, neither of which taps.
const IOSNoTaps = "neither devicectl nor safaridriver taps a page on the iPhone (safaridriver's Element Click gives pointerdown and touchstart, no click); tap by hand with -ios-driver manual"

// devicectlLaunch bounds devicectl's launch of the browser on the phone.
const devicectlLaunch = time.Minute

// open sends the phone to url. With IOSManual nothing happens here: the
// capture tool prints url for the maintainer, who also taps. A proxy
// scenario's proxy is the one the phone's Wi-Fi settings name, which the
// capture tool has the maintainer set.
func (s *iosBrowser) open(ctx context.Context, url string, t Target) (*Session, error) {
	if t.Proxy != nil && t.Proxy.Scheme != "http" {
		return nil, fmt.Errorf("the iPhone's Wi-Fi proxy setting takes an HTTP proxy only, not %s", t.Proxy.Scheme)
	}

	sess := &Session{
		device: true,
	}

	switch s.driver {
	case IOSManual:
		return sess, nil
	case IOSDevicectl:
		if t.ClickTitle != "" {
			return nil, errors.New(IOSNoTaps)
		}

		return sess, s.launch(ctx, url, slices.Contains(s.app.restarts, t.Scenario))

	case IOSSafaridriver:
	}

	if t.ClickTitle != "" {
		return nil, errors.New(iosSafaridriverNoClicks)
	}

	wd, err := startWebDriver(ctx, s.device)
	if err != nil {
		return nil, err
	}

	wd.navigate(url)

	sess.remote = wd

	return sess, nil
}

// launch opens url in the phone's own browser through devicectl, which
// launches the app (or brings it to the front) with url, in the app's
// payload form, as its payload; restart ends the running app first.
func (s *iosBrowser) launch(ctx context.Context, url string, restart bool) error {
	if s.launchID == "" {
		phone, err := pairedIPhone(ctx, s.device)
		if err != nil {
			return err
		}

		s.launchID = phone.id
	}

	if s.app.payload != nil {
		url = s.app.payload(url)
	}

	args := []string{"devicectl", "device", "process", "launch", "--device", s.launchID, "--payload-url", url, "--quiet"}
	if restart {
		args = append(args, "--terminate-existing")
	}

	ctx, cancel := context.WithTimeout(ctx, devicectlLaunch)
	defer cancel()

	out, err := exec.CommandContext(ctx, "xcrun", append(args, s.app.bundle)...).CombinedOutput() //nolint:gosec // a system tool; the device and URL are ours
	if err != nil {
		return fmt.Errorf("devicectl could not open %s on the iPhone (connected by cable, unlocked?): %w: %s", s.app.title, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// IPhoneVersion is the version of the browser name (IOSNames) on the
// paired iPhone connected to this Mac (device: its UDID or identifier; "":
// the only one). Safari's is the iPhone's iOS version: Safari on iOS comes
// with the system and gives the same version in its user agent. Chrome's
// is its app's, as devicectl lists it; an error if it is not installed.
// It needs Xcode's devicectl.
func IPhoneVersion(ctx context.Context, name, device string) (string, error) {
	app := iosAppNamed(name)
	if app == nil {
		return "", fmt.Errorf("%q is no browser on an iPhone", name)
	}

	phone, err := pairedIPhone(ctx, device)
	if err != nil {
		return "", err
	}

	if app.system {
		return phone.version, nil
	}

	return appVersion(ctx, phone.id, app)
}

// appVersion is the version (CFBundleShortVersionString) of app installed
// on the phone with CoreDevice identifier id.
func appVersion(ctx context.Context, id string, app *iosApp) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "devicectl", "device", "info", "apps", "--device", id, //nolint:gosec // a system tool; the device and bundle are ours
		"--include-default-apps", "--bundle-id", app.bundle, "--quiet", "--json-output", "-").Output()
	if err != nil {
		return "", fmt.Errorf("xcrun devicectl device info apps: %w", err)
	}

	var list struct {
		Result struct {
			Apps []struct {
				BundleIdentifier string `json:"bundleIdentifier"`
				Version          string `json:"version"`
			} `json:"apps"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", fmt.Errorf("xcrun devicectl device info apps: %w", err)
	}

	for _, a := range list.Result.Apps {
		if a.BundleIdentifier == app.bundle && a.Version != "" {
			return a.Version, nil
		}
	}

	return "", fmt.Errorf("%s (%s) is not installed on the iPhone", app.title, app.bundle)
}

// iPhone is a paired iPhone devicectl lists as connected.
type iPhone struct {
	// id is its CoreDevice identifier, version its iOS version.
	id, version string
}

// pairedIPhone is the paired physical iPhone connected to this Mac
// (device: its UDID or identifier; "": the only one connected).
func pairedIPhone(ctx context.Context, device string) (iPhone, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "devicectl", "list", "devices", "--quiet", "--json-output", "-").Output()
	if err != nil {
		return iPhone{}, fmt.Errorf("xcrun devicectl list devices: %w", err)
	}

	var list struct {
		Result struct {
			Devices []struct {
				Identifier string `json:"identifier"`
				Hardware   struct {
					Reality    string `json:"reality"`
					DeviceType string `json:"deviceType"`
					UDID       string `json:"udid"`
				} `json:"hardwareProperties"`
				Device struct {
					OSVersion string `json:"osVersionNumber"`
				} `json:"deviceProperties"`
				Connection struct {
					PairingState  string `json:"pairingState"`
					TransportType string `json:"transportType"`
					TunnelState   string `json:"tunnelState"`
				} `json:"connectionProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return iPhone{}, fmt.Errorf("xcrun devicectl list devices: %w", err)
	}

	var found []iPhone

	for _, d := range list.Result.Devices {
		switch {
		case d.Hardware.Reality != "physical", d.Hardware.DeviceType != "iPhone":
		case d.Connection.PairingState != "paired", d.Connection.TransportType == "", d.Connection.TunnelState == "unavailable":
		case device != "" && device != d.Hardware.UDID && device != d.Identifier:
		case d.Device.OSVersion == "":
		default:
			found = append(found, iPhone{
				id:      d.Identifier,
				version: d.Device.OSVersion,
			})
		}
	}

	switch len(found) {
	case 0:
		return iPhone{}, errors.New("no paired iPhone connected to this Mac (xcrun devicectl list devices)")
	case 1:
		return found[0], nil
	}

	return iPhone{}, errors.New("several paired iPhones are connected to this Mac: pick one with -ios-device (xcrun devicectl list devices)")
}

// webDriver is a safaridriver process and its session on the iPhone.
type webDriver struct {
	cmd     *exec.Cmd
	base    string
	session string
	// stopNav ends the Navigate To request, navDone holds its result.
	stopNav context.CancelFunc
	navDone chan error
}

// webDriverError is an error a WebDriver command answered with.
type webDriverError struct {
	status  string
	code    string
	message string
}

func (e *webDriverError) Error() string {
	return e.status + ": " + e.code + ": " + e.message
}

// safaridriverReady bounds safaridriver's start and the session's creation,
// which launches Safari on the phone.
const safaridriverReady = time.Minute

// sessionRetry bounds the retries of a session safaridriver could not
// create: it now and then reports the paired, unlocked iPhone as unpaired
// or without Web Inspector, and creates the session seconds later.
const sessionRetry = 20 * time.Second

// startWebDriver starts safaridriver on a free port and opens a session in
// Safari on a paired iPhone (device: its UDID, or any).
func startWebDriver(ctx context.Context, device string) (*webDriver, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "safaridriver", "-p", strconv.Itoa(port)) //nolint:gosec // a system tool; the port is ours
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting safaridriver: %w", err)
	}

	wd := &webDriver{
		cmd:  cmd,
		base: "http://127.0.0.1:" + strconv.Itoa(port),
	}

	setup, cancel := context.WithTimeout(ctx, safaridriverReady)
	defer cancel()

	for {
		if _, err := wd.call(setup, http.MethodGet, "/status", nil); err == nil {
			break
		}

		select {
		case <-setup.Done():
			wd.stop()

			return nil, errors.New("safaridriver did not answer")

		case <-time.After(100 * time.Millisecond):
		}
	}

	always := map[string]any{
		"browserName":         "Safari",
		"platformName":        "iOS",
		"safari:useSimulator": false,
		"safari:deviceType":   "iPhone",
	}
	if device != "" {
		always["safari:deviceUDID"] = device
	}

	value, err := wd.newSession(setup, map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": always,
		},
	})
	if err != nil {
		wd.stop()

		return nil, fmt.Errorf("safaridriver could not open a session on the iPhone (connected, unlocked, Settings > Apps > Safari > Advanced > Web Inspector and Remote Automation on?): %w", err)
	}

	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(value, &created); err != nil || created.SessionID == "" {
		wd.stop()

		return nil, fmt.Errorf("safaridriver's new session: %s", value)
	}

	wd.session = created.SessionID

	return wd, nil
}

// newSession creates the session, retrying for up to sessionRetry, with
// growing pauses, while safaridriver says the session was not created.
func (wd *webDriver) newSession(ctx context.Context, body any) (json.RawMessage, error) {
	deadline := time.Now().Add(sessionRetry)
	pause := 500 * time.Millisecond

	for attempt := 1; ; attempt++ {
		value, err := wd.call(ctx, http.MethodPost, "/session", body)

		var we *webDriverError
		if err == nil || !errors.As(err, &we) || !sessionNotCreated(we) || time.Now().Add(pause).After(deadline) {
			return value, err
		}

		log.Printf("%s: safaridriver did not create a session (attempt %d), trying again in %s: %v", IOSSafariName, attempt, pause, err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pause):
		}

		pause *= 2
	}
}

// sessionNotCreated reports whether e is safaridriver failing to create a
// session on a device it found.
func sessionNotCreated(e *webDriverError) bool {
	return e.code == "session not created" || strings.Contains(e.message, "could not be used")
}

// navigate sends the session to url. Navigate To answers once the page has
// loaded, so it runs until then or Close.
func (wd *webDriver) navigate(url string) {
	ctx, cancel := context.WithCancel(context.Background())

	wd.stopNav = cancel
	wd.navDone = make(chan error, 1)

	go func() {
		_, err := wd.call(ctx, http.MethodPost, "/session/"+wd.session+"/url", map[string]any{
			"url": url,
		})
		wd.navDone <- err
	}()
}

// Close ends the session, which closes its window, and stops safaridriver.
// A failed navigation is reported.
func (wd *webDriver) Close() error {
	wd.stopNav()

	navErr := <-wd.navDone
	if errors.Is(navErr, context.Canceled) {
		navErr = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := wd.call(ctx, http.MethodDelete, "/session/"+wd.session, nil)
	wd.stop()

	if navErr != nil {
		navErr = fmt.Errorf("navigating: %w", navErr)
	}

	if err != nil {
		err = fmt.Errorf("ending the WebDriver session: %w", err)
	}

	return errors.Join(navErr, err)
}

// stop stops safaridriver, killing it after five seconds.
func (wd *webDriver) stop() {
	done := make(chan struct{})

	go func() {
		_ = wd.cmd.Wait()

		close(done)
	}()

	_ = wd.cmd.Process.Signal(os.Interrupt)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = wd.cmd.Process.Kill()

		<-done
	}
}

// call sends a WebDriver command and returns its value, or the
// webDriverError it answered with.
func (wd *webDriver) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}

		r = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, wd.base+path, r)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	var out struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s %s: %s: %w", method, path, resp.Status, err)
	}

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}

		_ = json.Unmarshal(out.Value, &e)

		return nil, fmt.Errorf("%s %s: %w", method, path, &webDriverError{
			status:  resp.Status,
			code:    e.Error,
			message: e.Message,
		})
	}

	return out.Value, nil
}
