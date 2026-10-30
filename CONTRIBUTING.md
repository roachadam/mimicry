# Contributing

Contributions are welcome: bug reports, fixes, new profiles, docs.

## Before you open anything

- **Search first.** Check open and closed issues and pull requests so you
  don't open a duplicate. If one exists, add to it instead.
- **Issue or pull request first: either is fine.** For a large change, an
  issue first saves you work if the approach needs discussing.
- **Security problems** go through private reporting, not a public issue:
  see [`SECURITY.md`](SECURITY.md).

## Reporting a bug

Include:

- the mimicry version or commit, and the Go version;
- the profile (`profiles.Chrome()`, `chrome-156-macos`, …);
- what you sent, what you expected, and what happened (a minimal program
  is best);
- for a fingerprint mismatch, how you observed it (a fingerprint service,
  a proxy like mitmproxy, a packet capture) and what the real browser sent.

## Pull requests

- **Link the issue it solves** (`Fixes #123`), or describe the problem in
  the pull request if there is no issue.
- **Keep it focused.** One change per pull request is easier to review.
- **Readable and documented.** Code a newcomer can follow; comments where
  the why isn't obvious. A change in behaviour updates the docs in the same
  pull request: the concept page in `docs/concepts/`, the README if it
  mentions it, and a decision record in `docs/decisions/` when it changes a
  design decision.
- **Tested.** Ideally a test that fails before your change and passes after
  it, covering the failure case and the fix. Use judgement for docs and
  small refactors.
- **Green gate.** Run it before pushing:

  ```sh
  moon run :vet :lint :test
  ```

  Go and moon are pinned in `.prototools` (`proto install` sets them up).
  [`AGENTS.md`](AGENTS.md) explains the commands, the code style and the
  package boundaries.

  CI runs lint and tests on every pull request; it must be green to merge.
  A pull request runs the library's tests and the fast end-to-end replays,
  plus the slow HTTP/2, HTTP/3 and generator tests when it touches that
  code; main and a nightly run add every test, the timing-heavy ones too.

Using AI tools is fine. What matters is that you understand the change and
it meets the points above.

## Changes to how mimicry looks on the wire

mimicry copies real browsers from captures of what they send, so a change
to a profile or to anything a server can see needs evidence from the real
browser, not from another client or the browser's own developer tools.
Say in the pull request what you captured and how.
[`docs/capturing.md`](docs/capturing.md) and
[`docs/refreshing-profiles.md`](docs/refreshing-profiles.md) describe the
tooling; [decision 0003](docs/decisions/0003-evidence-rule.md) explains the
rule. Profile sections marked as generated are regenerated, never edited by
hand.

## Licence

By contributing you agree that your contribution is licensed under the
project's MIT licence ([`LICENSE`](LICENSE)).
