#!/usr/bin/env bash
# Drives the benchmark's two compose stacks (../docker). Commands:
#
#   up       build the image, create the CA and content, start the server stack
#            and the client host's offloads sidecar
#   micro    layers 1-2: $BENCH_COUNT (10) passes of go test -bench -count 1,
#            the clients in another order each pass, then benchstat
#   run      restart the server stack, micro, then layers 3-6 in $BENCH_PASSES
#            (3) passes and $BENCH_UDP_STOCK_PASSES (1) HTTP/3 passes at stock
#            UDP buffers, then the merged report
#   report   layers 3-6 only (one pass unless BENCH_PASSES says otherwise),
#            embedding the newest micro results
#   render   rewrite bench-<date>.md from its JSON with the newest micro results
#   ca-sync  copy the CA from a remote server stack to this host's volume
#   down     stop the server stack and the offloads sidecar
#
# Arguments after the command go to `bench run` (run, report) or to the test
# binary (micro); give -clients and -name through BENCH_CLIENTS and
# BENCH_NAME, which the passes vary. Settings, all optional:
#
#   BENCH_SERVER=<ip>              the server stack runs on another host at <ip>
#   BENCH_SERVER_CONTEXT=<ctx>     ... reached through this docker context
#                                  (or DOCKER_HOST=ssh://… for the server steps)
#   BENCH_CLIENT_CPUS, BENCH_SERVER_CPUS, BENCH_PROXY_CPUS   cpusets (0-3, 4-11, 10-11)
#   BENCH_CLIENT_PROCS, BENCH_SERVER_PROCS                    GOMAXPROCS (4, 8)
#   BENCH_PROXY_DELAY              netem delay on the proxy's packets (5ms)
#   BENCH_CLIENTS                  clients, in the first pass's order (all)
#   BENCH_NAME                     the report's name before the date (bench)
#   BENCH_PASSES                   layers 3-6: passes, each an invocation of its
#                                  own, the clients' order A, reversed, rotated
#                                  (run: 3; report: 1); layers 4 and 5 run in
#                                  the first only
#   BENCH_UDP_STOCK_PASSES         run: HTTP/3 passes (layers 3 and 6) with the
#                                  client's net.core.rmem_max and rmem_default
#                                  at stock Linux's 212992 and no NET_ADMIN
#                                  (1; 0: none; see below)
#   BENCH_UDP_STOCK=1              report: the whole report at stock UDP buffers
#                                  (layers 3 and 6 only: no netem, see below)
#   BENCH_COUNT                    micro: passes of -count 1 (10)
#   BENCH_BENCHTIME                micro: BenchmarkRequest's and
#                                  BenchmarkNewClient's -benchtime (1s)
#   BENCH_CONNECT_BENCHTIME        micro: BenchmarkConnect's -benchtime (200x: each
#                                  iteration also waits 5 ms untimed for its ticket)
#   BENCH_MICRO                    micro: one -bench pattern instead of all three
#   BENCH_SERVERS                  micro: servers (nginx,go)
#   BENCH_FAST=1                   shorter settings for a quick look (see the
#                                  comment below); the report records them
#   BENCH_SKIP_BUILD=1             reuse the images a previous run built
#   BENCH_ALLOW_BATTERY=1          measure on battery or in Low Power Mode
#                                  (smoke runs only: the report says so)
#
# NET_ADMIN and UDP buffers: the client container has NET_ADMIN for netem
# (layer 5, -load-netem). quic-go and its forks (net/http's HTTP/3,
# tls-client, httpcloak, azuretls) ask for a 7 MB UDP receive buffer and,
# when rmem_max cuts it, retry with SO_RCVBUFFORCE, which NET_ADMIN lets
# through: in the normal passes they get 7 MB whatever rmem_max says, and
# mimicry gets rmem_default. The stock passes (docker/compose.client.udpstock.yml)
# set both sysctls to 212992 and drop NET_ADMIN, so every client gets the
# stock 208 KB; without NET_ADMIN there is no netem, so they cannot include
# layer 5 or -load-netem (bench run refuses).
#
# On macOS it keeps the machine awake (caffeinate -i) for its whole run, and
# refuses to measure unless the Mac is on AC power with Low Power Mode off,
# before it does anything else.
set -euo pipefail

if [[ "$(uname -s)" == Darwin && -z "${BENCH_CAFFEINATED:-}" ]]; then
	export BENCH_CAFFEINATED=1
	exec caffeinate -i "$0" "$@"
fi

