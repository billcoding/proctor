#!/usr/bin/env bash
# Proctor 统一运维入口（一键部署）
#
# 能力：run / build / package|deploy / publish_update / install / update / uninstall /
#       service / status / log / clean / upload / remote_*
#
# 组件：server（教师端）| agent（学生机）| all
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
SCRIPTS="$ROOT/scripts"
cd "$ROOT"

# shellcheck source=scripts/_lib.sh
source "$SCRIPTS/_lib.sh"
load_workspace_dotenv "$ROOT"

_DESC_COL=34
_u_cmd_sep=0
_u_cmd(){
  if [[ "${_u_cmd_sep}" -eq 1 ]]; then echo; fi
  _u_cmd_sep=1
  printf " %-$(( _DESC_COL ))s %s\n" "$1" "${2:-}"
}
_u_opt(){ printf "  %-$(( _DESC_COL - 1 ))s %s\n" "$1" "${2:-}"; }
_u_group(){
  echo
  if [[ -n "${2:-}" ]]; then printf "%s: %s\n" "$1" "$2"
  else printf "%s:\n" "$1"; fi
}
_u_envs(){ echo; printf " env:\n"; }
_u_opts(){ echo; printf " options:\n"; }
_u_cmds(){ _u_cmd_sep=0; echo; }

via_script(){
  local name="$1"; shift
  local path="$SCRIPTS/$name"
  [[ -f "$path" ]] || die "缺少脚本: scripts/$name"
  exec bash "$path" "$@"
}

call_script(){
  local name="$1"; shift
  local path="$SCRIPTS/$name"
  [[ -f "$path" ]] || die "缺少脚本: scripts/$name"
  bash "$path" "$@"
}

