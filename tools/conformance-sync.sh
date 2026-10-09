#!/bin/sh
# conformance-sync.sh <harness checkout> <ref> - refresh conformance/ from the
# private conformance harness (lattice-substore-conformance) at a tag or commit.
#
# conformance/ is laid out as the harness root, so the vendored check.mjs runs
# unmodified against it: corpus/, goldens/, allowlist/, the checker and its
# library, the upstream pin, the package files, and the harness's
# .gitattributes so corpus and golden bytes are never converted. The copy is
# taken from the commit object with git archive, never from the harness's
# working tree, and is limited to the paths below. specs/ is never copied: the
# specifications are written from reading upstream Sub-Store and stay private.
# Nothing that fetches or bundles upstream (build.mjs, run.mjs, the build/
# directory) is copied either. TestVendoredDataCarriesNoUpstreamText and
# TestVendoredCheckerMatchesHarnessCommit (system-go/cmd/substore-conformance)
# hold the result to both rules.
#
# After a sync, regenerate conformance/conformance.json in the same commit
# (README, "Conformance data"): the conformance job runs check.mjs with
# --expect against it and fails until the numbers match.
set -eu

if [ $# -ne 2 ]; then
    echo "usage: $0 <harness checkout> <tag or commit>   e.g. $0 ../lattice-substore-conformance v0.1.0-alpha.1" >&2
    exit 2
fi
harness=$1
ref=$2

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
dest=$root/conformance

commit=$(git -C "$harness" rev-parse --verify --quiet "$ref^{commit}") || {
    echo "conformance-sync: $ref is not a commit in $harness (fetch the harness first)" >&2
    exit 1
}

# The only paths that leave the harness. Keep this list and the test's
# vendoredPaths in step.
paths='.gitattributes corpus goldens allowlist oracle/check.mjs oracle/lib oracle/upstream.json oracle/package.json oracle/package-lock.json'

# Clear every top-level entry the copy owns, whole, so a file someone added by
# hand under oracle/ or goldens/ does not survive a sync. conformance.json is
# the plugin's own numbers and stays.
for p in .gitattributes corpus goldens allowlist oracle HARNESS_COMMIT; do
    rm -rf "${dest:?}/$p"
done
mkdir -p "$dest"
# shellcheck disable=SC2086 # the path list is split on purpose
git -C "$harness" archive --format=tar "$commit" -- $paths | tar -x -f - -C "$dest"
printf '%s\n' "$commit" >"$dest/HARNESS_COMMIT"

echo "conformance-sync: conformance/ now matches $ref ($commit)"
