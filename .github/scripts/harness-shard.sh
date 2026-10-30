#!/usr/bin/env bash
# Splits the harness module's tests (internal/harness) into shards that CI
# runs as parallel jobs, and runs one shard.
#
#   harness-shard.sh <tiers> <shard> <total>   run shard <shard> (1 to <total>)
#   harness-shard.sh <tiers> plan <total>      print every shard's go test commands
#
# <tiers> picks the tests: a comma-separated list of the tiers in
# .github/harness-shards.txt (core, protocol, gen, main), or all. Each
# top-level test (go test -list) is a unit weighed in that file (2 seconds
# and tier core if not listed there). A weight line naming Test/regex makes
# that slice of Test's subtests a unit of its own; the subtests no slice
# matches are one more unit. A test's units all have its tier. A line
# naming TestA|TestB keeps those tests in one unit when the chosen tiers
# include them all (they share a costly setup that whichever runs first
# pays); otherwise each runs as its own unit. The chosen tiers' units are
# dealt heaviest first (ties by name) to the shard with the least work so
# far (ties: the lower shard), so the split depends only on the test list,
# the weights and the tiers, and a new test always lands in some shard. A
# shard runs its units with go test -race -count=1 -v (so the log has each
# test's duration, for the weights) and -timeout $TEST_TIMEOUT (default
# 30m); MIMICRY_PROFILES passes through to the tests.
set -euo pipefail

usage() {
	echo "usage: $0 <tiers> <shard> <total> | <tiers> plan <total>" >&2
	echo "  <tiers>: comma-separated core, protocol, gen, main; or all" >&2
	exit 2
}