usage(){
  cat <<EOF
Proctor 统一运维入口

用法:
 ./deploy.sh <命令> [组件] [参数...]
 ./deploy.sh <命令> -h,--help

组件: server | agent | all
  缩写: s | a

安装布局（Linux，可用 INSTALL_PREFIX 覆盖）:
  /opt/proctor/server/proctor-server + server.json + systemd
  /opt/proctor/agent/proctor-agent   + agent.json  + systemd
EOF

  if [[ -f "$ROOT/.env" ]]; then
    echo
    echo "提示: 已加载 .env → $ROOT/.env"
  fi

  _u_group "run" "开发运行"
  _u_envs
  _u_opt "SERVER_CONFIG / AGENT_CONFIG" "配置路径"
  _u_opt "SERVER_URL / STUDENT_NAME / CLASSROOM" "agent 覆盖项"
  _u_cmds
  _u_cmd "run server|agent"        "go run 前台启动"

  _u_group "build" "编译 → dist/"
  _u_opts
  _u_opt "-D,--darwin"             "编译 darwin"
  _u_opt "-L,--linux"              "编译 linux"
  _u_opt "-W,--windows"            "编译 windows"
  _u_opt "-a,--all"                "一次编译三端"
  _u_cmds
  _u_cmd "build [server|agent|all]" "默认当前平台；可加平台 flag"

  _u_group "package / deploy" "打发布包（bin+config+unit）"
  _u_envs
  _u_opt "DATA_DIR / UPDATES_DIR"  "OTA 目录（默认 ./data/updates）"
  _u_opt "SKIP_PUBLISH_UPDATE=1"     "打包后不写 updates/"
  _u_opts
  _u_opt "-S,--skip-build"         "跳过重建"
  _u_opt "-U,--skip-update"        "跳过写入 updates/"
  _u_opt "-L/--linux 等"           "透传给 build（默认 linux）"
  _u_opt "-v,--version <VER>"      "APP_VERSION（与 OTA 一致）"
  _u_cmds
  _u_cmd "package|deploy [组件]"   "→ dist/*.tar.gz；含 agent 时自动写 ./data/updates/"

  _u_group "publish_update" "仅发布 Agent OTA"
  _u_envs
  _u_opt "DATA_DIR"                "默认 ./data"
  _u_opt "UPDATES_DIR"             "默认 \$DATA_DIR/updates"
  _u_opt "APP_VERSION"             "写入 version.json / ldflags"
  _u_opts
  _u_opt "-S,--skip-build"         "跳过重建"
  _u_opt "-v,--version <VER>"      "版本号"
  _u_opt "--force / --notes <T>"   "manifest 字段"
  _u_opt "-n,--dry-run"            "只打印"
  _u_cmds
  _u_cmd "publish_update|package_update" "交叉编译 agent → updates/ + version.json"

  _u_group "install / update / uninstall" "本机部署"
  _u_envs
  _u_opt "INSTALL_PREFIX"          "默认 /opt/proctor"
  _u_opt "DRY_RUN=1"               "只打印"
  _u_opts
  _u_opt "-I,--prefix <DIR>"       "安装前缀"
  _u_opt "-N,--no-start"           "装完不启动"
  _u_opt "-n,--dry-run"            "只打印"
  _u_opt "--server-url / --student / --classroom" "写入 agent.json"
  _u_opt "--listen / --admin-token" "写入 server.json"
  _u_opt "--set key=value"         "增量写 JSON（可多次）"
  _u_cmds
  _u_cmd "install [组件]"          "首次安装（Linux systemd；macOS/Win 仅 agent）"
  _u_cmd "update [组件]"           "更新二进制（-c 覆盖配置，-u 覆盖 unit）"
  _u_cmd "uninstall [组件]"        "停服务并移除（-X 清目录）"

  _u_group "service / status / log / clean"
  _u_cmds
  _u_cmd "service [组件] --start|--stop|--restart" "本机服务"
  _u_cmd "status [组件] [-V]"      "检查 dist / 安装 / 服务"
  _u_cmd "log [server|agent] [-f] [-n N]" "journalctl（Linux）"
  _u_cmd "clean"                   "删除 dist/ bin/"

  _u_group "remote" "远程上传与部署（SSH；缺包先 package / Windows 先 build -W）"
  _u_envs
  _u_opt "REMOTE_SSH_HOST"         "远程主机 user@host"
  _u_opt "REMOTE_SSH_PASSWORD"     "密码（需 sshpass）"
  _u_opt "REMOTE_SSH_KEY / PORT"   "密钥 / 端口"
  _u_opt "REMOTE_OS"               "windows|linux（可强制；默认 SSH 探测）"
  _u_opt "REMOTE_PATH"             "远端临时目录"
  _u_opts
  _u_opt "-H,--host <user@host>"   "远程主机"
  _u_opt "-P,--password <pass>"    "密码登录（需 sshpass）"
  _u_opt "-k,--key / -p,--port"    "SSH 私钥 / 端口"
  _u_opt "--os windows|linux"      "强制远端 OS"
  _u_opt "-I,--prefix <DIR>"       "远程安装前缀（Linux）"
  _u_opt "-R,--remote-path <DIR>"  "远端临时目录"
  _u_opt "-S/-N/-n"                "skip-build / no-start / dry-run"
  _u_cmds
  _u_cmd "upload [组件]"           "仅上传（Linux: tar；Windows: exe+ps1）"
  _u_cmd "remote_install [组件]"   "远程首次安装（Win 仅 agent，需 OpenSSH）"
  _u_cmd "remote_update [组件]"    "远程更新二进制"
  _u_cmd "remote_status [组件]"    "远程状态"
  _u_cmd "remote_service [组件]"   "远程启停"
  _u_cmd "remote_log [组件]"       "远程日志（Linux journalctl）"
  _u_cmd "remote_uninstall [组件]" "远程卸载"
  _u_cmd "remote_ssh [user@host]"  "交互 SSH"

  _u_group "示例"
  _u_cmds
  _u_cmd "./deploy.sh build all -a" "交叉编译三端 server+agent"
  _u_cmd "./deploy.sh run server"  "本机跑教师端"
  _u_cmd "./deploy.sh deploy all"  "打 linux 发布包并写 OTA"
  _u_cmd "./deploy.sh publish_update -v 0.2.0" "仅发布 Agent 到 ./data/updates"
  _u_cmd "sudo ./deploy.sh install agent --server-url http://10.0.0.2:8911 --student 张三"
  _u_cmd "./deploy.sh remote_install agent -H root@192.168.1.10 --server-url http://10.0.0.2:8911"
  _u_cmd "./deploy.sh remote_install agent -H Administrator@192.168.1.20 --os windows --server-url http://10.0.0.2:8911 --student 张三"
  echo
}