here="$(cd "$(dirname "$0")/.." && pwd)"
compose_dir="$here/docker"
reports="$here/reports"
date="${BENCH_DATE:-$(date +%F)}"
name="${BENCH_NAME:-bench}"

cmd="${1:-}"
[[ -n "$cmd" ]] && shift

# BENCH_FAST=1 trades precision for time. Micro benchmarks: 4 passes of
# 300 ms each (100 ms does not sample enough iterations at the slowest
# points). Load points: a 3 s window over a 1 s warm-up, both stretched for
# long requests as in a full run, concurrency 1/64/256 (no 16), 10 and 20
# idle clients, 3 page loads per condition (5 under loss), and 100 clients
# per multi-client point (layer 6; 25 for the second count, the proxy
# rotation's window still 30 s, the client rotation's 10 s instead of 20 s).
# One pass unless BENCH_PASSES says otherwise. The report records what was
# used.
if [[ -n "${BENCH_FAST:-}" ]]; then
	: "${BENCH_COUNT:=4}"
	: "${BENCH_BENCHTIME:=300ms}"
	: "${BENCH_CONNECT_BENCHTIME:=50x}"
fi

# What the report records about this host, and whether it may measure.
power="unknown"
if [[ "$(uname -s)" == Darwin ]]; then
	source_power="$(pmset -g batt | head -1 | sed "s/.*'\(.*\)'.*/\1/")"
	low_power="$(pmset -g | awk '/lowpowermode/ { print $2 }')"
	power="$source_power, Low Power Mode $([[ "$low_power" == 1 ]] && echo on || echo off)"
fi

# check_power refuses to measure on battery or in Low Power Mode: both
# throttle the CPU, and runs on battery spread 4-11% per point.
check_power() {
	[[ "$(uname -s)" == Darwin ]] || return 0

	if [[ "$power" == "AC Power, Low Power Mode off" ]]; then
		export BENCH_HOST_POWER="$power"
		return 0
	fi

	if [[ -n "${BENCH_ALLOW_BATTERY:-}" ]]; then
		export BENCH_HOST_POWER="$power (BENCH_ALLOW_BATTERY: not for comparison)"
		echo "bench: measuring on $power (BENCH_ALLOW_BATTERY)" >&2
		return 0
	fi

	echo "bench: refusing to measure on '$power': plug the Mac in and turn Low Power Mode off" >&2
	echo "bench: (pmset -g batt; pmset -g | grep lowpowermode; BENCH_ALLOW_BATTERY=1 for a smoke run)" >&2
	exit 1
}

# The measuring commands check the power before anything else.
case "$cmd" in
micro | run | report) check_power ;;
esac

if [[ "$(uname -s)" == Darwin ]]; then
	BENCH_HOST_CPU="${BENCH_HOST_CPU:-$(sysctl -n machdep.cpu.brand_string), $(sysctl -n hw.ncpu) cores, $(($(sysctl -n hw.memsize) / 1073741824)) GB}"
	BENCH_HOST_OS="${BENCH_HOST_OS:-macOS $(sw_vers -productVersion)}"
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

client_files=(-f "$compose_dir/compose.client.yml")
if [[ -n "${BENCH_UDP_STOCK:-}" ]]; then
	client_files+=(-f "$compose_dir/compose.client.udpstock.yml")
fi

client_compose() {
	docker compose "${client_files[@]}" "$@"
}

ca_sync() {
	# Only the CA's certificate crosses: the client never needs the keys.
	docker volume create mimicry-bench-ca >/dev/null
	"${server_docker[@]}" run --rm -v mimicry-bench-ca:/data/ca:ro mimicry-bench:latest cat /data/ca/ca.pem |
		docker run --rm -i -v mimicry-bench-ca:/data/ca mimicry-bench:latest sh -c 'cat > /data/ca/ca.pem'
	echo "bench: copied ca.pem from the server host"
}

# up starts the stacks; with BENCH_RESTART=1 (run) it recreates the server
# containers, so nginx, the Go server and the proxy start fresh.
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

	local recreate=()
	if [[ -n "${BENCH_RESTART:-}" ]]; then
		recreate=(--force-recreate)
		echo "bench: restarting the server stack"
	fi
	server_compose up -d --remove-orphans ${recreate[@]+"${recreate[@]}"} nginx go-server proxy

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

# clients_base is the first pass's client order: BENCH_CLIENTS, else all.
clients_base() {
	echo "${BENCH_CLIENTS:-net/http,mimicry,tls-client,httpcloak,azuretls}"
}