[ $# -eq 3 ] || usage
tiers=$1
shard=$2
total=$3
[ "$tiers" = all ] && tiers=core,protocol,gen,main
[[ $tiers =~ ^(core|protocol|gen|main)(,(core|protocol|gen|main))*$ ]] || usage
[[ $total =~ ^[1-9][0-9]*$ ]] || usage
if [ "$shard" != plan ]; then
	[[ $shard =~ ^[1-9][0-9]*$ ]] && [ "$shard" -le "$total" ] || usage
fi

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/internal/harness"
module=$(awk '$1 == "module" { print $2; exit }' go.mod)

# One "package test" line per test, the package as ./dir. go test prints a
# package's names, then its ok line. -race builds what the run reuses.
tests=$(go test -race -list '.*' ./... | awk -v module="$module" '
	/^(ok|FAIL|\?)[ \t]/ {
		pkg = "." substr($2, length(module) + 1)
		for (i = 1; i <= n; i++) {
			print pkg, names[i]
		}
		n = 0
		next
	}
	/^(Test|Example|Fuzz)/ {
		names[++n] = $1
	}
')

# The shard's go test runs, one per line: package, -run, -skip (tabs). The
# plan goes to stderr, so it is in the job's log.
runs=$(echo "$tests" | awk -v shard="$shard" -v total="$total" -v tiers="$tiers" -v weights=".github/harness-shards.txt" '
	function add(w, pkg, test, run, skip) {
		units++
		uw[units] = w + 0
		upkg[units] = pkg
		utest[units] = test
		urun[units] = run
		uskip[units] = skip
	}
	# heavier first; then by package and pattern, for a stable order
	function before(a, b) {
		if (uw[a] != uw[b]) {
			return uw[a] > uw[b]
		}
		return (upkg[a] " " urun[a]) < (upkg[b] " " urun[b])
	}
	# a test has one tier, whichever of its lines names it
	function settier(key, t) {
		if ((key in tier) && tier[key] != t) {
			printf "%s puts %s in tiers %s and %s: give its lines one\n", weights, key, tier[key], t > "/dev/stderr"
			bad = 1
		}
		tier[key] = t
	}
	# whether the test is in a chosen tier
	function chosen(key) {
		return ((key in tier) ? tier[key] : "core") in want
	}
	# whether every member of the group (pkg, TestA|TestB) is chosen
	function wholegroup(pkg, g,    n, members, k) {
		n = split(g, members, "|")
		for (k = 1; k <= n; k++) {
			if (!chosen(pkg " " members[k])) {
				return 0
			}
		}
		return 1
	}
	BEGIN {
		known["core"] = known["protocol"] = known["gen"] = known["main"] = 1
		n = split(tiers, list, ",")
		for (k = 1; k <= n; k++) {
			want[list[k]] = 1
		}
	}
	FNR == NR {
		if ($0 ~ /^[ \t]*(#|$)/) {
			next
		}
		slash = index($4, "/")
		if (!slash && index($4, "|")) {
			# each member has the tier of a line of its own (or core)
			if ($2 != "-") {
				printf "%s: a group has tier -, its members their own: %s\n", weights, $0 > "/dev/stderr"
				bad = 1
			}
			n = split($4, members, "|")
			for (k = 1; k <= n; k++) {
				listed[$3 " " members[k]] = $4
				group[$3 " " members[k]] = $4
			}
			weight[$3 " " $4] = $1
			next
		}
		if (!($2 in known)) {
			printf "%s: unknown tier %s (core, protocol, gen, main): %s\n", weights, $2, $0 > "/dev/stderr"
			bad = 1
			next
		}
		key = $3 " " (slash ? substr($4, 1, slash - 1) : $4)
		listed[key] = $4
		settier(key, $2)
		if (slash) {
			k = ++nslices[key]
			slicew[key, k] = $1
			slicere[key, k] = substr($4, slash + 1)
		} else {
			weight[key] = $1
		}
		next
	}
	{
		key = $1 " " $2
		found[key] = 1
		if (!chosen(key)) {
			next
		}
		if ((key in group) && wholegroup($1, group[key])) {
			# the group is one unit, added with its first member
			if (!(($1 " " group[key]) in added)) {
				added[$1 " " group[key]] = 1
				add(weight[$1 " " group[key]], $1, group[key], "", "")
			}
			next
		}
		w = (key in weight) ? weight[key] : 2
		if (!nslices[key]) {
			add(w, $1, $2, "", "")
			next
		}
		skip = ""
		for (k = 1; k <= nslices[key]; k++) {
			add(slicew[key, k], $1, "", "^" $2 "$/" slicere[key, k], "")
			skip = skip (k > 1 ? "|" : "") slicere[key, k]
		}
		add(w, $1, "", "^" $2 "$", "^" $2 "$/(" skip ")")
	}
	END {
		for (key in listed) {
			if (!(key in found)) {
				split(key, f, " ")
				printf "%s lists %s in %s, which go test -list does not show: update it\n", weights, listed[key], f[1] > "/dev/stderr"
				bad = 1
			}
		}
		if (bad) {
			exit 1
		}
		for (i = 1; i <= units; i++) {
			order[i] = i
		}
		for (i = 2; i <= units; i++) {
			for (j = i; j > 1 && before(order[j], order[j - 1]); j--) {
				t = order[j]
				order[j] = order[j - 1]
				order[j - 1] = t
			}
		}
		for (i = 1; i <= units; i++) {
			u = order[i]
			s = 1
			for (j = 2; j <= total; j++) {
				if (load[j] < load[s]) {
					s = j
				}
			}
			load[s] += uw[u]
			ushard[u] = s
		}
		for (s = 1; s <= total; s++) {
			if (shard != "plan" && s != shard) {
				continue
			}
			printf "# shard %d/%d (%s): about %d s of tests\n", s, total, tiers, load[s] > "/dev/stderr"
			# whole tests of a package go in one go test run; each slice
			# and the rest of a sliced test in their own
			delete whole
			delete wholew
			delete pkgs
			np = 0
			for (i = 1; i <= units; i++) {
				u = order[i]
				if (ushard[u] != s) {
					continue
				}
				if (utest[u] == "") {
					printf "# %5d s  %s -run %s%s\n", uw[u], upkg[u], urun[u], (uskip[u] == "" ? "" : " -skip " uskip[u]) > "/dev/stderr"
					print upkg[u] "\t" urun[u] "\t" uskip[u]
					continue
				}
				if (!(upkg[u] in whole)) {
					pkgs[++np] = upkg[u]
					whole[upkg[u]] = utest[u]
				} else {
					whole[upkg[u]] = whole[upkg[u]] "|" utest[u]
				}
				wholew[upkg[u]] += uw[u]
			}
			for (p = 1; p <= np; p++) {
				printf "# %5d s  %s -run ^(%s)$\n", wholew[pkgs[p]], pkgs[p], whole[pkgs[p]] > "/dev/stderr"
				print pkgs[p] "\t^(" whole[pkgs[p]] ")$\t"
			}
		}
	}
' "$root/.github/harness-shards.txt" -)

[ "$shard" = plan ] && exit 0

# fd 3: a test reading stdin must not eat the list
failed=0
while IFS=$'\t' read -r -u 3 pkg run skip; do
	[ -n "$pkg" ] || continue
	args=(-race -count=1 -v -timeout "${TEST_TIMEOUT:-30m}" -run "$run")
	[ -n "$skip" ] && args+=(-skip "$skip")
	echo "+ go test ${args[*]} $pkg"
	go test "${args[@]}" "$pkg" || failed=1
done 3<<<"$runs"
exit $failed
