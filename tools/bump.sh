#!/bin/sh
# bump.sh <new-version> — sync the plugin version across manifest.json,
# ui/package.json, and system-go/main.go (when it carries pluginVersion).
# It does NOT repack or re-sign: after bumping, rebuild the bundle, update
# bundle.digest_sha256, and re-sign (see README "Reproducible bundle").
set -eu

if [ $# -ne 1 ]; then
    echo "usage: $0 <new-version>   e.g. $0 0.9.0-alpha.2" >&2
    exit 2
fi
new=$1
case "$new" in
    *[!A-Za-z0-9.-]* | "" | .* | -* | *..*)
        echo "bump: invalid version $new (semver-ish letters/digits/dot/hyphen)" >&2
        exit 2
        ;;
esac

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
changed=0

bump_json() {
    file=$1
    [ -f "$file" ] || return 0
    old=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$file" | head -1)
    [ -n "$old" ] || return 0
    if [ "$old" = "$new" ]; then
        echo "bump: $file already at $new"
        return 0
    fi
    sed -i.bak 's/^  "version": ".*",$/  "version": "'"$new"'",/' "$file" && rm -f "$file.bak"
    echo "bump: $file $old -> $new"
    changed=1
}

bump_json "$root/manifest.json"
bump_json "$root/ui/package.json"

main_go=$root/system-go/main.go
if [ -f "$main_go" ] && grep -q 'pluginVersion = "' "$main_go"; then
    old=$(sed -n 's/.*pluginVersion = "\(.*\)".*/\1/p' "$main_go" | head -1)
    if [ "$old" != "$new" ]; then
        sed -i.bak 's/pluginVersion = ".*"/pluginVersion = "'"$new"'"/' "$main_go" && rm -f "$main_go.bak"
        echo "bump: $main_go $old -> $new"
        changed=1
    else
        echo "bump: $main_go already at $new"
    fi
fi

if [ "$changed" -eq 0 ]; then
    echo "bump: nothing to change"
    exit 0
fi
cat <<'EOF'
bump: done. Next:
  1. rebuild the bundle (Go 1.26.4, Node 22)
  2. pluginpack -> write bundle.digest_sha256 into manifest.json
  3. pluginsign -write (never commit the seed)
EOF
