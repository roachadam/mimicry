# 0010: `Config.DialContext` for the TCP connection

- Status: Accepted
- Date: 2026-10-24
- Commits: `f445dc1`

## Problem

The kinds end-to-end test serves `www.kinds-a.test`, `static.kinds-a.test`
and `cdn.kinds-b.test` from one local server; site relations depend on
those real names, so `localhost` cannot stand in. Callers also need to pin
a host to an address or bring their own network code.

## Decision

`Config.DialContext`, shaped like `http.Transport.DialContext`, opens the
TCP connection; TLS and everything above stay the profile's.
`Config.DialTimeout` still bounds it. It plugs into the single dial path
(`internal/dial`), so there is still one place connections start.

## Consequences

Proxies belong in the same dial path; a caller's `DialContext` and a
configured proxy must be defined to compose, not to race. Since
[0014](0014-proxies.md) they do: `DialContext` opens the connection to the
proxy, and the tunnel through it is the profile's.

## Revisit when

A caller needs to dial the server's address itself while also using a
proxy.
