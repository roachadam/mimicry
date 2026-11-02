# 0044: Transports find their shared profile by a fingerprint of its content, not its JSON

- Status: Accepted; amended 2026-11-02 (profiles are read-only values: the walk runs once per edit, not per client)
- Date: 2026-10-29
- Commits: `e28df4c`

**Summary.** Transports built from equal profiles now find their shared
copy by SHA-256 of a compact binary walk of the profile, instead of
encoding it to JSON every time. The JSON encoding was most of the cost of
building a client; the walk still reads the whole profile, so a profile
changed after use never gets the old copy. For a library user,
`NewTransport` is about 2.5 times faster with less allocation, though
building clients up front, one per proxy, remains the advice.

## Problem

Transports built from equal profiles share one decoded, validated copy
(`profile.go`, `snapshot`): a profile is most of a Transport's memory. To
find the shared copy, `NewTransport` encoded the caller's profile to JSON
(about 140 KB compact for Chrome 156) and hashed it with SHA-256, every
time, whether or not a copy already existed. That was most of building a
client: in the benchmark at `df5a13f`
([linux-2026-10-29](../../internal/bench/reports/linux-2026-10-29/bench-2026-10-29.md),
the Linux test host, i5-13500) `BenchmarkNewClient` took 595 µs, 294 KB and 1,109 allocations
for mimicry against 1.3 µs for tls-client and 0.3–0.8 µs for azuretls, and
in client rotation (a new client every 10 requests) mimicry used 201–216
µs of CPU per request against tls-client's 123–145.