# order <pass> prints the clients' order for a pass (1-based): A, then
# reversed, then rotated by half, then rotated by one more each pass.
order() {
	local pass="$1"
	local IFS=,
	local -a a
	read -r -a a <<<"$(clients_base)"
	local n=${#a[@]}
	local -a out=()

	case "$pass" in
	1) out=("${a[@]}") ;;
	2) for ((i = n - 1; i >= 0; i--)); do out+=("${a[i]}"); done ;;
	*)
		local by=$(((n + 1) / 2 + pass - 3))
		for ((i = 0; i < n; i++)); do out+=("${a[(i + by) % n]}"); done
		;;
	esac

	echo "${out[*]}"
}

micro() {
	mkdir -p "$reports"
	local raw="micro-$date.txt"
	local stat="micro-$date-benchstat.txt"
	local count="${BENCH_COUNT:-10}"
	local test="bench.test -test.run '^\$' -test.count 1 -test.timeout 0 -test.v"
	local runs

	if [[ -n "${BENCH_MICRO:-}" ]]; then
		runs="$test -test.bench '$BENCH_MICRO' -test.benchtime ${BENCH_BENCHTIME:-1s} $*"
	else
		runs="$test -test.bench '^Benchmark(Request|NewClient)\$' -test.benchtime ${BENCH_BENCHTIME:-1s} $*
			$test -test.bench '^BenchmarkConnect\$' -test.benchtime ${BENCH_CONNECT_BENCHTIME:-200x} $*"
	fi

	# Each pass a process of its own (what one client leaves behind never
	# reaches the next pass), the clients in another order each time.
	: >"$reports/$raw"
	for ((pass = 1; pass <= count; pass++)); do
		echo "bench: micro pass $pass of $count, clients $(order "$pass")"
		BENCH_CLIENTS="$(order "$pass")" client_compose run --rm client sh -c "{ $runs; } 2>&1 | tee -a $raw"
	done

	client_compose run --rm client sh -c "benchstat -col /client $raw | bench microstat > $stat"
	echo "bench: wrote $reports/$raw and $reports/$stat"
}

# fast_args adds BENCH_FAST's settings unless the caller gave the flag.
fast_args() {
	[[ -n "${BENCH_FAST:-}" ]] || return 0

	local given=" $* "
	[[ "$given" == *" -window "* ]] || echo "-window 3s"
	[[ "$given" == *" -warmup "* ]] || echo "-warmup 1s"
	[[ "$given" == *" -conc "* ]] || echo "-conc 1,64,256"
	[[ "$given" == *" -idle-n "* ]] || echo "-idle-n 10,20"
	[[ "$given" == *" -reps "* ]] || echo "-reps 3"
	[[ "$given" == *" -reps-loss "* ]] || echo "-reps-loss 5"
	[[ "$given" == *" -multi-n "* ]] || echo "-multi-n 100"
	[[ "$given" == *" -multi-n2 "* ]] || echo "-multi-n2 25"
	[[ "$given" == *" -multi-client-window "* ]] || echo "-multi-client-window 10s"
}

# layers_of prints the -layers value among args, or the default (3,6 under
# BENCH_UDP_STOCK, which has no netem for layer 5).
layers_of() {
	local prev=""
	for a in "$@"; do
		if [[ "$prev" == "-layers" ]]; then
			echo "$a"
			return
		fi
		prev="$a"
	done
	if [[ -n "${BENCH_UDP_STOCK:-}" ]]; then
		echo "3,6"
	else
		echo "3,4,5,6"
	fi
}

# without_flag prints args without -<flag> and its value, one per line.
without_flag() {
	local flag="$1"
	shift
	local skip=""
	for a in "$@"; do
		if [[ -n "$skip" ]]; then
			skip=""
			continue
		fi
		if [[ "$a" == "$flag" ]]; then
			skip=1
			continue
		fi
		printf '%s\n' "$a"
	done
}

# pass <report name> <clients> <args...> runs one invocation of bench run.
pass() {
	local pname="$1" clients="$2"
	shift 2

	local micro_args=()
	if [[ -f "$reports/micro-$date-benchstat.txt" ]]; then
		micro_args=(-micro "micro-$date-benchstat.txt" -micro-raw "micro-$date.txt")
	fi

	local -a fast
	read -r -a fast <<<"$(fast_args "$@" | tr '\n' ' ')"

	client_compose run --rm client bench run -out /reports -date "$date" -name "$pname" -clients "$clients" \
		${micro_args[@]+"${micro_args[@]}"} ${fast[@]+"${fast[@]}"} "$@"
}

