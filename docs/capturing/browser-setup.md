# Browser setup

- **Chrome, Edge, Brave and Firefox** run with throwaway profiles (no
  extensions, default settings) and resolve the capture hosts to
  127.0.0.1 themselves (but for the [ECH scenarios](scenarios.md#ech-scenarios),
  whose hosts they look up in DNS); Chrome, Edge and Brave (Chromium, launched with
  the same flags) ignore certificate errors and Firefox gets a certificate
  exception (or, for pages across several hosts, trusts the system's
  roots). Nothing on the system changes. Brave keeps its Shields'
  defaults (decision 0067); its own background requests (`*.brave.com`)
  never reach the capture hosts, and through the capture proxy they are
  refused and not recorded. Its user agent is Chrome's, so its captures
  are named after the browser launched.
- **Safari** cannot be isolated: it opens a tab in the user's own window
  (closed afterwards), so leave a Safari window open: with none, closing
  the first sample's tab closes its window, and the next sample opened
  nothing (2026-10-30, Safari 27: "no request within 1m30s"; a sample cut
  short that way can leave its cookies for the next run). It must have
  **no content blocker or other extension enabled** while capturing (they
  block requests and strip cookies) and
  default privacy settings. It resolves through the system, so the capture
  tool runs a DNS stub answering `*.test` with 127.0.0.1
  (`internal/dnsstub`), reached through `/etc/resolver/test`, created once:

  ```sh
  sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test
  ```

  Firefox on macOS needs it too for the `ech` and `ech-h3` scenarios,
  whose hosts it looks up through the system's resolver.

  Capture names are under `.test`, never `.localhost`: WebKit treats every
  `*.localhost` host as a site of its own, which turns same-site requests
  cross-site.
- **The capture CA.** Safari must trust it, and so must the system for
  Chrome's and Edge's HTTP/3 (see below). The CA lives in
  `<user config dir>/mimicry/capture-ca/`, outside the repo, and is
  name-constrained to `test` and `localhost` with IP addresses excluded, so
  it cannot vouch for any real site. Trust it once; the capture command
  prints this when needed:

  ```sh
  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db \
    ~/Library/Application\ Support/mimicry/capture-ca/ca.pem
  ```

  To undo: `security remove-trusted-cert` with the same file, then delete
  the directory.
- **The second Mac** (a Mac kept on macOS 26, reachable over ssh; pass its
  ssh alias with `cmd/refresh -mini`; Chrome, Firefox and Safari 26) has
  no Go: `cmd/refresh` cross-compiles `cmd/capture` and `cmd/realcheck` for
  darwin/arm64 and copies them to a `refresh-<stamp>/` directory
  there. It needs, once: its ssh user logged in at its screen (Screen
  Sharing is enough; browsers opened over ssh show on that session), its
  own capture CA trusted there (`security add-trusted-cert` as above, at
  its screen), `/etc/resolver/test` (Safari, and Firefox for `ech`), python3 with aioquic (full
  mode), and each newly installed browser's first launch approved by
  Gatekeeper (open it once at the screen, or `xattr -d
  com.apple.quarantine` the app). The preflight checks each of these.
- **The Windows machine** (Chrome and Firefox on Windows, `cmd/refresh
  -windows`): its setup is in
  [Refreshing profiles](../refreshing-profiles.md#the-windows-machine).
- **The Linux machine** (Chrome and Firefox on Linux, `cmd/refresh
  -linux`, a WSL distribution of the Windows machine with `-wsl`): its
  setup is in [Refreshing profiles](../refreshing-profiles.md#the-linux-machine).
  Each browser trusts the capture CA in its own throwaway profile, never the
  system.
- **Safari on an iPhone** (`ios-safari`): served from the Mac over the
  network; see [below](iphone.md#safari-on-an-iphone).
- **Chrome on an iPhone** (`ios-chrome`): the same, with Chrome; see
  [below](iphone.md#chrome-on-an-iphone).
- **Firefox on an iPhone** (`ios-firefox`): the same, with Firefox; see
  [below](iphone.md#firefox-on-an-iphone).
