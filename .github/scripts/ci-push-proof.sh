#!/usr/bin/env bash
# Whether a push to main is a pull request's run already proved, so CI
# needs to add only what pull requests leave out (ci-plan.sh push). Prints
# the pull request's number and exits 0 when:
#
#   - <sha> is the squash merge of exactly one pull request into main;
#   - the pull request's head has <sha>'s tree (it was up to date with
#     main, as the ruleset requires; so its run tested this tree);
#   - a ci-ok check run of GitHub Actions passed on that head.
#
# Otherwise it prints why not and exits 1, and the push gets a full run: a
# direct push, a merge that bypassed the ruleset, a pull request whose
# ci-ok never passed, or a GitHub API error.
#
#   ci-push-proof.sh <sha>   (GH_TOKEN; GITHUB_REPOSITORY)
set -euo pipefail

sha=${1:?usage: ci-push-proof.sh <sha>}
repo=${GITHUB_REPOSITORY:?}

no() {
	echo "$*"
	exit 1
}

pr=$(gh api "repos/$repo/commits/$sha/pulls" --jq "[.[] | select(.merged_at != null and .merge_commit_sha == \"$sha\" and .base.ref == \"main\")] | if length == 1 then .[0] | \"\(.number) \(.head.sha)\" else empty end") ||
	no "the pull requests of $sha could not be read"
[ -n "$pr" ] || no "$sha is no pull request's squash merge into main"
read -r number head <<<"$pr"

tree=$(gh api "repos/$repo/git/commits/$sha" --jq .tree.sha) || no "the tree of $sha could not be read"
headtree=$(gh api "repos/$repo/git/commits/$head" --jq .tree.sha) || no "the tree of #$number's head $head could not be read"
[ "$tree" = "$headtree" ] || no "#$number's head $head has another tree than $sha (it was not up to date with main)"

passed=$(gh api "repos/$repo/commits/$head/check-runs?check_name=ci-ok&filter=all&per_page=100" \
	--jq '[.check_runs[] | select(.conclusion == "success" and .app.slug == "github-actions")] | length') ||
	no "the checks of #$number's head $head could not be read"
[ "$passed" -gt 0 ] || no "ci-ok never passed on #$number's head $head"

echo "$number"