# report runs layers 3-6 in passes, then the stock-buffer passes, then
# merges them when there is more than one.
report() {
	local passes="${BENCH_PASSES:-1}"
	local stock_passes="${BENCH_UDP_STOCK_PASSES:-0}"
	local layers
	layers="$(layers_of "$@")"
	local -a rest=()
	local line
	while IFS= read -r line; do
		rest+=("$line")
	done < <(without_flag -layers "$@")

	if ((passes <= 1 && stock_passes == 0)); then
		pass "$name" "$(clients_base)" -layers "$layers" ${rest[@]+"${rest[@]}"}
		echo "bench: wrote $reports/$name-$date.md and .json"
		return
	fi

	# Layers 3 and 6 in every pass, 4 and 5 in the first only.
	local repeat
	repeat="$(tr ',' '\n' <<<"$layers" | { grep -E '^[36]$' || true; } | paste -sd, -)"

	local jsons=()
	for ((p = 1; p <= passes; p++)); do
		local l="$layers"
		((p > 1)) && l="$repeat"
		[[ -n "$l" ]] || continue

		echo "bench: pass $p of $passes, layers $l, clients $(order "$p")"
		pass "$name-pass$p" "$(order "$p")" -layers "$l" ${rest[@]+"${rest[@]}"}
		jsons+=("$name-pass$p-$date.json")
	done

	# The stock passes' arguments: the rest without -protos (HTTP/3 only).
	local -a srest=()
	while IFS= read -r line; do
		srest+=("$line")
	done < <(without_flag -protos ${rest[@]+"${rest[@]}"})

	local stock=()
	for ((p = 1; p <= stock_passes; p++)); do
		echo "bench: stock UDP buffers pass $p of $stock_passes (HTTP/3, layers 3 and 6), clients $(order "$p")"
		client_files+=(-f "$compose_dir/compose.client.udpstock.yml")
		pass "$name-udpstock-pass$p" "$(order "$p")" -layers "${repeat:-3}" -protos h3 ${srest[@]+"${srest[@]}"}
		client_files=(-f "$compose_dir/compose.client.yml")
		stock+=("$name-udpstock-pass$p-$date.json")
	done

	local stock_arg=()
	if ((${#stock[@]} > 0)); then
		stock_arg=(-udp-stock "$(IFS=,; echo "${stock[*]}")")
	fi

	client_compose run --rm client bench merge -passes -out /reports -name "$name" -date "$date" \
		${stock_arg[@]+"${stock_arg[@]}"} "${jsons[@]}"
	echo "bench: wrote $reports/$name-$date.md and .json from ${jsons[*]} ${stock[*]:-}"
}

render() {
	client_compose run --rm client bench render -json "$name-$date.json" \
		-micro "micro-$date-benchstat.txt" -micro-raw "micro-$date.txt"
	echo "bench: rewrote $reports/$name-$date.md"
}

# estimate prints run's expected duration before it starts. The figures are
# per part at the defaults (BENCH_FAST off, no flags); other flags change
# them, which it says.
estimate() {
	local count="${BENCH_COUNT:-10}" passes="${BENCH_PASSES:-3}" stock="${BENCH_UDP_STOCK_PASSES:-1}"
	local layers
	layers="$(layers_of "$@")"
	local per=0 first=0
	[[ ",$layers," == *",3,"* ]] && per=$((per + 70))
	[[ ",$layers," == *",6,"* ]] && per=$((per + 60))
	[[ ",$layers," == *",4,"* ]] && first=$((first + 5))
	[[ ",$layers," == *",5,"* ]] && first=$((first + 45))
	local min=$((5 + count * 5 / 2 + passes * per + first + stock * 40))
	local note=""
	if [[ -n "${BENCH_FAST:-}" ]] || [[ -n "$(without_flag -layers "$@")" ]]; then
		note=" (for the default settings; BENCH_FAST or the flags given change it)"
	fi
	echo "bench: run: estimated $((min / 60)) h $((min % 60)) min$note: setup ~5 min, micro ~$((count * 5 / 2)) min" \
		"($count passes), layers 3 and 6 in $passes passes (~$per min each), layers 4 and 5 once (~$first min)," \
		"$stock stock-buffer pass(es) (~40 min each)"
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
	estimate "$@"
	BENCH_RESTART=1 up
	micro
	BENCH_PASSES="${BENCH_PASSES:-3}" BENCH_UDP_STOCK_PASSES="${BENCH_UDP_STOCK_PASSES:-1}" report "$@"
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
	sed -n '2,64p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
