#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"

usage(){
  cat <<EOF
status

用法:
 ./deploy.sh status [server|agent|all] [-I prefix] [-V]
EOF
}

COMP="all"
VERBOSE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -I|--prefix) INSTALL_PREFIX="$2"; shift 2 ;;
    -V|--verbose) VERBOSE=1; shift ;;
    server|agent|all|s|a|srv|agt) COMP="$(normalize_component "$1")"; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  *) comps=("$COMP") ;;
esac

echo "== dist =="
ls -la "$ROOT/dist" 2>/dev/null || echo "(empty)"
echo

for comp in "${comps[@]}"; do
  prefix="$(component_prefix "$comp")"
  unit="$(component_unit "$comp")"
  bin="$(component_bin "$comp")"
  echo "== $comp =="
  echo "prefix: $prefix"
  if [[ -x "$prefix/$bin" ]]; then
    echo "binary: OK ($prefix/$bin)"
    "$prefix/$bin" version 2>/dev/null || true
  else
    echo "binary: missing"
  fi
  if has_systemd; then
    systemctl is-active "$unit" 2>/dev/null || echo "service: inactive/missing"
    systemctl is-enabled "$unit" 2>/dev/null || true
  elif [[ -x "$prefix/$bin" ]]; then
    "$prefix/$bin" status 2>/dev/null || true
  fi
  if [[ "$VERBOSE" == "1" ]]; then
    cfg="$([ "$comp" = server ] && echo server.json || echo agent.json)"
    [[ -f "$prefix/$cfg" ]] && { echo "--- $cfg ---"; cat "$prefix/$cfg"; }
    [[ -f "$SYSTEMD_DIR/$unit" ]] && { echo "--- $unit ---"; cat "$SYSTEMD_DIR/$unit"; }
  fi
  echo
done
