#!/usr/bin/env bash
# 前台开发运行
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
run

用法:
 ./deploy.sh run <server|agent> [-- <透传参数>]

env:
 SERVER_CONFIG / AGENT_CONFIG   配置路径
 SERVER_URL                     agent 覆盖教师端地址
EOF
}

COMP="${1:-}"
[[ -n "$COMP" ]] || { usage; exit 2; }
case "$COMP" in
  help|-h|--help) usage; exit 0 ;;
esac
COMP="$(normalize_component "$COMP")" || die "run 请指定 server 或 agent"
shift || true

# 吃掉可选组件重复与 --
[[ "${1:-}" == "--" ]] && shift
EXTRA=("$@")

case "$COMP" in
  server)
    cfg="${SERVER_CONFIG:-$ROOT/configs/server.json}"
    log "run server -config $cfg"
    exec go run ./cmd/server -config "$cfg" ${EXTRA[@]+"${EXTRA[@]}"}
    ;;
  agent)
    cfg="${AGENT_CONFIG:-$ROOT/configs/agent.json}"
    args=(run -config "$cfg")
    [[ -n "${SERVER_URL:-}" ]] && args+=(-server "$SERVER_URL")
    [[ -n "${STUDENT_NAME:-}" ]] && args+=(-student "$STUDENT_NAME")
    [[ -n "${CLASSROOM:-}" ]] && args+=(-classroom "$CLASSROOM")
    log "run agent ${args[*]}"
    exec go run ./cmd/agent "${args[@]}" ${EXTRA[@]+"${EXTRA[@]}"}
    ;;
esac
