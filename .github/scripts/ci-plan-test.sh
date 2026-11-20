#!/usr/bin/env bash
# Cases for ci-plan.sh: changed paths in, the plan out. ci.yml's zizmor job
# runs it (a change to .github/ runs that job).
#
#   bash .github/scripts/ci-plan-test.sh
set -euo pipefail

plan="$(dirname "$0")/ci-plan.sh"
failed=0

# check <name> <mode> <paths, one per line> <expected output>
check() {
	local got
	got=$(printf '%s' "$3" | bash "$plan" "$2" 2>/dev/null | sort)
	if [ "$got" != "$(printf '%s\n' "$4" | sort)" ]; then
		echo "FAIL $1"
		diff <(printf '%s\n' "$4" | sort) <(echo "$got") | sed 's/^/    /' || true
		failed=1
	else
		echo "ok   $1"
	fi
}

everything_core='jobs=["lint","test-root","vuln","zizmor","test-harness","test-node","test-python"]
tiers=core
shards=[1,2]
node=all
python=all'

check 'docs only: nothing' pr 'README.md
docs/ci.md
bindings/node/README.md
internal/bench/reports/2026-11-01.json' 'jobs=[]
tiers=
shards=[]
node=none
python=none'

check 'no paths: nothing' pr '' 'jobs=[]
tiers=
shards=[]
node=none
python=none'

check 'library: every job but zizmor, core' pr 'mimicry.go' 'jobs=["lint","test-root","vuln","test-harness","test-node","test-python"]
tiers=core
shards=[1,2]
node=all
python=all'

check 'protocol code: protocol tier' pr 'internal/h2/frame.go' 'jobs=["lint","test-root","vuln","test-harness","test-node","test-python"]
tiers=core,protocol
shards=[1,2,3,4,5]
node=all
python=all'

check 'profile data: protocol and gen tiers' pr 'profiles/data/chrome-157-macos.json' 'jobs=["lint","test-root","vuln","test-harness","test-node","test-python"]
tiers=core,protocol,gen
shards=[1,2,3,4,5,6]
node=all
python=all'

check 'rpc schema: the library owns it' pr 'proto/mimicry/v1/client.proto
cmd/mimicry-core/main.go' 'jobs=["lint","test-root","vuln","test-harness","test-node","test-python"]
tiers=core
shards=[1,2]
node=all
python=all'

check 'harness: no library tests, the SDKs only through the harness' pr 'internal/harness/internal/headers/order.go' 'jobs=["lint","vuln","test-harness","test-node","test-python"]
tiers=core
shards=[1,2]
node=harness
python=harness'

check 'harness e2e: protocol tier' pr 'internal/harness/e2e/h2_test.go' 'jobs=["lint","vuln","test-harness","test-node","test-python"]
tiers=core,protocol
shards=[1,2,3,4,5]
node=harness
python=harness'

check 'harness module versions: protocol tier' pr 'internal/harness/go.mod' 'jobs=["lint","vuln","test-harness","test-node","test-python"]
tiers=core,protocol
shards=[1,2,3,4,5]
node=harness
python=harness'

check 'generator: gen tier' pr 'internal/harness/cmd/hellogen/main.go' 'jobs=["lint","vuln","test-harness","test-node","test-python"]
tiers=core,gen
shards=[1,2,3]
node=harness
python=harness'

check 'bench: lint, vuln, its build' pr 'internal/bench/cmd/bench/main.go' 'jobs=["lint","test-root","vuln"]
tiers=
shards=[]
node=none
python=none'

check 'node SDK: its jobs only' pr 'bindings/node/src/client.ts
bindings/node/package.json' 'jobs=["test-node"]
tiers=
shards=[]
node=all
python=none'

check 'python SDK: its jobs only' pr 'bindings/python/src/mimicry/client.py' 'jobs=["test-python"]
tiers=
shards=[]
node=none
python=all'

check 'harness and node SDK: node all, python through the harness' pr 'internal/harness/e2e/node_test.go
bindings/node/test/fixtures/harness-driver.ts' 'jobs=["lint","vuln","test-harness","test-node","test-python"]
tiers=core,protocol
shards=[1,2,3,4,5]
node=all
python=harness'

check 'workflow: every job' pr '.github/workflows/ci.yml' "$everything_core"

check 'shared SDK script: every job' pr 'bindings/build-core.sh' "$everything_core"

check 'unknown top-level path: every job' pr 'tools/gen.go' "$everything_core"

check 'Go version: every job, protocol tier' pr '.prototools' 'jobs=["lint","test-root","vuln","zizmor","test-harness","test-node","test-python"]
tiers=core,protocol
shards=[1,2,3,4,5]
node=all
python=all'

check 'docs and node SDK: node only' pr 'docs/ci.md
bindings/node/src/client.ts' 'jobs=["test-node"]
tiers=
shards=[]
node=all
python=none'

check 'full: every job and tier' full '' 'jobs=["lint","test-root","vuln","zizmor","test-harness","test-node","test-python"]
tiers=core,protocol,gen,main
shards=[1,2,3,4,5,6,7,8]
node=all
python=all'

CI_SELECTION=full check 'CI_SELECTION=full: node SDK runs every job' pr 'bindings/node/src/client.ts' "$everything_core"

CI_SELECTION=full check 'CI_SELECTION=full: docs still nothing' pr 'docs/ci.md' 'jobs=[]
tiers=
shards=[]
node=none
python=none'

exit $failed
