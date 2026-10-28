#!/usr/bin/env bash
# Drives the benchmark's two compose stacks (../docker). Commands:
#
#   up       build the image, create the CA and content, start the server stack
#            and the client host's offloads sidecar
#   micro    layers 1-2: go test -bench, -count $BENCH_COUNT (10), then benchstat
#   run      micro, then layers 3-6 (bench run), then the report
#   report   layers 3-6 only, embedding the newest micro results
#   render   rewrite bench-<date>.md from its JSON with the newest micro results
#   ca-sync  copy the CA from a remote server stack to this host's volume
#   down     stop the server stack and the offloads sidecar
#
# Arguments after the command go to `bench run` (run, report) or to the test
# binary (micro). Settings, all optional:
#
#   BENCH_SERVER=<ip>              the server stack runs on another host at <ip>
#   BENCH_SERVER_CONTEXT=<ctx>     ... reached through this docker context
#                                  (or DOCKER_HOST=ssh://… for the server steps)
#   BENCH_CLIENT_CPUS, BENCH_SERVER_CPUS, BENCH_PROXY_CPUS   cpusets (0-3, 4-11, 10-11)
#   BENCH_CLIENT_PROCS, BENCH_SERVER_PROCS                    GOMAXPROCS (4, 8)
#   BENCH_PROXY_DELAY              netem delay on the proxy's packets (5ms)
#   BENCH_COUNT                    micro: -count (10)
#   BENCH_BENCHTIME                micro: BenchmarkRequest's -benchtime (1s)
#   BENCH_CONNECT_BENCHTIME        micro: BenchmarkConnect's -benchtime (200x: each
#                                  iteration also waits 5 ms untimed for its ticket)
#   BENCH_MICRO                    micro: one -bench pattern instead of both
#   BENCH_SERVERS                  micro: servers (nginx,go)
#   BENCH_FAST=1                   the whole run in about 30 minutes instead
#                                  of an hour (see the comment below); the
#                                  report records the settings used
#   BENCH_SKIP_BUILD=1             reuse the images a previous run built
#
# On macOS it keeps the machine awake (caffeinate -i) for its whole run.
set -euo pipefail

if [[ "$(uname -s)" == Darwin && -z "${BENCH_CAFFEINATED:-}" ]]; then
	export BENCH_CAFFEINATED=1
	exec caffeinate -i "$0" "$@"
fi

here="$(cd "$(dirname "$0")/.." && pwd)"
compose_dir="$here/docker"
reports="$here/reports"
date="${BENCH_DATE:-$(date +%F)}"

cmd="${1:-}"
[[ -n "$cmd" ]] && shift

# BENCH_FAST=1 trades precision for time: about 30 minutes instead of an
# hour. Micro benchmarks: 4 runs of 300 ms each (100 ms does not sample
# enough iterations at the slowest points). Load points: a 3 s window over
# a 1 s warm-up (run-to-run spread at 3 s measured the same as at 5 s, 2-3%),
# concurrency 1/64/256 (no 16), 20 idle clients, 3 page loads per
# condition, and 100 clients per multi-client point (layer 6, same windows).
# The report records what was used.
if [[ -n "${BENCH_FAST:-}" ]]; then
	: "${BENCH_COUNT:=4}"
	: "${BENCH_BENCHTIME:=300ms}"
	: "${BENCH_CONNECT_BENCHTIME:=50x}"
fi

# What the report records about this host.
if [[ "$(uname -s)" == Darwin ]]; then
	BENCH_HOST_CPU="${BENCH_HOST_CPU:-$(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) cores, $(($(sysctl -n hw.memsize) / 1073741824)) GB}"
	BENCH_HOST_OS="${BENCH_HOST_OS:-macOS $(sw_vers -productVersion)}"
	power="$(pmset -g batt | head -1 | sed "s/.*'\(.*\)'.*/\1/")"
	BENCH_HOST_OS="$BENCH_HOST_OS, $power"
else
	BENCH_HOST_CPU="${BENCH_HOST_CPU:-$(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs), $(nproc) cores}"
	BENCH_HOST_OS="${BENCH_HOST_OS:-$(uname -sr)}"
fi
export BENCH_HOST_CPU BENCH_HOST_OS
export BENCH_HOST="${BENCH_HOST:-$(hostname -s)}"
export BENCH_DOCKER="${BENCH_DOCKER:-$(docker context show) context, $(docker info --format '{{.OperatingSystem}}, server {{.ServerVersion}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes')}"
export BENCH_MIMICRY_COMMIT="${BENCH_MIMICRY_COMMIT:-$(git -C "$here" rev-parse --short HEAD)$(git -C "$here" diff --quiet HEAD -- . ../../ 2>/dev/null || echo ' + uncommitted changes')}"

