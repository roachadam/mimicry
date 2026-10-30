#!/usr/bin/env bash
# Splits the harness module's tests (internal/harness) into shards that CI
# runs as parallel jobs, and runs one shard.
#
#   harness-shard.sh <shard> <total>   run shard <shard> (1 to <total>)
#   harness-shard.sh plan <total>      print every shard's go test commands
#
# Each top-level test (go test -list) is a unit weighed in
# .github/harness-shards.txt (2 seconds if not listed there). A weight line
# naming Test/regex makes that slice of Test's subtests a unit of its own;
# the subtests no slice matches are one more unit. A line naming TestA|TestB
# keeps those tests in one unit (they share a costly setup that whichever
# runs first pays). Units are dealt heaviest first (ties by name) to the
# shard with the least work so far (ties: the lower shard), so the split
# depends only on the test list and the weights, and a new test always lands
# in some shard. A shard runs its units with
# go test -race -count=1 and -timeout $TEST_TIMEOUT (default 30m);
# MIMICRY_PROFILES passes through to the tests.
set -euo pipefail

usage() {
	echo "usage: $0 <shard> <total> | plan <total>" >&2
	exit 2
}

[ $# -eq 2 ] || usage
shard=$1
total=$2
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
runs=$(echo "$tests" | awk -v shard="$shard" -v total="$total" -v weights=".github/harness-shards.txt" '
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
	FNR == NR {
		if ($0 ~ /^[ \t]*(#|$)/) {
			next
		}
		slash = index($3, "/")
		if (!slash && index($3, "|")) {
			n = split($3, members, "|")
			for (k = 1; k <= n; k++) {
				listed[$2 " " members[k]] = $3
				group[$2 " " members[k]] = $3
			}
			weight[$2 " " $3] = $1
			next
		}
		key = $2 " " (slash ? substr($3, 1, slash - 1) : $3)
		listed[key] = $3
		if (slash) {
			k = ++nslices[key]
			slicew[key, k] = $1
			slicere[key, k] = substr($3, slash + 1)
		} else {
			weight[key] = $1
		}
		next
	}
	{
		key = $1 " " $2
		found[key] = 1
		if (key in group) {
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
			printf "# shard %d/%d: about %d s of tests\n", s, total, load[s] > "/dev/stderr"
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
	args=(-race -count=1 -timeout "${TEST_TIMEOUT:-30m}" -run "$run")
	[ -n "$skip" ] && args+=(-skip "$skip")
	echo "+ go test ${args[*]} $pkg"
	go test "${args[@]}" "$pkg" || failed=1
done 3<<<"$runs"
exit $failed
