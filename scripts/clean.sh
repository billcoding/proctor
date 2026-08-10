#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"

# 解析 DATA_DIR / UPDATES_DIR（相对路径相对仓库根）
resolve_abs_dir(){
  local d="$1"
  if [[ "$d" != /* ]]; then
    d="$ROOT/${d#./}"
  fi
  printf '%s' "$d"
}

usage(){
  cat <<EOF
clean

用法:
 ./deploy.sh clean [选项]

删除 dist/ 与 bin/ 下构建产物

options:
 -u,--updates          同时清除 \$UPDATES_DIR（默认 ./data/updates）

env:
 DATA_DIR              默认 ./data
 UPDATES_DIR           默认 \$DATA_DIR/updates
EOF
}

CLEAN_UPDATES=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -u|--updates|updates) CLEAN_UPDATES=1; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

rm -rf "$ROOT/dist" "$ROOT/bin"
log "cleaned dist/ bin/"

if [[ "$CLEAN_UPDATES" == "1" ]]; then
  DATA_DIR="$(resolve_abs_dir "${DATA_DIR:-./data}")"
  if [[ -n "${UPDATES_DIR:-}" ]]; then
    UPDATES_DIR="$(resolve_abs_dir "$UPDATES_DIR")"
  else
    UPDATES_DIR="$DATA_DIR/updates"
  fi
  rm -rf "$UPDATES_DIR"
  log "cleaned updates → $UPDATES_DIR"
fi
