#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"

usage(){
  cat <<EOF
uninstall

用法:
 ./deploy.sh uninstall [server|agent|all] [选项]

options:
 -I,--prefix <DIR>
 -X,--purge          删除整个安装目录
 -n,--dry-run
EOF
}

COMP="all"
PURGE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -I|--prefix) INSTALL_PREFIX="$2"; shift 2 ;;
    -X|--purge) PURGE=1; shift ;;
    -n|--dry-run) DRY_RUN=1; shift ;;
    server|agent|all|s|a|srv|agt) COMP="$(normalize_component "$1")"; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  *) comps=("$COMP") ;;
esac

for comp in "${comps[@]}"; do
  prefix="$(component_prefix "$comp")"
  unit="$(component_unit "$comp")"
  bin="$(component_bin "$comp")"
  if has_systemd; then
    run_cmd systemctl stop "$unit" 2>/dev/null || true
    run_cmd systemctl disable "$unit" 2>/dev/null || true
    [[ -f "$SYSTEMD_DIR/$unit" ]] && run_cmd rm -f "$SYSTEMD_DIR/$unit"
    run_cmd systemctl daemon-reload 2>/dev/null || true
  elif [[ "$comp" == "agent" && -x "$prefix/$bin" ]]; then
    run_cmd "$prefix/$bin" stop 2>/dev/null || true
    run_cmd "$prefix/$bin" uninstall 2>/dev/null || true
  fi
  if [[ "$PURGE" == "1" ]]; then
    run_cmd rm -rf "$prefix"
  else
    [[ -f "$prefix/$bin" ]] && run_cmd rm -f "$prefix/$bin"
  fi
  log "uninstalled $comp"
done
