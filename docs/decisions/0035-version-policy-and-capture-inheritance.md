# 0035: Newest-version accessors, five supported versions, inherited captures

- Status: Accepted; amended by 0047
- Date: 2026-10-26
- Commits: `2c913bf`

**Summary.** `profiles.Chrome()`, `Firefox()` and `Safari()` return the
newest built-in version; the newest five versions per browser and platform
are supported and older ones are marked deprecated. A new version
recaptures only the fingerprint layer and inherits other scenarios'
captures by reference (`capturesFrom`), with version strings rewritten in
comparisons, so profiles can ship each release instead of going stale. For
a library user, the unversioned accessors track new versions, while a
rule changed inside an inherited scenario can go unseen until the next
full refresh.

## Problem

Chrome and Firefox ship a new major version about every four weeks, and
Safari one with each macOS. Until now each new version needed:

- a full recapture: 47 scenarios, 3 samples each, about 1.5–2 h of screen
  takeover per browser (the refresh plan's estimate, not measured);
- a rename of `profiles.Chrome155MacOS()` in about 25 files of examples,
  docs and tests;
- one more full run of the e2e suite, which took over four minutes without
  `-race` with the four profiles of the time.

kindsgen refuses a profile without all ten of its captures, and every e2e
test fails on a missing capture. So a profile could not ship until its
browser had been recaptured completely. Most of what those captures pin
(cookie rules, CORS, redirects, proxies, the HTTP/3 failure model,
connection reuse, resumption) rarely changes between consecutive versions.
The fingerprint layer does change with every version: the TLS hello, the
HTTP/2 preface, the headers (version strings at the least), and the QUIC
hello.

## Options

- **Full recapture of every version.** This is the most evidence, but at
  that cost per browser per month it gets skipped, and the profiles go
  stale instead.
- **Copy the previous version's captures under the new name.** The files
  would claim a browser that never produced them, which breaks decision
  0003's evidence rule. Rejected.
- **Inherit profile values between versions** (httpcloak's `based_on`).
  Rejected again for the reasons in decision 0004: values that ride along
  unseen.
- **Inherit captures, per scenario, by reference.** The profile file stays
  complete. A new version recaptures the fingerprint layer, and for every
  other scenario names the earlier version whose captures it is checked
  against. Comparisons rewrite only the version tokens in the captured
  header values. Chosen.

For the version names in code, the options were:

- keep the versioned accessors only, and rename them each release;
- add string constants;
- add generated newest-version accessors. Chosen.

## Decision

**Versions.**
- `profiles.Chrome()`, `profiles.Firefox()` and `profiles.Safari()`
  return the newest built-in version of each browser.
- The versioned accessors stay for when a version must be fixed.
- The newest five versions of each browser on each platform are
  supported. Older ones stay in the repo (JSON, accessor, captures) with
  `"deprecated": true`.
  - The generator writes them a `// Deprecated:` accessor.
  - `profiles.Supported()` leaves them out; `profiles.Names()` still lists
    every built-in.
  - `cmd/profilegen` refuses a set that breaks the rule in either
    direction, or two profiles of one browser and platform with the same
    version.
- Examples, docs and tests use the newest-version accessors unless the
  version matters to what they show. Examples whose output prints the user
  agent or the profile name keep `Chrome155MacOS()`.

**Inherited captures.** `"capturesFrom": {"<scenario>": "<profile>"}`
names, per capture scenario, the profile whose captures this profile is
checked against.
- `profile.Validate` checks:
  - the scenario name's form;
  - that the named profile is not the profile itself;
  - that the named profile is of the same browser and platform (by name).
- `cmd/profilegen` checks:
  - that it is a built-in, older version;
  - that it holds its own captures of the scenario: chains are refused,
    so a resolver never follows more than one step;
  - in the harness, that the key is a known scenario and never the
    default page (`TestCapturesFromNamesCapturedScenarios`).
- A key covers the scenario's `.local-roots` capture too. Replays are
  keyed `replay-<site>`.
- Everything that loads captures resolves through `capture.Inherited` and
  `capture.Files` (in `internal/harness/internal/capture`): hellogen, h3gen,
  kindsgen, and every e2e test.
- Gap tables keyed by profile name are looked up by the profile that took
  the capture. A gap pinned on chrome-155's cookies capture therefore
  applies to chrome-156 when it inherits that capture. This covers
  `sampleGaps`, `h3PriorityGaps`, `partitionGaps`, `headersOrderGaps`,
  `redirects*`, `connectionsPreconnects`, `h3Gaps`, `h3EdgeGaps` and
  `missingCaptures`.

**The version rewriter** (`capture.Rewriter`) is applied only to inherited
captures.
- In captured header values and lines, it rewrites:
  - the user agent's version token, as each profile's own user agent has
    it (Chrome `Chrome/155.0.0.0`; Firefox `rv:158.0` and
    `Firefox/158.0`; Safari `Version/27.0.1`);
  - the whole `sec-ch-ua` value, from the source profile's to this one's.
    Chrome's brand order and GREASE brand change with the major version
    as well as its numbers.
- It rewrites decoded HTTP/2 and HTTP/3 field values, HTTP/1.1 and CONNECT
  lines, and QPACK insert values.
- Bytes compared as sent stay as captured: HPACK and QPACK reproduction,
  and h3gen's Huffman and encoder rules. Those compare the encoder with
  the browser's own blocks, whatever the values.
- kindsgen derives its header lists from rewritten captures, so they carry
  the profile's own user agent.
- Any other difference still fails.

**Test selection** (`internal/harness/internal/testprofiles`). By default each
harness test checks the newest profile of each browser.
- `MIMICRY_PROFILES=all` (`moon run :test-full`) checks every supported
  one.
- A comma-separated list checks the built-in names and profile files it
  names.
- `TestInheritedProfile` always runs.

## Evidence

The proof is a synthetic profile, `testprofiles.Inheriting`: chrome-155-macos
renamed chrome-999-macos (first chrome-156-macos, renamed once the real
Chrome 156 profile arrived so the two never share captures), with all 187
`Chrome/155.0.0.0` and 103 `v="155"` strings rewritten to 999 and a
different `sec-ch-ua` brand order and GREASE brand. It inherits every
scenario chrome-155 captured except the default page.

- **`TestInheritedProfile` (e2e).** It reruns the test binary on it.
  - It passes the 18 e2e tests that read non-default captures. In a full
    run it also passes the three HTTP/3 replays.
  - With `accept-language` changed to `en-GB,en;q=0.9` it fails
    `TestKindsMatchBrowserTraces`, `TestHTTP1MatchesBrowserTrace` and
    `TestCORSEdgeMatchesBrowser`.
  - With the rewriter turned off (tried by hand, not kept), it fails the
    cookies scenario, CORS edge, headers-order and HTTP/1.1 tests, among
    others.
- **`TestRegenerateReadsInheritedCaptures` (hellogen, h3gen, kindsgen).**
  Regenerating the synthetic profile from chrome-155's captures
  reproduces it byte for byte.

## Consequences

- Shipping a new version costs a quick recapture of the fingerprint layer
  (`cmd/refresh`), not a full one. Its ledger column says which rows are
  inherited (`I: from <profile>`, `profiles/EVIDENCE.md`).
- **Risk: a rule that changes within an inherited scenario goes unseen
  until the next full refresh of that browser.** The version's own traffic
  is never compared for it. Examples are a new cookie rule, a CORS
  preflight change, or an HTTP/3 fallback timing. This is the price of the
  quick tier. It is bounded by:
  - running a full refresh before a release;
  - running one when the quick capture shows a change beyond version
    strings;
  - running one periodically (backlog: Tooling and tests).
- A version-string change outside the user agent and `sec-ch-ua` would show
  as a failure in an inherited comparison. Examples are a new client hint
  sent by default, or the version moving into another header. That is
  intended: it means a recapture.
- An inherited capture keeps the source version's byte lengths, for
  instance where a version goes from 99 to 100. Comparisons of encoded
  bytes run against the source's own blocks, so they stay consistent.
- By default the harness runs three profiles, not every supported one. A
  regression only an older supported version shows (Chrome 155 and Safari 26 today) is
  caught by `:test-full`, not `:test`.
- Deprecated profiles are not tested unless named in `MIMICRY_PROFILES`.

## Revisit when

- A full refresh finds a rule that changed inside an inherited scenario
  between two quick refreshes. Shorten the period, or move that scenario
  into the quick tier.
- A vendor is found checking behaviour that only inherited scenarios
  cover.

## Amendments

- 0047: the newest-version accessors `Chrome()`, `Firefox()` and `Safari()` are macOS; other platforms get `<Browser><Platform>()` (`ChromeWindows()`), and the tests' default set is the newest profile of each browser on each platform.
