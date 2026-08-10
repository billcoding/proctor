#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"

usage(){
  cat <<EOF
service

用法:
 ./deploy.sh service [server|agent|all] [--start|--stop|--restart|--status]

默认 --status
EOF
}

COMP="all"
ACTION=status
while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    --start) ACTION=start; shift ;;
    --stop) ACTION=stop; shift ;;
    --restart) ACTION=restart; shift ;;
    --status) ACTION=status; shift ;;
    start|stop|restart|status) ACTION="$1"; shift ;;
    -I|--prefix) INSTALL_PREFIX="$2"; shift 2 ;;
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
  unit="$(component_unit "$comp")"
  bin="$(component_bin "$comp")"
  prefix="$(component_prefix "$comp")"
  if has_systemd; then
    case "$ACTION" in
      status) systemctl --no-pager --full status "$unit" || true ;;
      *) run_cmd systemctl "$ACTION" "$unit" ;;
    esac
  elif [[ -x "$prefix/$bin" ]]; then
    case "$ACTION" in
      status) run_cmd "$prefix/$bin" status || true ;;
      *) run_cmd "$prefix/$bin" "$ACTION" ;;
    esac
  else
    log "WARN: $comp 未安装 ($prefix)"
  fi
done
