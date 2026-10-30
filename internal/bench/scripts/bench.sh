#!/usr/bin/env bash
# Drives the benchmark's two compose stacks (../docker). Commands:
#
#   up       build the image, create the CA and content, start the server stack
#            and the client host's offloads sidecar
#   micro    layers 1-2: $BENCH_COUNT (6) passes of go test -bench -count 1,
#            the clients in another order each pass, then benchstat
#   run      the default run: print the plan and its estimated duration,
#            restart the server stack, micro, then layers 3, 4 and 6 in
#            $BENCH_PASSES (3) passes (layer 4 in the first only), then the
#            merged report (README.md, "Default run"; BENCH_FULL=1 for every
#            layer and the full matrix)
#   plan     what run would measure and how long it should take, without
#            measuring (the same arguments and settings as run)
#   report   layers 3, 4 and 6 only (one pass unless BENCH_PASSES says
#            otherwise), embedding the newest micro results
#   render   rewrite bench-<date>.md from its JSON with the newest micro results
#   ca-sync  copy the CA from a remote server stack to this host's volume
#   down     stop the server stack and the offloads sidecar
#
# Arguments after the command go to `bench run` (run, plan, report) or to
# the test binary (micro); give -clients and -name through BENCH_CLIENTS
# and BENCH_NAME, which the passes vary. Settings, all optional:
#
#   BENCH_SERVER=<ip>              the server stack runs on another host at <ip>
#   BENCH_SERVER_CONTEXT=<ctx>     ... reached through this docker context
#                                  (or DOCKER_HOST=ssh://… for the server steps)
#   BENCH_CLIENT_CPUS, BENCH_SERVER_CPUS, BENCH_PROXY_CPUS   cpusets (0-3, 4-11, 10-11)
#   BENCH_CLIENT_PROCS, BENCH_SERVER_PROCS, BENCH_PROXY_PROCS GOMAXPROCS (4, 8, 2)
#   BENCH_PROXY_DELAY              delay on the proxy's packets (5ms)
#   BENCH_PROXY_DELAY_MODE         how: netem (on the proxy's eth0) or proxy
#                                  (the proxy's in-process delay line); the
#                                  default is netem where the Docker host has
#                                  it, else proxy (see "netem" below)
#   BENCH_PUBLISH=0                publish none of the server stack's ports
#                                  (a shared host: the client reaches the
#                                  servers on the mimicry-bench network only)
#   BENCH_CLIENTS                  clients, in the first pass's order (the five:
#                                  net/http, mimicry, tls-client, httpcloak,
#                                  azuretls; also takes clients.Variants)
#   BENCH_NAME                     the report's name before the date (bench)
#   BENCH_DATE                     the date in the report's name (today); render
#                                  needs it for another day's report
#   BENCH_MIMICRY_COMMIT           the commit the report names (HEAD, plus
#                                  " + uncommitted changes" if the tree differs)
#   BENCH_PASSES                   layers 3-6: passes, each an invocation of its
#                                  own, the clients' order A, reversed, rotated
#                                  (run: 3; report: 1); layers 4 and 5 run in
#                                  the first only
#   BENCH_UDP_STOCK_PASSES         run: HTTP/3 passes (layers 3 and 6) with the
#                                  client's net.core.rmem_max and rmem_default
#                                  at stock Linux's 212992 and no NET_ADMIN
#                                  (0; BENCH_FULL: 1; see below)
#   BENCH_UDP_STOCK=1              report: the whole report at stock UDP buffers
#                                  (layers 3 and 6 only: no netem, see below)
#   BENCH_COUNT                    micro: passes of -count 1 (6)
#   BENCH_HOST_LABEL               the report's name for this machine (default:
#                                  the OS and CPU model, e.g. "Linux host
#                                  (i5-13500)"; never the hostname)
#   BENCH_BENCHTIME                micro: BenchmarkRequest's and
#                                  BenchmarkNewClient's -benchtime (1s)
#   BENCH_CONNECT_BENCHTIME        micro: BenchmarkConnect's -benchtime (200x: each
#                                  iteration also waits 5 ms untimed for its ticket)
#   BENCH_MICRO                    micro: one -bench pattern instead of all three
#   BENCH_SERVERS                  micro: servers (nginx; BENCH_FULL: nginx,go)
#   BENCH_FULL=1                   the full matrix of the runs before the trim:
#                                  every layer, both servers, bodies up to 10 MB,
#                                  concurrency 1/16/64/256, 10 s windows, 200
#                                  clients per layer 6 point, proxy rotation on
#                                  every route, the stock-buffer pass, 10 micro
#                                  passes (see full_args below)
#   BENCH_FAST=1                   a quick smoke run, not for comparison: one
#                                  pass, shorter windows (see below); the report
#                                  records them
#   BENCH_SKIP_BUILD=1             reuse the images a previous run built
#   BENCH_ALLOW_BATTERY=1          measure on battery or in Low Power Mode
#                                  (smoke runs only: the report says so)
#
# NET_ADMIN and UDP buffers: the client container has NET_ADMIN for netem
# (layer 5, -load-netem). quic-go and its forks (net/http's HTTP/3,
# tls-client, httpcloak, azuretls) ask for a 7 MB UDP receive buffer and,
# when rmem_max cuts it, retry with SO_RCVBUFFORCE, which NET_ADMIN lets
# through: in the normal passes they get 7 MB whatever rmem_max says, and
# mimicry, which asks for 1 MB (Config.HTTP3ReadBufferSize, decision 0045),
# gets what rmem_max allows of it. The stock passes
# (docker/compose.client.udpstock.yml) set both sysctls to 212992 and drop
# NET_ADMIN, so every client that asks gets twice that, 416 KB; without
# NET_ADMIN there is no netem, so they cannot include layer 5 or
# -load-netem (bench run refuses).
#
# netem: run and report probe the Docker host once (a throwaway container
# adding a netem qdisc on its own loopback). Without it (a kernel without
# sch_netem), the proxy's delay is the in-process one, the
# offloads sidecar (only for netem, on the host's network) is not started,
# and a run asking for netem (layer 5 without -netem=false, -load-netem)
# stops before measuring.
#
# On Linux it records the host's load before each pass and at the end: the
# load average and how many containers not the benchmark's use 1% CPU or
# more, with their total (docker stats; no names), into the report's
# Environment (the pass's start) and reports/host-<date>.txt (all of them).
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

