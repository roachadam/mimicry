# 0044: Transports find their shared profile by a fingerprint of its content, not its JSON

- Status: Accepted
- Date: 2026-10-29
- Commits: (where it landed)

## Problem

Transports built from equal profiles share one decoded, validated copy
(`profile.go`, `snapshot`): a profile is most of a Transport's memory. To
find the shared copy, `NewTransport` encoded the caller's profile to JSON
(about 140 KB compact for Chrome 156) and hashed it with SHA-256, every
time, whether or not a copy already existed. That was most of building a
client: in the final benchmark
([linux-2026-10-29](../../internal/bench/reports/linux-2026-10-29/bench-2026-10-29.md),
i5-13500) `BenchmarkNewClient` took 595 µs, 294 KB and 1,109 allocations
for mimicry against 1.3 µs for tls-client and 0.3–0.8 µs for azuretls, and
in client rotation (a new client every 10 requests) mimicry used 201–216
µs of CPU per request against tls-client's 123–145.

On the Mac (M3 Pro, `profiles.Chrome()` then `NewTransport`, the bench
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
  but 109 µs and 158 allocations on the Mac (its visited map), plus a list
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
- the Linux test host (i5-13500, Linux), isolated runs
  ([focus-2026-10-29/ctor](../../internal/bench/reports/focus-2026-10-29/ctor/),
  before = c1f37ab, after = c1f37ab plus this change only),
  `BenchmarkNewClient` (10 passes, mimicry; HTTP/1.1 and HTTP/2, with and
  without SOCKS5): 594–597 µs → 235–241 µs (−60%), 292–295 KB → 122 KB,
  1,109–1,113 → 874–878 allocations. tls-client 1.3–1.8 µs, azuretls
  0.3–0.9 µs, unchanged.
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
  JSON encoding: about 2.5× faster on the Mac, with a quarter of the
  allocation, still far more than clients that copy nothing (tls-client,
  azuretls). Building clients up front, one per proxy, remains the advice.
- A profile type may hold only data the walk writes (enforced by the
  test).
- Profiles that differ only in nil against empty slices or maps no
  longer share, where their JSON was equal; nothing builds such pairs.

## Revisit when

- Profiles become immutable values (then identity is enough), or the
  constructor matters again at the scale of a generated hasher.