cmd_log(){
  local comp="${1:-server}"
  shift || true
  case "$comp" in
    help|-h|--help)
      cat <<EOF
log

用法:
 ./deploy.sh log [server|agent] [-f|--follow] [-n|--lines N]
EOF
      return 0
      ;;
  esac
  if c="$(normalize_component "$comp" 2>/dev/null)"; then
    comp="$c"
  else
    set -- "$comp" "$@"
    comp=server
  fi
  [[ "$comp" != "all" ]] || die "log 请指定 server 或 agent"
  has_systemd || die "log 需要 Linux systemd（或用 remote_log）"
  local unit lines=100 follow=0
  unit="$(component_unit "$comp")"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -f|--follow) follow=1; shift ;;
      -n|--lines) lines="$2"; shift 2 ;;
      *) die "未知参数: $1" ;;
    esac
  done
  local args=(-u "$unit" --no-pager -n "$lines")
  [[ "$follow" == "1" ]] && args+=(-f)
  exec journalctl "${args[@]}"
}

# $1=default if missing；设置 COMP / REMAINING
_take_comp(){
  local def="${1:-}"
  shift || true
  local first="${1:-}"
  if [[ -z "$first" || "$first" == help || "$first" == -h || "$first" == --help ]]; then
    if [[ -n "$first" ]]; then
      COMP="help"
      REMAINING=("${@:2}")
    else
      COMP="${def:-}"
      REMAINING=()
    fi
    return 0
  fi
  if COMP="$(normalize_component "$first" 2>/dev/null)"; then
    REMAINING=("${@:2}")
    return 0
  fi
  if [[ -n "$def" ]]; then
    COMP="$def"
    REMAINING=("$@")
    return 0
  fi
  # 允许无组件名、直接跟选项（如 build -L）
  COMP="${def:-all}"
  REMAINING=("$@")
}

CMD="${1:-help}"
shift || true

case "$CMD" in
  help|-h|--help) usage ;;

  run)
    via_script run.sh "$@"
    ;;

  build)
    via_script build.sh "$@"
    ;;

  package|pkg|deploy)
    via_script package.sh "$@"
    ;;

  publish_update|package_update|pub_update)
    via_script publish_update.sh "$@"
    ;;

  install)
    via_script install.sh "$@"
    ;;

  update)
    via_script update.sh "$@"
    ;;

  uninstall)
    via_script uninstall.sh "$@"
    ;;

  service)
    via_script service.sh "$@"
    ;;

  status)
    via_script status.sh "$@"
    ;;

  log)
    cmd_log "$@"
    ;;

  clean)
    via_script clean.sh "$@"
    ;;

  upload|up|\
  remote_install|remote_update|remote_status|\
  remote_service|remote_log|remote_ssh|remote_uninstall)
    via_script remote.sh "$CMD" "$@"
    ;;

  # 兼容旧独立安装脚本入口提示
  install-agent-linux|install-agent-macos)
    die "请改用: sudo ./deploy.sh install agent ...（旧脚本仍保留在 scripts/）"
    ;;

  *)
    die "未知命令: ${CMD}（./deploy.sh help）"
    ;;
esac