# The default run is trimmed to the workload it is for (README.md, "Default
# run"): many clients, one proxy each, small requests. Its settings are
# bench run's flag defaults, plus these for micro.
#
# BENCH_FULL=1 restores the full matrix the runs before the trim used, in
# one switch: 10 micro passes on both servers, the stock-buffer pass, and
# full_args's flags (every layer, both servers, every body, concurrency
# 1/16/64/256, 10 s windows over 3 s warm-ups, 200 and 50 clients per layer
# 6 point with both rotations' second count, proxy rotation on every route
# and body, a 20 s client-rotation window).
#
# BENCH_FAST=1 is a quick smoke run, not for comparison: one pass, no
# stock-buffer pass, 2 micro passes of 300 ms each (100 ms does not sample
# enough iterations at the slowest points), and fast_args's flags (a 3 s
# window over a 1 s warm-up, both still stretched for long requests, 10 and
# 20 idle clients, 3 page loads per condition, 5 under loss). The report
# records what was used.
if [[ -n "${BENCH_FULL:-}" && -n "${BENCH_FAST:-}" ]]; then
	echo "bench: BENCH_FULL and BENCH_FAST exclude each other" >&2
	exit 2
fi

if [[ -n "${BENCH_FULL:-}" ]]; then
	: "${BENCH_COUNT:=10}"
	: "${BENCH_SERVERS:=nginx,go}"
	: "${BENCH_UDP_STOCK_PASSES:=1}"
fi

if [[ -n "${BENCH_FAST:-}" ]]; then
	: "${BENCH_COUNT:=2}"
	: "${BENCH_BENCHTIME:=300ms}"
	: "${BENCH_CONNECT_BENCHTIME:=50x}"
	: "${BENCH_PASSES:=1}"
fi

# Micro's servers: nginx, as layers 3 and 4 (bench_test.go reads it).
export BENCH_SERVERS="${BENCH_SERVERS:-nginx}"

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

# The report names the machine by BENCH_HOST_LABEL, never its hostname, and
# leaves out the kernel's local version suffix and, on Linux, the Docker
# host's distribution name.
if [[ "$(uname -s)" == Darwin ]]; then
	cpu_model="$(sysctl -n machdep.cpu.brand_string)"
	BENCH_HOST_CPU="${BENCH_HOST_CPU:-$cpu_model, $(sysctl -n hw.ncpu) cores, $(($(sysctl -n hw.memsize) / 1073741824)) GB}"
	BENCH_HOST_OS="${BENCH_HOST_OS:-macOS $(sw_vers -productVersion)}"
	BENCH_HOST_LABEL="${BENCH_HOST_LABEL:-Mac ($cpu_model)}"
	docker_os='{{.OperatingSystem}}'
