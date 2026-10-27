package server

import (
	"strings"
)

// The watchdog is a service worker that asks for /done when a refused
// navigation strands the page on an error page. A page arms it before such a
// navigation; unless re-armed or disarmed in time, it fires and unregisters.
// Its scope (/sw/) holds no page, and its requests are not steps.

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

// watchdogPage defines a page's time-bounded helpers: within(p, ms),
// register(), arm(ms) (0 disarms) and end() (disarm and unregister).
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
