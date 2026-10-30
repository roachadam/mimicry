# 0047: Windows profiles, starting with Chrome

- Status: Accepted
- Date: 2026-10-29
- Commits: pending (fill in when it lands)

**Summary.** Built-in profiles can now be for Windows as well as macOS,
named `<browser>-<major>-windows`, captured from a real Chrome window on a
Windows desktop by the same harness. `profiles.Chrome()`, `Firefox()` and
`Safari()` stay the newest macOS version; a Windows family gets its own
newest-version accessor, `profiles.ChromeWindows()`, generated once its
first profile file exists, and the five-version policy and capture
inheritance of decision 0035 apply per browser and platform. Only Chrome
is captured on Windows for now.

## Problem

Every built-in profile was captured on macOS, so every request mimicry
sends says it comes from a Mac. Most desktop traffic comes from Windows,
and a client claiming macOS stands out against that. A caller who needs
to pass as Windows had to edit a macOS profile's user agent and client
hints by hand, which breaks decision 0003: values no browser was seen
sending, and anything else that differs on Windows left unmeasured.

## Options

- **Edit macOS profiles' platform headers.** Cheap, but it is guessing
  (decision 0003), and whatever else differs on Windows stays macOS's.
  Rejected.
- **Capture Chrome, Firefox and Edge on Windows at once.** Triples the
  capture work before the harness is known to work on Windows. Deferred:
  Chrome is the most used browser on Windows, and one browser proves the
  harness port.
- **Capture Chrome on Windows with the existing harness, as its own
  profile family.** Chosen.

For the newest-version accessors, the options were:

- make `Chrome()` the newest across platforms: which one it returns would
  depend on which platform was refreshed last, and could switch a caller
  from macOS to Windows silently;
- add a platform argument (`Chrome("windows")`): a runtime error for a
  platform without profiles, where a missing function is a compile error;
- keep `Chrome()` on macOS and generate `ChromeWindows()` per family.
  Chosen.

## Decision

- **Names.** `<browser>-<major>-<platform>`, platform `windows`:
  `chrome-156-windows`, accessor `profiles.Chrome156Windows()`.
  `cmd/profilegen` refuses a file whose name is not its own browser,
  major version and platform. Capture files are named the same way as on
  macOS, with this name (`chrome-156-windows.<scenario>…`).
- **Accessors.** `Chrome()`, `Firefox()` and `Safari()` return the newest
  macOS profile, as before. Each browser on another platform gets
  `<Browser><Platform>()`, the newest profile of that family
  (`ChromeWindows()`). The generator writes an accessor only for families
  with a profile file, so none exists before the first Windows capture.
  `profiles.ByName`, `Names` and `Supported` list Windows profiles like
  any other.
- **Versions** (decision 0035) are counted per browser and platform: the
  newest five Chrome versions on Windows are supported whatever macOS
  has. A deprecated Windows profile's accessor points to
  `ChromeWindows()`.
- **Inheritance** stays within one browser and platform: a Windows
  profile's `capturesFrom` names only an older Windows profile of the same
  browser (`profile.Validate` and `cmd/profilegen` refuse otherwise). A
  Windows capture never stands in for a macOS one, nor the other way: the
  first Windows profile of a browser is a full capture.
- **What differs on the wire.** Known before capturing: the user agent
  (`Windows NT 10.0; Win64; x64` in place of `Macintosh; Intel Mac OS X
  10_15_7`) and `sec-ch-ua-platform` (`"Windows"`). Everything else (the
  TLS and QUIC hellos, the HTTP/2 preface, header order, the cookie, CORS,
  redirect and connection rules, HTTP/3 timings) is whatever the captures
  show; nothing is copied from the macOS profile on the assumption that it
  is the same.
- **Capture.** `cmd/refresh` takes browser `chrome-windows` and
  `-windows <ssh alias>`. On the Windows machine `capture` starts Chrome in
  the logged-in user's desktop session, trusts the capture CA in the
  machine Root store only for the run, and maps the test hosts with
  Chrome's `--host-resolver-rules`, leaving system DNS alone; see
  [Capturing](../capturing.md). A scenario that needs real DNS behaviour
  the flag cannot give is marked unsupported on Windows, with its reason,
  rather than captured another way.

## Evidence

None yet: this record sets the naming and the rules; the first
`chrome-*-windows` capture and its ledger column in `profiles/EVIDENCE.md`
are the evidence for the profile itself. `cmd/profilegen`'s
`TestGenerateWindows` shows `ChromeWindows()` and the versioned accessors
generated from a Windows file, with `Chrome()` still on macOS.

## Consequences

- A caller picks the platform by accessor: `profiles.ChromeWindows()` or
  `profiles.ByName("chrome-156-windows")`. Code calling `Chrome()` is
  unaffected.
- Each refresh of Chrome now has two families to keep within five
  supported versions, and the Windows one needs the Windows machine with
  a user logged on at its console.
- Until the first Windows capture, there is no Windows accessor; code
  using one does not compile, rather than failing at run time.
- The harness tests by default check `Chrome()`, `Firefox()` and
  `Safari()`; a Windows profile is checked by `:test-full` or by naming
  it in `MIMICRY_PROFILES`.

## Revisit when

- Firefox or Edge is captured on Windows: the same rules apply, and
  `cmd/refresh` gains their browser names.
- Most callers ask for Windows: the unqualified accessors could then move
  to Windows, which would be a breaking change for every caller of
  `Chrome()`.
