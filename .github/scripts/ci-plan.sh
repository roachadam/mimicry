#!/usr/bin/env bash
# Plans a run of .github/workflows/ci.yml (its changes job): which jobs run,
# the harness tiers and their shards, and how much of the SDK workflows.
#
#   ci-plan.sh full         every job and tier: the nightly run, a run by
#                           hand, a release, and a push to main that
#                           ci-push-proof.sh cannot tie to a passing pull
#                           request
#   ci-plan.sh pr <paths    a pull request, its changed paths on stdin
#   ci-plan.sh push <paths  a push to main that squash-merged a pull request
#                           whose ci-ok passed on the same tree
#                           (ci-push-proof.sh), the commit's changed paths
#                           on stdin: what a full run of those paths' owners
#                           adds to the pull request's run, which planned
#                           the same paths (the main tier, the tiers the
#                           pull request left out, the SDKs' macOS jobs)
#
# It prints key=value lines for $GITHUB_OUTPUT, and on stderr the owner of
# each changed path, so a job's absence can be traced to a rule.
#
#   jobs    JSON array of ci.yml's jobs to run (ci-ok passes only when
#           these succeed and the others are skipped)
#   tiers   the harness tiers (.github/harness-shards.txt), comma-separated
#   shards  JSON array 1..N, the harness shards
#   node    the Node SDK's jobs (node.yml): all, harness, macos or none
#   python  the Python SDK's jobs (python.yml): all, harness, macos or none
#
# A pull request runs what its changed paths can affect, by owner:
#
#   docs     *.md, docs/, bench reports: nothing
#   root     the library module (Go files and directories at the root,
#            internal/ but the harness and the bench): lint, vuln,
#            test-root, the harness, both SDKs. Everything depends on it.
#   harness  internal/harness/: lint, vuln, the harness, and the SDKs' jobs
#            that run the harness (node: harness; python: harness)
#   bench    internal/bench/: lint, vuln, test-root (it builds the bench)
#   node     bindings/node/: the Node SDK's jobs
#   python   bindings/python/: the Python SDK's jobs
#   other    anything else (workflows, .moon, .prototools, .golangci.yml,
#            bindings/build-core.sh, a new top-level path): every job, so a
#            path no rule names never narrows a run
#
# With CI_SELECTION=full (the repository variable) a changed path other
# than docs counts as other, and a push plans a full run: every job, as
# before owners.
#
# The harness tiers: core whenever the harness runs; protocol and gen when
# a changed path is theirs (PROTOCOL_PATHS, GEN_PATHS); main only after
# the merge. Shards: core 2, protocol 3 more, gen 1 more; a push's main 4;
# a full run's every tier, 8 (bash .github/scripts/harness-shard.sh
# <tiers> plan <shards> prints a split).
set -euo pipefail

usage() {
	echo "usage: $0 full | pr <paths | push <paths" >&2
	exit 2
}

[ $# -eq 1 ] || usage
mode=$1
if [ "$mode" = push ] && [ "${CI_SELECTION:-}" = full ]; then
	mode=full
fi

# The HTTP/2, HTTP/3, QUIC and engine code, what the protocol tier's tests
# replay it with (the scenario server, the trace decoders, the profiles),
# and the Go and module versions.
PROTOCOL_PATHS='^(internal/(h2|h3|quic|qpack|hpack|engine|pool|dial|handshake|fingerprint|proxy)/|profile/|profiles/data/|internal/harness/e2e/|internal/harness/internal/(server|h2trace|quictrace)/|(internal/harness/)?go\.(mod|sum)$|go\.work(\.sum)?$|\.prototools$)'
# The generators, what they read (captures, capture parsing) and what they
# must reproduce (the profiles).
GEN_PATHS='^(internal/harness/cmd/(hellogen|h3gen|kindsgen)/|internal/harness/internal/capture/|internal/harness/testdata/captures/|profiles/data/|profile/)'

lint=false root=false vuln=false zizmor=false harness=false
node=none python=none
protocol=false gen=false

# scope a b: the wider of two SDK scopes
wider() {
	case "$1 $2" in
	all\ * | *\ all) echo all ;;
	harness\ * | *\ harness) echo harness ;;
	*) echo none ;;
	esac
}

