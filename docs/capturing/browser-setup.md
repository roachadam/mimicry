# Browser setup

## Chrome, Edge, Brave and Firefox

- They run with throwaway profiles: no extensions, default settings.
- They resolve the capture hosts to 127.0.0.1 themselves.
  The exception is the [ECH scenarios](scenarios.md#ech-scenarios), whose hosts they look up in DNS.
- Chrome, Edge and Brave are Chromium, launched with the same flags.
  They ignore certificate errors.
- Firefox gets a certificate exception.
  For pages across several hosts, it trusts the system's roots instead.
- Nothing on the system changes.

### Brave

- Brave keeps its Shields' defaults (decision 0067).
- Its own background requests (`*.brave.com`) never reach the capture hosts.
  Through the capture proxy they are refused and not recorded.
- Its user agent is Chrome's, so its captures are named after the browser launched.

## Safari

- Safari cannot be isolated.
  It opens a tab in the user's own window, and closes the tab afterwards.
- **Open window:** leave a Safari window open.
  - With none, closing the first sample's tab closes its window, and the next sample opened nothing.
  - Seen 2026-10-30, Safari 27: "no request within 1m30s".
  - A sample cut short that way can leave its cookies for the next run.
- **No extensions:** no content blocker or other extension may be enabled while capturing.
  They block requests and strip cookies.
- **Privacy:** default privacy settings.

### The resolver

- Safari resolves through the system.
- So the capture tool runs a DNS stub (`internal/dnsstub`) answering `*.test` with 127.0.0.1.
- The stub is reached through `/etc/resolver/test`, created once:

  ```sh
  sudo mkdir -p /etc/resolver && printf 'nameserver 127.0.0.1\nport 15353\n' | sudo tee /etc/resolver/test
  ```

- Firefox on macOS needs it too for the `ech` and `ech-h3` scenarios.
  It looks up their hosts through the system's resolver.
- Capture names are under `.test`, never `.localhost`.
  WebKit treats every `*.localhost` host as a site of its own, which turns same-site requests cross-site.

## The capture CA

- **Who trusts it:** Safari must trust it.
  So must the system, for Chrome's and Edge's HTTP/3 (see [HTTP/3 scenarios](http3-scenarios.md)).
- **Location:** `<user config dir>/mimicry/capture-ca/`, outside the repo.
- **Scope:** name-constrained to `test` and `localhost`, with IP addresses excluded.
  So it cannot vouch for any real site.
- **Trusting it:** do it once.
  The capture command prints this when needed:

  ```sh
  security add-trusted-cert -r trustRoot -p ssl -k ~/Library/Keychains/login.keychain-db \
    ~/Library/Application\ Support/mimicry/capture-ca/ca.pem
  ```

- **Undoing it:** `security remove-trusted-cert` with the same file, then delete the directory.

## The second Mac

- It is a Mac kept on macOS 26, reachable over ssh, with Chrome, Firefox and Safari 26.
- Pass its ssh alias with `cmd/refresh -mini`.
- It has no Go.
  `cmd/refresh` cross-compiles `cmd/capture` and `cmd/realcheck` for darwin/arm64.
  It copies them to a `refresh-<stamp>/` directory there.
- It needs, once:
  - **Screen session:** its ssh user logged in at its screen.
    Screen Sharing is enough.
    Browsers opened over ssh show on that session.
  - **Capture CA:** its own capture CA trusted there (`security add-trusted-cert` as above, at its screen).
  - **Resolver:** `/etc/resolver/test` (Safari, and Firefox for `ech`).
  - **QUIC:** python3 with aioquic (full mode).
  - **Gatekeeper:** each newly installed browser's first launch approved by Gatekeeper.
    Open it once at the screen, or `xattr -d com.apple.quarantine` the app.
- The preflight checks each of these.

## The Windows machine

- It runs Chrome and Firefox on Windows, with `cmd/refresh -windows`.
- Its setup is in [Refreshing profiles](../refreshing-profiles.md#the-windows-machine).

## The Linux machine

- It runs Chrome and Firefox on Linux, with `cmd/refresh -linux`.
- It can be a WSL distribution of the Windows machine, with `-wsl`.
- Its setup is in [Refreshing profiles](../refreshing-profiles.md#the-linux-machine).
- Each browser trusts the capture CA in its own throwaway profile, never the system.

## iPhones

Each iPhone browser is served from the Mac over the network.

| Browser | Name | Setup |
|---|---|---|
| Safari | `ios-safari` | [Safari on an iPhone](iphone.md#safari-on-an-iphone) |
| Chrome | `ios-chrome` | [Chrome on an iPhone](iphone.md#chrome-on-an-iphone) |
| Firefox | `ios-firefox` | [Firefox on an iPhone](iphone.md#firefox-on-an-iphone) |
