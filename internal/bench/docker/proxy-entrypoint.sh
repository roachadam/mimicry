#!/bin/sh
# The proxy's own network delay: everything the proxy container sends (to
# the servers and back to the client) waits BENCH_PROXY_DELAY, so a round
# trip through the proxy gains about twice that. 0 or empty: none.
# BENCH_PROXY_DELAY_MODE says how: netem (the default) on eth0, or proxy,
# the proxy's in-process delay line (cmd/proxy/delay.go) for hosts without
# sch_netem.
set -eu

if [ -n "${BENCH_PROXY_DELAY:-}" ] && [ "${BENCH_PROXY_DELAY}" != "0" ]; then
	case "${BENCH_PROXY_DELAY_MODE:-netem}" in
	netem) tc qdisc replace dev eth0 root netem limit 100000 delay "${BENCH_PROXY_DELAY}" ;;
	proxy) ;;
	*)
		echo "proxy-entrypoint: BENCH_PROXY_DELAY_MODE must be netem or proxy" >&2
		exit 2
		;;
	esac
else
	export BENCH_PROXY_DELAY_MODE=netem
fi

exec /usr/local/bin/proxy
