#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"

usage(){
  cat <<EOF
clean

用法:
 ./deploy.sh clean
删除 dist/ 与 bin/ 下构建产物
EOF
}

case "${1:-}" in
  help|-h|--help) usage; exit 0 ;;
esac

rm -rf "$ROOT/dist" "$ROOT/bin"
log "cleaned dist/ bin/"
