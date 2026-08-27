#!/usr/bin/env bash
# Regenerate the README screenshots.
#
# Everything here runs against a throwaway sandbox in $TMPDIR, never your real
# state, and every image is the actual output of the command above it — which
# is the point: a screenshot that was hand-edited stops being evidence.
#
#   ./docs/screenshots/generate.sh
#
# Needs: freeze (go install github.com/charmbracelet/freeze@latest)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

command -v freeze >/dev/null 2>&1 || {
  echo "freeze not found: go install github.com/charmbracelet/freeze@latest" >&2
  exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# HOME points at the sandbox so displayed paths abbreviate to ~/code/... —
# the images show what a real setup looks like, not a temp directory. The
# build and test runs below are given the real HOME back, since they need the
# module cache.
real_home="$HOME"
export HOME="$work/home"
mkdir -p "$HOME"
export XDG_DATA_HOME="$work/state"
export CLICOLOR_FORCE=1
export NO_COLOR=

# Build the binary under test, so the images can never drift from the code.
HOME="$real_home" go build -o "$work/prizm" "$root"
prizm="$work/prizm"

# freeze's defaults are a plain block; the window chrome and padding make the
# images read as terminal output rather than as a code block. The fixed width
# keeps them a consistent shape — left to auto-size, one long value turns an
# image into a letterbox and the set stops looking like one tool. It is a hard
# clip, not a wrap, so a line longer than this loses its tail: if an image
# looks truncated, the line is too long for a terminal too.
# freeze keeps its font cache under $HOME. Pointed at the sandbox it rebuilds
# that cache from scratch, which takes minutes; the real home is where its
# fonts already are.
shoot() { # shoot <name> <ansi-file>
  HOME="$real_home" freeze "$2" --language ansi --output "$here/$1.png" \
    --window --border.radius 8 --padding 24 --margin 16 --width 1000 \
    --font.family "JetBrains Mono,Fira Code,monospace" --font.size 15 --line-height 1.3 \
    >/dev/null
}

run() { # run <name> -- <command...>
  local name="$1"; shift; shift
  "$@" >"$work/$name.ansi" 2>&1 || true
  shoot "$name" "$work/$name.ansi"
  echo "  $name.png"
}

echo "building the sandbox…"
mkdir -p "$HOME/code"/{frontend,backend,auth,ai}

"$prizm" init my-saas-platform >/dev/null
for r in frontend backend auth ai; do
  "$prizm" add-repo my-saas-platform "$HOME/code/$r" --name "$r" >/dev/null
done

# The same five the picker fixture shows, so `ls` and the picker do not
# disagree about which workflows exist.
"$prizm" add-workflow my-saas-platform local         --tag local --repos frontend,backend,auth,ai >/dev/null
"$prizm" add-workflow my-saas-platform frontend-only --tag qa    --repos frontend                 >/dev/null
"$prizm" add-workflow my-saas-platform payments      --tag debug --repos backend,frontend         >/dev/null
"$prizm" add-workflow my-saas-platform staging       --tag qa    --repos frontend,backend,auth,ai >/dev/null
"$prizm" add-workflow my-saas-platform production    --tag prod  --repos frontend,backend,auth,ai >/dev/null

# Layer 0: true in every workflow.
"$prizm" var my-saas-platform --global _PRIZM_CLUSTER_USER=svc_app _PRIZM_CLUSTER_HOST=cluster.internal >/dev/null

# Layer 2: the values that differ per environment.
mkdir -p "$XDG_DATA_HOME/prizm/shared/my-saas-platform"/{local,production}
for wf in local production; do
  "$prizm" shared-add my-saas-platform "$wf" infra >/dev/null
done
cat >"$XDG_DATA_HOME/prizm/shared/my-saas-platform/local/infra.env" <<'ENV'
# prizm:repos ai,auth,backend,frontend
_PRIZM_DB_NAME=my_saas_local
_PRIZM_MONGO_URI=mongodb://${_PRIZM_CLUSTER_USER}@${_PRIZM_CLUSTER_HOST}/${_PRIZM_DB_NAME}
_PRIZM_AUTH_URL=http://localhost:4000
ENV
cat >"$XDG_DATA_HOME/prizm/shared/my-saas-platform/production/infra.env" <<'ENV'
# prizm:repos ai,auth,backend,frontend
_PRIZM_DB_NAME=my_saas_prod
_PRIZM_MONGO_URI=mongodb://${_PRIZM_CLUSTER_USER}@${_PRIZM_CLUSTER_HOST}/${_PRIZM_DB_NAME}
_PRIZM_AUTH_URL=https://auth.my-saas-platform.invalid
ENV
"$prizm" shared-sync my-saas-platform --yes >/dev/null

# Layer 1: the wiring, written once per repo.
"$prizm" var my-saas-platform auth     'MONGO_URI=${_PRIZM_MONGO_URI}' PORT=4000 >/dev/null
"$prizm" var my-saas-platform backend  'MONGO_URI=${_PRIZM_MONGO_URI}' 'AUTH_URL=${_PRIZM_AUTH_URL}' PORT=4001 >/dev/null
"$prizm" var my-saas-platform frontend 'NEXT_PUBLIC_AUTH_URL=${_PRIZM_AUTH_URL}' PORT=3000 >/dev/null
"$prizm" var my-saas-platform ai       'MONGO_URI=${_PRIZM_MONGO_URI}' 'AUTH_URL=${_PRIZM_AUTH_URL}' PORT=4003 >/dev/null

echo "rendering…"

run ls        -- "$prizm" ls my-saas-platform
run dry-run   -- "$prizm" up my-saas-platform local --dry-run

"$prizm" up my-saas-platform local >/dev/null

# A hand-edit, so sync has something real to reconcile.
env_file="$(readlink "$HOME/code/auth/.env")"
sed -i 's/PORT=4000/PORT=9999/' "$env_file"
printf 'DEBUG_TRACE=on\n' >>"$env_file"
run sync      -- "$prizm" sync my-saas-platform auth --yes

# Leave one repo drifted, because a listing where everything is clean does
# not show what status is for.
sed -i 's/PORT=4001/PORT=4444/' "$(readlink "$HOME/code/backend/.env")"
run status    -- "$prizm" status my-saas-platform
run audit     -- "$prizm" audit my-saas-platform auth

# The interactive surfaces render without a terminal, which is exactly why
# their update/View methods are kept pure.
tui="$work/tui"; mkdir -p "$tui"
( cd "$root" && HOME="$real_home" PRIZM_SHOWCASE=1 PRIZM_SHOWCASE_DIR="$tui" \
  go test ./internal/tui/ -run TestRenderShowcase >/dev/null 2>&1 )

shoot picker   "$tui/workflow-picker---cursor-on-row-1.ansi";      echo "  picker.png"
shoot resolve  "$tui/sync---one-decision-per-row.ansi";            echo "  resolve.png"
shoot carousel "$tui/history-carousel---scrubbed-back-two.ansi";   echo "  carousel.png"

echo "done — $here"
