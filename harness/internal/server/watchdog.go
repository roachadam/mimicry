package server

import (
	"strings"
)

// A navigation the browser refuses (too many redirects, an upgrade with no
// fallback) ends on an error page, and the page's script, which would ask
// for /done, is gone. The watchdog is a service worker that outlives it: a
// page arms it for a number of milliseconds before such a navigation, and
// unless a page of its origin re-arms or disarms it by then, it asks for
// /done itself (finishing the capture with what the browser did) and
// unregisters. Its scope (/sw/) holds no page, so it intercepts nothing.
// Its requests (the script, its /done) are not steps.

// watchdogPath is the watchdog's script.
const watchdogPath = "/sw/watchdog.js"

// watchdogWorker is the service worker. A message of ms > 0 arms it for
// ms, 0 disarms it; each is acknowledged on the message's port, and an
// armed message keeps the worker alive until its time is up.
const watchdogWorker = `let due = 0;
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("message", e => {
  const ms = Number(e.data) || 0;
  due = ms > 0 ? Date.now() + ms : 0;
  if (e.ports[0]) e.ports[0].postMessage(true);
  if (ms > 0) {
    e.waitUntil(new Promise(d => setTimeout(d, ms + 100)).then(() => {
      if (due === 0 || Date.now() < due) return;
      due = 0;
      return fetch("/done").catch(() => {}).then(() => self.registration.unregister());
    }));
  }
});
`

// watchdogPage defines a page's helpers, every one bounded in time:
// within(p, ms) settles with p or after ms; register() installs the
// watchdog and waits until it is active (the first page of its origin);
// arm(ms) arms it (0 disarms), true once acknowledged; end() disarms and
// unregisters it.
const watchdogPage = `const within = (p, ms) => Promise.race([Promise.resolve(p).catch(() => {}), new Promise(d => setTimeout(d, ms))]);
const sw = navigator.serviceWorker;
const register = () => within(sw && sw.register("` + watchdogPath + `", {scope: "/sw/"}).then(r => {
  const w = r.installing || r.waiting || r.active;
  return w.state === "activated" || new Promise(d => w.addEventListener("statechange", () => w.state === "activated" && d(true)));
}), 5000);
const arm = ms => within(sw && sw.getRegistration("/sw/").then(r => r && r.active && new Promise(d => {
  const c = new MessageChannel();
  c.port1.onmessage = () => d(true);
  r.active.postMessage(ms, [c.port2]);
})), 2000);
const end = () => arm(0).then(() => within(sw && sw.getRegistration("/sw/").then(r => r && r.unregister()), 2000));
`

// watchdogResponse answers the watchdog's script.
func watchdogResponse() response {
	return response{
		status:      200,
		contentType: "text/javascript",
		body:        []byte(watchdogWorker),
	}
}

// pageScript wraps a page's statements in an async function after the
// watchdog helpers.
func pageScript(statements []string) string {
	return "<script>\n(async () => {\n" + watchdogPage + strings.Join(statements, "\n") + "\n})();\n</script>\n"
}
