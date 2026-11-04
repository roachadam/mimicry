# Client hints

Chrome sends three client hints with every secure request: `sec-ch-ua`,
`sec-ch-ua-mobile` and `sec-ch-ua-platform`. A site can ask for more with
`Accept-CH`. These include the system version, CPU, screen, memory and
network, and Chrome sends them from then on. mimicry's Chrome profiles do
the same; Firefox and Safari ignore the opt-in, and so do their profiles.
See [decision 0055](../decisions/0055-client-hints-opt-in.md), and
`ExampleConfig_clientHints` in [`example_test.go`](../../example_test.go)
for a runnable walk-through.

## What happens

- A navigation's response with `Accept-CH`, a redirect's or a form POST's
  included, stores the hints for its origin (scheme, host and port). A
  later `Accept-CH` replaces the set, and a response without one keeps it.
  A subresource's `Accept-CH` changes nothing.
- Later requests to that origin carry the hints, in the browser's order and
  with the machine's values. So do the requests its pages make to their
  own origin.
- A response whose `Critical-CH` names a stored hint that the navigation did
  not send makes mimicry send the navigation again, once, as Chrome does,
  a redirect included. `RoundTrip` returns the second response; the first
  is read and dropped. Only GET navigations restart: Chrome sends a form
  POST once.
- `Clear-Site-Data: "clientHints"` (or `"*"`) on any response forgets its
  origin's hints. A page already loaded keeps sending its own: the last
  navigation's page's requests (`Request.From` equal to it, or left nil)
  still carry the hints it loaded with.
- Requests to other origins carry only the three low-entropy hints, unless
  the page delegates its hints to them (below).
- Nothing changes for non-secure (`http://`) URLs: no browser sends them
  hints.

## The store

`Config.ClientHintStore` keeps each origin's opt-in. Left nil, every
Transport has its own. It belongs to one identity, like the cookie jar:
share it between the Transports of one identity, and save it with the jar
to resume that identity later.

```go
store := mimicry.NewClientHintStore()
client, err := mimicry.NewClient(mimicry.Config{
	Profile:         profiles.ChromeMacOS(),
	ClientHintStore: store,
})

// Later: save it alongside the cookies, and read it back for the same
// identity with store.ReadJSON.
err = store.WriteJSON(f)
```

`MemoryClientHintStore` saves and loads as JSON (`WriteJSON`, `ReadJSON`),
like the cookie jar and `AltSvcCache`. Any type with the `ClientHintStore`
methods works too.

## The machine

Hint values describe one computer:

- its system version, CPU architecture and bitness;
- its screen (DPR, viewport) and memory;
- its colour and motion preferences;
- its connection type.

By default these are the values of the machine the profile was captured on.
`Config.ClientHints` sets another machine:

```go
m, ok := profiles.RandomMachine("windows") // one of profiles.Machines("windows")
if !ok {
	return errors.New("no captured Windows machine")
}
client, err := mimicry.NewClient(mimicry.Config{
	Profile:     profiles.ChromeWindows(),
	ClientHints: &m,
})
```

`profiles.Machines(platform)` lists every captured machine. Each is one
real machine, so its values agree with each other. Pick one per identity
and keep it: a machine whose values change between requests, or mix
several machines, gives a client away. You can also set values of your own
(`profile.Machine`). `NewTransport` checks only their form, not that they
are plausible together.

Two hints depend on the connection:

- `rtt` is the last TCP connect's time, from its `connect()` to the
  connection being up: no DNS lookup, and through a proxy only the connect
  to the proxy. QUIC connections do not update it.
- `downlink` is the machine's estimate.

Each gets a noise factor of ±10% and is rounded as Chrome rounds it (50 ms;
0.05 Mbps). Both are fixed per host, separately for navigations and
subresources, when first sent, and keep that value for the Transport's
life, as Chrome's stay per host. `ect` is the machine's.

## Delegation

A page can share its hints with other origins. Chrome reads its
`Permissions-Policy` header and its `<meta http-equiv="Delegate-CH">` tag;
mimicry reads neither, so say it on the request:

```go
req = mimicry.With(req, mimicry.Request{
	Kind:        mimicry.Image,
	From:        page,
	ClientHints: mimicry.HintsDelegated, // the page delegates to the image's origin
})
```

- `HintsDefault`: the page's own origin gets every hint it opted in to;
  another origin, a subdomain included, gets the low-entropy three.
- `HintsDelegated`: the request's origin gets every hint the page's origin
  opted in to. The page named it in an allowlist such as
  `ch-ua-arch=(self "https://cdn.example")`, or in `Delegate-CH`.
- `HintsWithheld`: the page's `Permissions-Policy` lists its own
  allowlists and leaves the request's origin out. The request sends no
  client hints at all, not even `sec-ch-ua`. A request to the page's own
  origin always gets its hints, whatever this says.

A navigation takes none of these: it sends what its own origin opted in to.

## Limits

- `Width` and `Sec-CH-Width` (sent for an `<img>` with `sizes`) are not
  sent: mimicry has no way to say an image's layout width.
- The page that owns an opt-in is `Request.From`'s origin. mimicry has no
  frame tree, so a request from inside a cross-origin frame (`From` set to
  the frame) uses the frame's origin's opt-in. A browser would use the
  top-level page's, filtered by the frame's delegation.
- mimicry remembers only the last navigation's page's hints. A request
  from an earlier page (`Request.From` set to it) sends its origin's
  current set, which a later `Accept-CH` or `Clear-Site-Data` may have
  changed; Chrome's earlier tab would still send what that page loaded
  with.
- `Clear-Site-Data` on a redirect's response was not captured; mimicry
  treats it as on any other response.
- Profiles without a client-hints capture ignore the opt-in:
  `chrome-155-macos`, and Chrome on iOS, which sends no client hints.
