#!/usr/bin/env bash
#
# Forget every migration this checkout has run, without touching any workplace.
#
# Removes the migrator's checkpoint, run history and service-token ledger, so
# the next migration starts from nothing: no projects treated as already
# migrated, no tokens remembered as issued. Useful after pointing the migrator
# at a different target, or after emptying one by hand. To empty the target as
# well, use `make wipe-target ARGS="--yes --clear-state"` instead.
#
# Refuses while the migrator is running. The server keeps the token ledger in
# memory and writes it back on its next save, so files cleared under a running
# server quietly reappear.
#
# Honours CHECKPOINT_DIR and PORT, the same variables the server reads.

set -euo pipefail

dir="${CHECKPOINT_DIR:-$(pwd)/data}"
port="${PORT:-8080}"

# Kept in step with stateFiles in backend/cmd/wipe-target/main.go.
files=(migration-checkpoint.json scope-history.json service-tokens.json)

# bash's own /dev/tcp rather than curl, so this needs nothing installed. The
# server always answers on loopback, whether it binds 127.0.0.1 or 0.0.0.0.
if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
  echo "The migrator is running on port ${port}. Stop it first — it keeps the token" >&2
  echo "ledger in memory and would write it straight back." >&2
  exit 1
fi

removed=0
for name in "${files[@]}"; do
  path="${dir}/${name}"
  if [ -e "$path" ]; then
    rm -f -- "$path"
    echo "removed ${path}"
    removed=$((removed + 1))
  fi
done

if [ "$removed" -eq 0 ]; then
  echo "Nothing to clear in ${dir}."
else
  echo "Cleared. The next migration starts from nothing."
fi