else
	cpu_model="$(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs)"
	BENCH_HOST_CPU="${BENCH_HOST_CPU:-$cpu_model, $(nproc) cores}"
	BENCH_HOST_OS="${BENCH_HOST_OS:-$(uname -s) $(uname -r | cut -d- -f1)}"
	BENCH_HOST_LABEL="${BENCH_HOST_LABEL:-Linux host ($(sed -E 's/\((R|TM)\)//g; s/[0-9]+th Gen //; s/ [0-9]+-Core Processor$//; s/  +/ /g' <<<"$cpu_model"))}"
	docker_os='{{.OSType}}/{{.Architecture}}'
fi
export BENCH_HOST_CPU BENCH_HOST_OS BENCH_HOST_LABEL
export BENCH_DOCKER="${BENCH_DOCKER:-$(docker info --format "$docker_os, server {{.ServerVersion}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes")}"
export BENCH_MIMICRY_COMMIT="${BENCH_MIMICRY_COMMIT:-$(git -C "$here" rev-parse --short HEAD)$(git -C "$here" diff --quiet HEAD -- . ../../ 2>/dev/null || echo ' + uncommitted changes')}"

server_docker=(docker)
if [[ -n "${BENCH_SERVER_CONTEXT:-}" ]]; then
	server_docker=(docker --context "$BENCH_SERVER_CONTEXT")
fi

server_files=(-f "$compose_dir/compose.server.yml")
if [[ "${BENCH_PUBLISH:-1}" == 0 ]]; then
	server_files+=(-f "$compose_dir/compose.server.nopublish.yml")
fi

server_compose() {
	"${server_docker[@]}" compose "${server_files[@]}" "$@"
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

# build builds the images: the server stack's (which includes the client's
# locally), and the client's here when the servers run elsewhere.
# BENCH_SKIP_BUILD=1 reuses the images a previous run left behind (the
# servers are unaffected by client changes; the client is not, so skip it
# only when the client code is unchanged too).
build() {
	[[ -z "${BENCH_SKIP_BUILD:-}" ]] || return 0

	server_compose build
	if [[ -n "${BENCH_SERVER:-}" ]]; then
		client_compose build
	fi
}

# up starts the stacks; with BENCH_RESTART=1 (run) it recreates the server
# containers, so nginx, the Go server and the proxy start fresh.
up() {
	if [[ -n "${BENCH_SERVER:-}" ]]; then
		export BENCH_PUBLISH_ADDR="${BENCH_PUBLISH_ADDR:-0.0.0.0}"
	fi

	build
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
		ca_sync
	fi

	# On the client's Docker host: turns its veth's offloads off under netem.
	if [[ "${BENCH_NETEM:-1}" == 1 ]]; then
		client_compose up -d offloads
	fi
	client_compose run --rm client bench wait
}

# probe_netem sets BENCH_NETEM (1 if a container on the client's Docker
# host can add a netem qdisc, else 0) and BENCH_PROXY_DELAY_MODE (netem,
# else proxy) unless given. On the client's host: with BENCH_SERVER the
# proxy's host may differ, so there it only defaults the mode to netem.
probe_netem() {
	if [[ -z "${BENCH_NETEM:-}" ]]; then
		if docker run --rm --name mimicry-bench-netem-probe --network none --cap-add NET_ADMIN mimicry-bench:latest \
			tc qdisc add dev lo root netem delay 1ms >/dev/null 2>&1; then
			BENCH_NETEM=1
		else
			BENCH_NETEM=0
		fi
	fi
	export BENCH_NETEM

	local mode=netem
	[[ "$BENCH_NETEM" == 1 || -n "${BENCH_SERVER:-}" ]] || mode=proxy
	export BENCH_PROXY_DELAY_MODE="${BENCH_PROXY_DELAY_MODE:-$mode}"
	echo "bench: netem $([[ "$BENCH_NETEM" == 1 ]] && echo available || echo unavailable) on this Docker host; the proxy's delay: $BENCH_PROXY_DELAY_MODE"

	if [[ "$BENCH_NETEM" == 0 && "$BENCH_PROXY_DELAY_MODE" == netem && -z "${BENCH_SERVER:-}" ]]; then
		echo "bench: BENCH_PROXY_DELAY_MODE=netem, but this Docker host has no netem: use proxy" >&2
		exit 2
	fi
}

# needs_netem <args...> succeeds when the run asks for netem: layer 5
# without -netem=false, or -load-netem.
needs_netem() {
	local layers
	layers="$(layers_of "$@")"
	if grep -qE '(^|[ ])-?-load-netem([ =]|$)' <<<"$*"; then
		return 0
	fi
	grep -qE '(^|,)5(,|$)' <<<"$layers" || return 1
	! grep -qE '(^|[ ])-?-netem(=false| false)([ ]|$)' <<<"$*"
}

# check_netem stops a run that needs netem on a host without it.
check_netem() {
	[[ "$BENCH_NETEM" == 0 ]] || return 0
	if needs_netem "$@"; then
		echo "bench: this run needs netem (layer 5, or -load-netem), which this Docker host lacks (no sch_netem):" >&2
		echo "bench: drop layer 5 (the default run is 3,4,6), give -netem=false, or run on a host with netem" >&2
		exit 2
	fi
}

# host_load prints a Linux host's load average and how many containers that
# are not the benchmark's use 1% CPU or more, with their total CPU (docker
# stats, one sample; never their names); nothing on macOS, whose Docker
# runs in a VM.
host_load() {
	[[ "$(uname -s)" == Linux ]] || return 0
	local others
	others="$(docker stats --no-stream --format '{{.Name}} {{.CPUPerc}}' 2>/dev/null |
		awk '$1 !~ /^mimicry-bench/ { c = $2; sub(/%/, "", c); if (c + 0 >= 1) { n++; t += c } }
			END { if (n) printf "%d, %.2f%% CPU together", n, t }')"
	echo "load average $(cut -d' ' -f1-3 /proc/loadavg); other containers at 1% CPU or more: ${others:-none}"
}

# note_load <when> records host_load in reports/host-<date>.txt and, for
# the next pass's report, BENCH_HOST_LOAD.
note_load() {
	[[ "$(uname -s)" == Linux ]] || return 0
	local l
	l="$(host_load)"
	mkdir -p "$reports"
	echo "$(date -u +%FT%TZ) $1: $l" >>"$reports/host-$date.txt"
	echo "bench: host $1: $l"
	export BENCH_HOST_LOAD="$l"
}

# clients_base is the first pass's client order: BENCH_CLIENTS, else the
# five (bench run's default; clients.Variants only when named).
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
	note_load "before micro"
	local raw="micro-$date.txt"
	local stat="micro-$date-benchstat.txt"
	local count="${BENCH_COUNT:-6}"
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
	echo "bench: wrote reports/$raw and reports/$stat"
}

# preset_args prints BENCH_FULL's or BENCH_FAST's flags for bench run, one
# word per line, except those the caller gave.
preset_args() {
	local given=" $* "
	local -a preset=()

	if [[ -n "${BENCH_FULL:-}" ]]; then
		local layers="3,4,5,6"
		[[ -z "${BENCH_UDP_STOCK:-}" ]] || layers="3,6"
		preset=(-layers "$layers" -window 10s -warmup 3s -servers nginx,go -bodies all -conc 1,16,64,256
			-multi-n 200 -multi-n2 50 -multi-n2-variants fixed,proxy -multi-proxy-routes all -multi-proxy-bodies all
			-multi-client-window 20s)
	elif [[ -n "${BENCH_FAST:-}" ]]; then
		preset=(-window 3s -warmup 1s -idle-n 10,20 -reps 3 -reps-loss 5)
	fi

	local i
	for ((i = 0; i < ${#preset[@]}; i += 2)); do
		local f="${preset[i]}"
		[[ "$given" == *" $f "* || "$given" == *" $f="* ]] || printf '%s\n%s\n' "$f" "${preset[i + 1]}"
	done
}

# with_presets <args...> prints args after preset_args's, one per line.
with_presets() {
	preset_args "$@"
	[[ $# -eq 0 ]] || printf '%s\n' "$@"
}

# layers_of prints the -layers value among args, or bench run's default
# (3,4,6; 3,6 under BENCH_UDP_STOCK, which has no netem for layer 5).
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
		echo "3,4,6"
	fi
}

# repeat_of <layers> prints the layers every pass runs: 3 and 6 (4 and 5 run
# in the first only).
repeat_of() {
	tr ',' '\n' <<<"$1" | { grep -E '^[36]$' || true; } | paste -sd, -
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

	note_load "before $pname"
	client_compose run --rm client bench run -out /reports -date "$date" -name "$pname" -clients "$clients" \
		${micro_args[@]+"${micro_args[@]}"} "$@"
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
		note_load "end"
		echo "bench: wrote reports/$name-$date.md and .json"
		return
	fi

	# Layers 3 and 6 in every pass, 4 and 5 in the first only.
	local repeat
	repeat="$(repeat_of "$layers")"

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

	note_load "end"
	client_compose run --rm client bench merge -passes -out /reports -name "$name" -date "$date" \
		${stock_arg[@]+"${stock_arg[@]}"} "${jsons[@]}"
	echo "bench: wrote reports/$name-$date.md and .json from ${jsons[*]} ${stock[*]:-}"
}

render() {
	client_compose run --rm client bench render -json "$name-$date.json" \
		-micro "micro-$date-benchstat.txt" -micro-raw "micro-$date.txt"
	echo "bench: rewrote reports/$name-$date.md"
}

# plan_of <args...> prints bench run -plan's listing for one pass: its jobs
# by layer and group, each estimated, and the total, from the client
# image's own matrix (cmd/bench/plan.go; no network, no CA needed).
plan_of() {
	docker run --rm -e BENCH_SERVER="${BENCH_SERVER:-}" mimicry-bench:latest \
		bench run -plan -clients "$(clients_base)" "$@"
}

# seconds_of <plan> prints a plan's estimate in seconds, from its last line.
seconds_of() {
	sed -n 's/^plan: .*(\([0-9]*\) s)$/\1/p' <<<"$1"
}

# duration_ms <d> prints a -benchtime duration (1s, 1.5s, 300ms) in ms.
duration_ms() {
	case "$1" in
	*ms) echo "${1%ms}" ;;
	*s) awk -v s="${1%s}" 'BEGIN { printf "%d\n", s * 1000 }' ;;
	*) echo 1000 ;;
	esac
}

# micro_seconds <passes> prints micro's estimate in seconds. Per pass:
# servers × 3 protocols × 2 methods × clients BenchmarkRequest rows and as
# many BenchmarkConnect rows (full and resumed), and 3 protocols × 2
# proxies × clients BenchmarkNewClient rows; a Request or NewClient row
# takes its -benchtime plus a fifth and 0.1 s (Go's calibration runs, the
# client's setup), a Connect row 11 ms per iteration (a handshake and the
# 5 ms ticket wait) plus 0.3 s; and 5 s for the pass's container. The
# 2026-10-27 micro log (10 passes on both servers) took about 1.3 s per
# 1 s row.
micro_seconds() {
	local count="$1" k s bt_ms conn_ms
	k="$(tr ',' '\n' <<<"$(clients_base)" | grep -c .)"
	s="$(tr ',' '\n' <<<"$BENCH_SERVERS" | grep -c .)"
	bt_ms="$(duration_ms "${BENCH_BENCHTIME:-1s}")"

	local cb="${BENCH_CONNECT_BENCHTIME:-200x}"
	if [[ "$cb" == *x ]]; then
		conn_ms=$((${cb%x} * 11 + 300))
	else
		conn_ms=$(($(duration_ms "$cb") * 12 / 10 + 300))
	fi

	local req_ms=$((bt_ms * 12 / 10 + 100))
	local per_ms=$((s * 6 * k * req_ms + s * 6 * k * conn_ms + 6 * k * req_ms + 5000))
	echo $((count * per_ms / 1000))
}

# minutes <seconds> prints them as whole minutes.
minutes() {
	echo "$((($1 + 30) / 60)) min"
}

# plan <passes> <stock passes> <args...> prints what run measures and how
# long it should take: pass 1's jobs in full, the other passes' and the
# stock-buffer passes' totals, micro's, and the sum. Per-job arithmetic, not
# measured end to end: bench run -plan for layers 3-6 (cmd/bench/plan.go:
# each point's warm-up and window, stretched as scenario.LoadSpec does,
# page loads at their RTT and loss, 0.5 s per worker, calibrated on a run's
# log), micro_seconds for micro, 3 min of setup (cached build, setup,
# server restart, wait) and 15 s per container that runs bench run or
# bench merge.
plan() {
	local passes="$1" stock="$2"
	shift 2

	local layers repeat
	layers="$(layers_of "$@")"
	repeat="$(repeat_of "$layers")"

	local -a rest=() srest=()
	local line
	while IFS= read -r line; do
		rest+=("$line")
	done < <(without_flag -layers "$@")
	while IFS= read -r line; do
		srest+=("$line")
	done < <(without_flag -protos ${rest[@]+"${rest[@]}"})

	local out first other=0 stocks=0
	echo "bench: plan: pass 1 of $passes, layers $layers"
	out="$(plan_of -layers "$layers" ${rest[@]+"${rest[@]}"})"
	sed 's/^/  /' <<<"$out"
	first="$(seconds_of "$out")"

	local others=0
	if ((passes > 1)) && [[ -n "$repeat" ]]; then
		others=$((passes - 1))
		out="$(plan_of -layers "$repeat" ${rest[@]+"${rest[@]}"})"
		other="$(seconds_of "$out")"
		local label="passes 2-$passes"
		((passes > 2)) || label="pass 2"
		echo "bench: plan: $label, layers $repeat (the same points, the clients reordered), each: $(sed -n '$s/^plan: //p' <<<"$out")"
	fi

	if ((stock > 0)); then
		out="$(plan_of -layers "${repeat:-3}" -protos h3 ${srest[@]+"${srest[@]}"})"
		stocks="$(seconds_of "$out")"
		echo "bench: plan: $stock stock-buffer pass(es), HTTP/3, layers ${repeat:-3}, each: $(sed -n '$s/^plan: //p' <<<"$out")"
	fi

	local count="${BENCH_COUNT:-6}" micro_s
	micro_s="$(micro_seconds "$count")"
	local micro_note=""
	[[ -z "${BENCH_MICRO:-}" ]] || micro_note=", as if all three benchmarks (BENCH_MICRO runs fewer)"
	echo "bench: plan: micro, $count passes on $BENCH_SERVERS, ~$(minutes "$micro_s")$micro_note"

	local setup=180 container=15
	local containers=$((1 + others + stock + 1))
	local total=$((setup + micro_s + first + others * other + stock * stocks + containers * container))
	echo "bench: run: estimated $((total / 3600)) h $((total % 3600 / 60)) min ($total s): setup ~3 min, micro ~$(minutes "$micro_s")," \
		"pass 1 ~$(minutes "$first"), $others more pass(es) ~$(minutes "$other") each, $stock stock-buffer pass(es)" \
		"~$(minutes "$stocks") each, $containers containers ~15 s each. Per-job arithmetic (bench run -plan, scripts/bench.sh" \
		"micro_seconds), not measured end to end"
}

cmd_args=()
case "$cmd" in
run | plan | report)
	while IFS= read -r line; do
		cmd_args+=("$line")
	done < <(with_presets "$@")
	;;
esac

case "$cmd" in
up)
	build
	probe_netem
	BENCH_SKIP_BUILD=1 up
	;;
micro)
	build
	probe_netem
	BENCH_SKIP_BUILD=1 up
	micro "$@"
	;;
run)
	passes="${BENCH_PASSES:-3}"
	stock="${BENCH_UDP_STOCK_PASSES:-0}"
	build
	probe_netem
	check_netem ${cmd_args[@]+"${cmd_args[@]}"}
	plan "$passes" "$stock" ${cmd_args[@]+"${cmd_args[@]}"}
	BENCH_SKIP_BUILD=1 BENCH_RESTART=1 up
	micro
	BENCH_PASSES="$passes" BENCH_UDP_STOCK_PASSES="$stock" report ${cmd_args[@]+"${cmd_args[@]}"}
	;;
plan)
	build
	plan "${BENCH_PASSES:-3}" "${BENCH_UDP_STOCK_PASSES:-0}" ${cmd_args[@]+"${cmd_args[@]}"}
	;;
report)
	build
	probe_netem
	check_netem ${cmd_args[@]+"${cmd_args[@]}"}
	BENCH_SKIP_BUILD=1 up
	report ${cmd_args[@]+"${cmd_args[@]}"}
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
	# The comment at the top, up to the first line that is not one.
	awk 'NR > 1 && !/^#/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "$0"
	exit 2
	;;
esac
