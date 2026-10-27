#!/bin/sh
# The proxy's own network delay: every packet the proxy container sends (to
# the servers and back to the client) waits BENCH_PROXY_DELAY, so a round
# trip through the proxy gains about twice that. 0 or empty: none.
set -eu

if [ -n "${BENCH_PROXY_DELAY:-}" ] && [ "${BENCH_PROXY_DELAY}" != "0" ]; then
	tc qdisc replace dev eth0 root netem limit 100000 delay "${BENCH_PROXY_DELAY}"
fi

exec /usr/local/bin/proxy
