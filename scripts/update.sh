#!/usr/bin/env bash
# 本机更新（默认仅二进制；-c 覆盖配置；-u 覆盖 unit）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
update

用法:
 ./deploy.sh update [server|agent|all] [选项]

options:
 -I,--prefix <DIR>   安装前缀
 -c,--config         覆盖配置文件
 -u,--unit           覆盖 systemd unit
 -S,--skip-build     package 跳过重建
 -n,--dry-run
 --set key=value     增量改配置（可多次）
EOF
}

COMP="all"
UPDATE_CONFIG=0
UPDATE_UNIT=0
SKIP_BUILD=0
CONFIG_SETS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -I|--prefix) INSTALL_PREFIX="$2"; shift 2 ;;
    -c|--config) UPDATE_CONFIG=1; shift ;;
    -u|--unit) UPDATE_UNIT=1; shift ;;
    -S|--skip-build) SKIP_BUILD=1; shift ;;
    -n|--dry-run) DRY_RUN=1; shift ;;
    --set) CONFIG_SETS+=("$2"); shift 2 ;;
    --server-url) CONFIG_SETS+=("server_url=$2"); shift 2 ;;
    --student) CONFIG_SETS+=("student_name=$2"); shift 2 ;;
    --classroom) CONFIG_SETS+=("classroom=$2"); shift 2 ;;
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
  local_tar="$ROOT/dist/proctor-${comp}.tar.gz"
  if [[ ! -f "$local_tar" ]]; then
    args=("$comp")
    [[ "$SKIP_BUILD" == "1" ]] && args+=(-S)
    bash "$SCRIPT_DIR/package.sh" "${args[@]}"
  fi
  prefix="$(component_prefix "$comp")"
  bin="$(component_bin "$comp")"
  unit="$(component_unit "$comp")"
  cfg="$([ "$comp" = server ] && echo server.json || echo agent.json)"
  [[ -d "$prefix" ]] || die "未安装: $prefix（请先 install）"
  need_root_or_writable "$prefix"

  stage="$(mktemp -d "${TMPDIR:-/tmp}/proctor-upd.XXXXXX")"
  tar -C "$stage" -xzf "$local_tar"
  # backup
  bak="$prefix/backup/$(date '+%Y%m%d%H%M%S')"
  run_cmd mkdir -p "$bak"
  [[ -f "$prefix/$bin" ]] && run_cmd cp "$prefix/$bin" "$bak/" || true
  [[ -f "$prefix/$cfg" ]] && run_cmd cp "$prefix/$cfg" "$bak/" || true

  run_cmd cp "$stage/$bin" "$prefix/$bin"
  run_cmd chmod +x "$prefix/$bin" || true
  if [[ "$UPDATE_CONFIG" == "1" ]]; then
    run_cmd cp "$stage/$cfg" "$prefix/$cfg"
  fi
  if [[ ${#CONFIG_SETS[@]} -gt 0 ]]; then
    run_cmd json_set_file "$prefix/$cfg" "${CONFIG_SETS[@]}"
  fi
  if [[ "$UPDATE_UNIT" == "1" ]] && has_systemd; then
    body="$(sed "s|/opt/proctor/${comp}|${prefix}|g" "$stage/$unit")"
    if is_dry_run; then
      log "[dry-run] update unit $unit"
    else
      printf '%s\n' "$body" >"$SYSTEMD_DIR/$unit"
      systemctl daemon-reload
    fi
  fi
  if has_systemd; then
    run_cmd systemctl restart "$unit" || true
  else
    run_cmd "$prefix/$bin" restart || run_cmd "$prefix/$bin" start || true
  fi
  rm -rf "$stage"
  log "updated $comp @ $prefix"
done
log "update done"