On the M3 Pro laptop (`profiles.Chrome()` then `NewTransport`, the bench
adapter's constructor) the 506 µs split as: the accessor's deep copy 122
µs, `json.Marshal` 296 µs, SHA-256 of its 140 KB 47 µs, the rest about
40 µs.

The question was how to stop paying for the encoding without a footgun:
a profile is a plain struct with exported fields, the accessors return a
fresh copy the caller may change, and a caller may change a profile after
building a Transport from it and build another. Two equal custom
profiles must still share, and a changed profile must never get the copy
of what it was before.

## Options

- **A content hash precomputed for the built-in profiles** (`go generate`)
  and carried by the accessors' copies, or **a cache by pointer**: both
  tell nothing once the caller writes into the copy, and nothing in Go
  sees that write. A profile changed after `profiles.Chrome()` (or after a
  first `NewTransport`) would silently get the unchanged profile's copy:
  another browser's fingerprint, without an error. Making the check safe
  means reading the whole profile anyway. Rejected.
- **Hash the source JSON bytes when unmodified**: the same problem;
  "unmodified" is only known by reading the profile.
- **Profiles that cannot change** (unexported fields, getters, a builder):
  safe and free to identify, but it rewrites the public `profile`
  package and every caller that builds or edits a profile. Out of
  proportion.
- **Compare with `reflect.DeepEqual` against a copy found by name**: exact,
  but 109 µs and 158 allocations on the M3 Pro laptop (its visited map), plus a list
  of candidates per name to keep. No faster than hashing.
- **A generated hasher** (code emitted per profile type): the fastest
  (no reflection), but a second generator to keep in step with the
  profile types; one that misses a new field shares profiles that differ
  in it, silently. Not worth it at the cost below.
- **Hash a compact binary form written by one reflective walk** (chosen).

## Decision

- `fingerprint` walks the profile with reflection and writes every value
  into a pooled buffer in a form no other content has: the fields in
  declaration order, a byte for whether each pointer, slice and map is
  set, a length before every string, slice and map, map entries by sorted
  key, integers as varints. SHA-256 of that (85 KB for Chrome 156, 60% of
  the compact JSON) keys the shared copies, as the JSON's hash did. Equal
  fingerprints are equal profiles; nil and empty differ, which can only
  cost sharing, never correctness. SHA-256 rather than a faster
  non-cryptographic hash: a collision would hand a Transport another
  profile without an error, and the hash is a quarter of the cost.
- Only a profile no live Transport holds yet is encoded to JSON and
  decoded (the copy that later changes cannot reach, validated), as
  before.
- `TestFingerprintCoversProfile` fails when a profile type gains a kind
  the walk cannot write (an interface, a channel, a function) or a type
  that contains itself, which would crash `NewTransport` (or never end) for
  a caller whose profile sets it.
- The accessors' deep copy (`profiles.deepCopy`) assigns a value with no
  pointer, slice or map in it (a header's name and value, a list of codes)
  whole instead of field by field.

## Evidence

- Mac, M3 Pro, `profiles.Chrome()` and `NewTransport` in a loop: 506 µs,
  572 KB, 1,118 allocations before; 197 µs, 147 KB, 867 after. The
  accessor 122 → 90 µs, finding the shared copy 343 → 100 µs (the walk
  about 70, SHA-256 about 25).
- The Linux test host (i5-13500), isolated runs
  ([focus-2026-10-29/ctor](../../internal/bench/reports/focus-2026-10-29/ctor/),
  before = df5a13f, after = df5a13f plus this change only),
  `BenchmarkNewClient` (10 passes, mimicry; HTTP/1.1 and HTTP/2, with and
  without SOCKS5): 594–597 µs → 235–241 µs (−60%), 292–295 KB → 122 KB,
  1,109–1,113 → 874–878 allocations. tls-client 1.3–1.8 µs, azuretls
  0.3–0.9 µs, unchanged.
- Confirmed in the final benchmark at `e28df4c`
  ([linux-final-2026-10-29](../../internal/bench/reports/linux-final-2026-10-29/final-2026-10-29.md),
  micro benchmarks): `BenchmarkNewClient` 235–237 µs, 122–125 KB and
  874–882 allocations for mimicry across protocols and proxies.
- Same host, layer 6 client rotation (100 routines, a new client every 10
  requests, HTTP/2 through CONNECT and SOCKS5, dropped and closed, 3
  passes): CPU per request 208–213 µs → 157–158 µs (tls-client 131–138,
  azuretls 158–163); allocations 280–283 → 258–260 per request; the
  constructor under load (p50) 627 → 249–251 µs; a new client alone 648–661
  → 288–306 µs, 380–388 → 234–241 KB with its first request. Requests/s,
  latency and memory per routine unchanged within the ranges.
- `TestTransportsShareProfile`: Transports built from two separate
  `profiles.Chrome()` copies share one copy; a profile changed in place
  after its Transports were built, and fresh copies changed in a map's
  entry (a kind's header), a list's order (cipher suites) and a pointer
  (redirects removed), each get a copy of their own holding the change;
  the shared copy goes with its last Transport. Mutation-checked: writing
  pointers' identities into the fingerprint fails the sharing check;
  skipping map values, or the elements of code lists, fails the change
  checks; a cleanup that never removes the entry fails the last check
  (which, keyed by the indented JSON's hash, had never looked at the right
  entry before).

## Consequences

- Building a client costs a walk and a hash of the profile instead of a
  JSON encoding: about 2.5× faster on the M3 Pro laptop, with a quarter of the
  allocation, still far more than clients that copy nothing (tls-client,
  azuretls). Building clients up front, one per proxy, remains the advice.
- A profile type may hold only data the walk writes (enforced by the
  test).
- Profiles that differ only in nil against empty slices or maps no
  longer share, where their JSON was equal; nothing builds such pairs.

## Revisit when

- Profiles become immutable values (then identity is enough), or the
  constructor matters again at the scale of a generated hasher.

## Amendment 2026-11-02: immutable profiles, identity is enough

- **Problem.** Profiling at `f63784e` on the Linux test host
  (i5-13500, `internal/bench` `BenchmarkNewClient`, 3 passes, cores 0-3,
  `GOMAXPROCS` 4) put `NewClient` at 243–257 µs, 126–128 KB and 874–888
  allocations on every protocol and proxy, about 99% of it in two passes
  over a profile of about 10,000 values that build nothing a client owns:
  the accessor's deep copy (122 µs, 113 KB; 47 µs of it a `sync.Map`
  lookup per value) and the fingerprint walk with its SHA-256 (about
  125 µs). Everything a client owns (jar, tickets, pools, Alt-Svc, proxy
  state, CORS cache) cost about 1 µs and 2 KB. A caller editing the
  profile per identity paid 1,983 µs and 941 KB per client. This is the
  "profiles become immutable values" case under Revisit when.
- **Decision.** Profiles are read-only values. `profile.Profile` is now a
  small value with one unexported field, safe to copy and share; the old
  struct, renamed `profile.Spec`, is its data, unchanged. `Name`,
  `UserAgent`, `Spec` (a deep copy, to read or edit) and `With` (copy,
  apply an edit, check and share once) are its methods; `profile.Parse`
  returns one; `profiles.Chrome()` and every accessor and `ByName` return
  the same one, parsed once per process on first use. `Config.Profile` is
  a `profile.Profile`, and `NewTransport` reads the checked Spec it holds
  directly: no copy, no walk. A clean cutover: no `*Profile` path is left.
- **Errors.** `With` never fails or panics: an edit that leaves the profile
  invalid returns a Profile carrying the problems (the
  `*profile.ValidationError` listing every one), which `NewTransport`
  returns; its `Spec` is the edited data, so a further `With` can fix it.
  The maintainer chose this over `With` returning an error (every chained
  edit would need checking) and over a panic. The zero Profile is no
  profile: `NewTransport` refuses it, as it refused nil.
- **Sharing.** Profiles of equal content still share one copy, and only
  that: what a client owns is never shared. The walk this record
  introduced now runs once per `With`, never per client, and it now
  compares first: `With` checks the edited Spec for exact equality (nil
  and empty differ, as in the fingerprint) against the copy last found or
  made under the same name, and only on a difference computes the
  fingerprint and looks it up. A miss encodes the edited Spec to JSON and
  decodes it, as before, so the copy kept is one the edit (which may have
  kept its `*Spec`) cannot reach, then validates it. The copy is held by
  value inside what the Profile points at, so a client holding the Spec
  keeps the entry alive: a profile edited again while one of its clients
  lives finds the same copy.
- **The other fixes the profiling ranked.** The deep copy (now
  `Profile.Spec`'s, in package `profile`, as `profiles` copies nothing any
  more) and the equality walk follow a plan worked out once per type
  instead of a `sync.Map` lookup per value. The fingerprint is written
  into the hash in 4 KB chunks instead of a pooled buffer the size of the
  profile, which a GC emptied (7–35 KB regrown per client before).
- **How the root package reads the Spec.** `profile` keeps it unexported
  so no caller can reach the shared copy. Rejected: an exported accessor
  (callers could write into what every client of the profile reads),
  `go:linkname` (an unchecked signature) and `unsafe`. Chosen:
  `internal/checked`, which imports nothing and holds a function `profile`
  sets as it is initialised; it is typed `any` because the package cannot
  import `profile` to name it, and the root asserts its type once.
  `profile`'s import rule allows that one package besides the standard
  library.
- **Wire unchanged.** A built-in profile's Spec is now its file parsed,
  where it was that copy encoded to JSON and decoded again; for all 9
  built-in profiles the two are `reflect.DeepEqual`. Edited profiles take
  the same encode-decode path as before. The e2e tests pass with only
  their API call sites changed (`checked` in `e2e/captures_test.go` turns
  a profile under test into its Profile, the shared one when unchanged).
- **Evidence.** `BenchmarkNewClient` (mimicry only; HTTP/1.1, HTTP/2 and
  HTTP/3, with and without SOCKS5), before (`f63784e`) and after, 3
  alternating passes each; and a root benchmark of `NewClient` from a
  built-in profile, and from one edited profile reused for every client
  (before: a `*Profile` with its user agent changed; after: a `With`
  result):

  | Host | Case | Before | After |
  |---|---|---|---|
  | Linux | `BenchmarkNewClient` | 245–256 µs, 125–127 KB, 874–886 allocations | 1.26–2.06 µs, 2.1–2.8 KB, 30–42 |
  | Linux | built-in profile | 261–276 µs, 147–149 KB, 886 | 1.51–1.54 µs, 2.2 KB, 36 |
  | Linux | edited profile, reused | 127–128 µs, 6.0–6.2 KB, 141 | 1.44–1.47 µs, 2.2 KB, 35 |
  | Mac (M3 Pro) | `BenchmarkNewClient` | 194–204 µs, 126–127 KB, 874–886 | 0.79–1.22 µs, 2.1–2.8 KB, 30–42 |
  | Mac | built-in profile | 197–204 µs, 147–148 KB, 885 | 0.91–0.94 µs, 2.2 KB, 36 |
  | Mac | edited profile, reused | 103–105 µs, 5.9–6.1 KB, 141 | 0.89–0.90 µs, 2.2 KB, 35 |

  A `With` per client (the edit repeated for every identity, its copy
  shared) costs 202–213 µs and 120 KB on Linux, 162–166 µs on the Mac:
  the copy and the comparison, once per call. `TestTransportsShareProfile`
  (now with `With` results found by name and by fingerprint, an edit that
  keeps and later changes its Spec, and a Transport outliving its Profile)
  fails when the equality walk skips map values or slice elements, the
  fingerprint skips map values, slice elements or pointers, the comparison
  by name is skipped, or the edited Spec is kept instead of its decoded
  copy; `TestSharedCopiesGo` fails when a collected copy's entries are
  never removed; `TestSpecsShareNothing` (package `profiles`) fails when
  the copy shares slices or maps.
- **Consequences.** A breaking change: `p.X = y` on an accessor's copy
  becomes `profiles.Chrome().With(func(s *profile.Spec) { s.X = y })`,
  built once and reused; reading becomes `p.Spec().X`, or `p.Name()`.
  Building a client is no longer a reason to build clients up front, but
  a Transport is still one browsing entity (decision 0019). Headers that
  differ per identity belong in `Config.Header`, which costs nothing; a
  User-Agent set there does not reach an HTTP proxy's CONNECT, which
  carries the profile's (Chrome's `HttpProxyConnectJob::GetUserAgent` and
  Firefox's `nsHttpConnection::MakeConnectString` also take the browser's
  own user agent, not a request's; a lead from their sources, not a
  capture), so a user agent meant for both is a `With` edit.
- **Revisit when** callers need many distinct edited profiles a second
  (each `With` is about 200 µs): then a generated copier and comparer.