everything() {
	lint=true root=true vuln=true zizmor=true harness=true
	node=all python=all
}

case $mode in
full)
	everything
	protocol=true gen=true
	echo "full run: every job, every tier" >&2
	;;
pr | push)
	while IFS= read -r path || [ -n "$path" ]; do
		[ -n "$path" ] || continue
		if [[ $path =~ \.md$|^docs/|^internal/bench/reports/ ]]; then
			owner=docs
		elif [ "${CI_SELECTION:-}" = full ]; then
			owner=other
		elif [[ $path == bindings/node/* ]]; then
			owner=node
		elif [[ $path == bindings/python/* ]]; then
			owner=python
		elif [[ $path == internal/harness/* ]]; then
			owner=harness
		elif [[ $path == internal/bench/* ]]; then
			owner=bench
		elif [[ $path =~ ^[^/]+\.go$|^go\.(mod|sum)$|^(cmd|cookies|internal|profile|profiles|proto)/ ]]; then
			owner=root
		else
			owner=other
		fi
		echo "$owner: $path" >&2
		case $owner in
		docs) continue ;;
		root)
			lint=true root=true vuln=true harness=true
			node=all python=all
			;;
		harness)
			lint=true vuln=true harness=true
			node=$(wider "$node" harness)
			python=$(wider "$python" harness)
			;;
		bench) lint=true root=true vuln=true ;;
		node) node=all ;;
		python) python=all ;;
		other) everything ;;
		esac
		[[ $path =~ $PROTOCOL_PATHS ]] && protocol=true
		[[ $path =~ $GEN_PATHS ]] && gen=true
	done
	;;
*) usage ;;
esac

tiers='' shards=0
if $harness; then
	case $mode in
	full) tiers=core,protocol,gen,main shards=8 ;;
	pr)
		tiers=core shards=2
		if $protocol; then
			tiers+=,protocol shards=$((shards + 3))
		fi
		if $gen; then
			tiers+=,gen shards=$((shards + 1))
		fi
		;;
	push)
		# the tiers the pull request left out, then main
		if ! $protocol; then
			tiers+=,protocol shards=$((shards + 3))
		fi
		if ! $gen; then
			tiers+=,gen shards=$((shards + 1))
		fi
		tiers+=,main shards=$((shards + 4))
		tiers=${tiers#,}
		;;
	esac
fi

# A push adds no lint, test-root, vuln or zizmor (the pull request ran
# them on this tree), and of the SDKs only the macOS jobs pull requests
# leave out.
if [ "$mode" = push ]; then
	lint=false root=false vuln=false zizmor=false
	if [ "$node" = all ]; then node=macos; else node=none; fi
	if [ "$python" = all ]; then python=macos; else python=none; fi
fi

jobs=()
$lint && jobs+=(lint)
$root && jobs+=(test-root)
$vuln && jobs+=(vuln)
$zizmor && jobs+=(zizmor)
[ -n "$tiers" ] && jobs+=(test-harness)
[ "$node" != none ] && jobs+=(test-node)
[ "$python" != none ] && jobs+=(test-python)

json() {
	local out='' item
	for item in "$@"; do
		out+=${out:+,}\"$item\"
	done
	echo "[$out]"
}

echo "jobs: ${jobs[*]:-none}; harness tiers: ${tiers:-none} in $shards shards; node: $node; python: $python" >&2
echo "jobs=$(json "${jobs[@]+"${jobs[@]}"}")"
list=
for ((i = 1; i <= shards; i++)); do
	list+=${list:+,}$i
done
echo "shards=[$list]"
echo "tiers=$tiers"
echo "node=$node"
echo "python=$python"