server_docker=(docker)
if [[ -n "${BENCH_SERVER_CONTEXT:-}" ]]; then
	server_docker=(docker --context "$BENCH_SERVER_CONTEXT")
fi

server_compose() {
	"${server_docker[@]}" compose -f "$compose_dir/compose.server.yml" "$@"
}

client_compose() {
	docker compose -f "$compose_dir/compose.client.yml" "$@"
}

ca_sync() {
	# Only the CA's certificate crosses: the client never needs the keys.
	docker volume create mimicry-bench-ca >/dev/null
	"${server_docker[@]}" run --rm -v mimicry-bench-ca:/data/ca:ro mimicry-bench:latest cat /data/ca/ca.pem |
		docker run --rm -i -v mimicry-bench-ca:/data/ca mimicry-bench:latest sh -c 'cat > /data/ca/ca.pem'
	echo "bench: copied ca.pem from the server host"
}

up() {
	if [[ -n "${BENCH_SERVER:-}" ]]; then
		export BENCH_PUBLISH_ADDR="${BENCH_PUBLISH_ADDR:-0.0.0.0}"
	fi

	# BENCH_SKIP_BUILD=1 reuses the images a previous run left behind (the
	# servers are unaffected by client changes; the client is not, so skip it
	# only when the client code is unchanged too).
	if [[ -z "${BENCH_SKIP_BUILD:-}" ]]; then
		server_compose build
	fi
	server_compose run --rm setup
	server_compose up -d --remove-orphans nginx go-server proxy

	if [[ -n "${BENCH_SERVER:-}" ]]; then
		# The client's own network on this host, then the CA.
		docker network inspect mimicry-bench >/dev/null 2>&1 ||
			docker network create --subnet 172.30.77.0/24 mimicry-bench >/dev/null
		client_compose build
		ca_sync
	fi

	# On the client's Docker host: turns its veth's offloads off under netem.
	client_compose up -d offloads
	client_compose run --rm client bench wait
}

micro() {
	mkdir -p "$reports"
	local raw="micro-$date.txt"
	local stat="micro-$date-benchstat.txt"
	local test="bench.test -test.run '^\$' -test.count ${BENCH_COUNT:-10} -test.timeout 0 -test.v"
	local runs

	if [[ -n "${BENCH_MICRO:-}" ]]; then
		runs="$test -test.bench '$BENCH_MICRO' -test.benchtime ${BENCH_BENCHTIME:-1s} $*"
	else
		runs="$test -test.bench '^BenchmarkRequest\$' -test.benchtime ${BENCH_BENCHTIME:-1s} $*
			$test -test.bench '^BenchmarkConnect\$' -test.benchtime ${BENCH_CONNECT_BENCHTIME:-200x} $*"
	fi

	client_compose run --rm client sh -c "{ $runs; } 2>&1 | tee $raw; benchstat -col /client $raw > $stat"
	echo "bench: wrote $reports/$raw and $reports/$stat"
}

report() {
	local stat="micro-$date-benchstat.txt"
	local args=()

	if [[ -f "$reports/$stat" ]]; then
		args=(-micro "$stat" -micro-raw "micro-$date.txt")
	fi

	# In fast mode, the shorter load settings, unless the caller set the flag
	# itself (any flag, not just no flags: -date used to switch them off).
	if [[ -n "${BENCH_FAST:-}" ]]; then
		local given=" $* "
		[[ "$given" == *" -window "* ]] || args+=(-window 3s)
		[[ "$given" == *" -warmup "* ]] || args+=(-warmup 1s)
		[[ "$given" == *" -conc "* ]] || args+=(-conc 1,64,256)
		[[ "$given" == *" -idle-n "* ]] || args+=(-idle-n 20)
		[[ "$given" == *" -reps "* ]] || args+=(-reps 3)
		[[ "$given" == *" -multi-n "* ]] || args+=(-multi-n 100)
	fi

	client_compose run --rm client bench run -out /reports -date "$date" ${args[@]+"${args[@]}"} "$@"
	echo "bench: wrote $reports/<name>-$date.md and .json (<name>: -name, bench by default)"
}

render() {
	client_compose run --rm client bench render -json "bench-$date.json" \
		-micro "micro-$date-benchstat.txt" -micro-raw "micro-$date.txt"
	echo "bench: rewrote $reports/bench-$date.md"
}

case "$cmd" in
up)
	up
	;;
micro)
	up
	micro "$@"
	;;
run)
	up
	micro
	report "$@"
	;;
report)
	up
	report "$@"
	;;
render)
	render
	;;
ca-sync)
	ca_sync
	;;
down)
	server_compose down
	client_compose down
	;;
*)
	sed -n '2,33p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
