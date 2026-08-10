#!/usr/bin/env bash
# 远程上传 / 安装 / 更新 / 状态 / 服务 / 日志 / SSH
# Linux：SSH + tar + systemd（原有路径）
# Windows：需远端启用 OpenSSH Server，走 PowerShell + install-agent-windows.ps1（仅 agent）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

ACTION="${1:-}"
shift || true

usage(){
  cat <<EOF
remote_* / upload

用法:
 ./deploy.sh upload [server|agent|all] [远程选项]
 ./deploy.sh remote_install [server|agent|all] [远程选项] [配置选项]
 ./deploy.sh remote_update  [server|agent|all] [远程选项]
 ./deploy.sh remote_status  [server|agent|all] [远程选项]
 ./deploy.sh remote_service [server|agent|all] [--start|--stop|--restart]
 ./deploy.sh remote_log     [server|agent] [-f] [-n N]
 ./deploy.sh remote_uninstall [server|agent|all] [-X]
 ./deploy.sh remote_ssh     [user@host]

远程选项:
 -H,--host <user@host>   或位置参数 / REMOTE_SSH_HOST
 -P,--password <pass>    需 sshpass
 -k,--key <FILE>
 -p,--port <N>
 -I,--prefix <DIR>       Linux 安装前缀
 -R,--remote-path <DIR>  远端临时目录（Linux 默认 /tmp；Windows 默认 C:/Windows/Temp）
 --os windows|linux      强制远端 OS（也可 REMOTE_OS=；默认自动探测）
 -S,--skip-build
 -N,--no-start
 -n,--dry-run
 --server-url / --student / --classroom / --listen / --admin-token / --set

说明:
 Linux：假定 systemd，上传 tar.gz 后远端安装
 Windows：需 OpenSSH Server；仅支持 agent；上传 .exe + install-agent-windows.ps1
 RDP 仅适合人工登录辅助，不作为 remote_install 自动化入口
EOF
}

[[ -n "$ACTION" ]] || { usage; exit 2; }
case "$ACTION" in
  help|-h|--help) usage; exit 0 ;;
esac

NO_START=0
SKIP_BUILD=0
CONFIG_SETS=()
parse_remote_flags "$@"
set -- "${REMOTE_REMAIN[@]+"${REMOTE_REMAIN[@]}"}"

COMP="all"
SVC_ACTION=""
LOG_FOLLOW=0
LOG_LINES=100
PURGE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    --start|--stop|--restart|--status) SVC_ACTION="${1#--}"; shift ;;
    start|stop|restart|status) SVC_ACTION="$1"; shift ;;
    -f|--follow) LOG_FOLLOW=1; shift ;;
    -n|--lines) LOG_LINES="$2"; shift 2 ;;
    -X|--purge) PURGE=1; shift ;;
    server|agent|all|s|a|srv|agt) COMP="$(normalize_component "$1")"; shift ;;
    *)
      if [[ -z "${REMOTE_SSH_HOST:-}" && "$1" == *@* ]]; then
        REMOTE_SSH_HOST="$1"; shift
      else
        die "未知参数: $1"
      fi
      ;;
  esac
done

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  *) comps=("$COMP") ;;
esac

# 解析主机后立刻探测 OS 并校正临时目录（多数子命令需要）
init_remote_target(){
  require_remote_host
  local os
  os="$(remote_os_family)"
  ensure_remote_path_for_os
  # /tmp 在原生 Windows OpenSSH 上通常不可用
  if [[ "$os" == "windows" && ( "$REMOTE_PATH" == "/tmp" || "$REMOTE_PATH" == "/tmp/" ) ]]; then
    log "WARN: Windows 上 REMOTE_PATH=/tmp 不可用，改用 C:/Windows/Temp"
    REMOTE_PATH="C:/Windows/Temp"
  fi
  log "remote OS=$os host=${REMOTE_SSH_HOST} tmp=${REMOTE_PATH}"
}

require_windows_agent_only(){
  local c
  for c in "${comps[@]}"; do
    [[ "$c" == "agent" ]] || die "Windows 远程目前仅支持 agent（不要带 server/all）。当前组件: $c"
  done
}

ensure_local_pkg(){
  local comp="$1"
  local tar="$ROOT/dist/proctor-${comp}.tar.gz"
  if [[ ! -f "$tar" ]]; then
    local args=("$comp")
    [[ "$SKIP_BUILD" == "1" ]] && args+=(-S)
    bash "$SCRIPT_DIR/package.sh" "${args[@]}"
  fi
  echo "$tar"
}

# ---------- Linux ----------
cmd_upload_linux(){
  for comp in "${comps[@]}"; do
    local tar rpath
    tar="$(ensure_local_pkg "$comp")"
    rpath="${REMOTE_PATH%/}/$(basename "$tar")"
    log "upload $tar → ${REMOTE_SSH_HOST}:$rpath"
    remote_scp "$tar" "$rpath"
  done
}

cmd_remote_install_linux(){
  cmd_upload_linux
  for comp in "${comps[@]}"; do
    local rpath prefix
    rpath="${REMOTE_PATH%/}/proctor-${comp}.tar.gz"
    prefix="$(component_prefix "$comp")"
    log "remote_install $comp @ $prefix"
    remote_ssh bash -s -- "$comp" "$rpath" "$prefix" "$NO_START" "${CONFIG_SETS[*]-}" <<'EOS'
set -euo pipefail
comp="$1"; rpath="$2"; prefix="$3"; no_start="$4"; sets="${5:-}"
unit="proctor-${comp}.service"
bin="proctor-${comp}"
cfg="$([ "$comp" = server ] && echo server.json || echo agent.json)"
stage="$(mktemp -d /tmp/proctor-rinst.XXXXXX)"
tar -C "$stage" -xzf "$rpath"
mkdir -p "$prefix"
cp "$stage/$bin" "$prefix/$bin"
chmod +x "$prefix/$bin"
[[ -f "$prefix/$cfg" ]] || cp "$stage/$cfg" "$prefix/$cfg"
if [[ "$comp" == "server" && -d "$stage/web" ]]; then
  mkdir -p "$prefix/web"
  cp -R "$stage/web/." "$prefix/web/"
fi
if [[ -n "$sets" ]]; then
  python3 - "$prefix/$cfg" $sets <<'PY'
import json,sys
path=sys.argv[1]
data=json.load(open(path,encoding='utf-8'))
for p in sys.argv[2:]:
  k,v=p.split('=',1)
  if v.lower() in ('true','false'): val=v.lower()=='true'
  else:
    try: val=int(v)
    except: val=v
  data[k]=val
json.dump(data, open(path,'w',encoding='utf-8'), ensure_ascii=False, indent=2)
open(path,'a').write('\n')
PY
fi
body="$(sed "s|/opt/proctor/${comp}|${prefix}|g" "$stage/$unit")"
printf '%s\n' "$body" >"/etc/systemd/system/$unit"
systemctl daemon-reload
systemctl enable "$unit"
if [[ "$no_start" != "1" ]]; then
  systemctl restart "$unit"
fi
rm -rf "$stage"
echo "remote installed $comp -> $prefix"
EOS
  done
}

cmd_remote_update_linux(){
  cmd_upload_linux
  for comp in "${comps[@]}"; do
    local rpath prefix
    rpath="${REMOTE_PATH%/}/proctor-${comp}.tar.gz"
    prefix="$(component_prefix "$comp")"
    log "remote_update $comp"
    remote_ssh bash -s -- "$comp" "$rpath" "$prefix" <<'EOS'
set -euo pipefail
comp="$1"; rpath="$2"; prefix="$3"
unit="proctor-${comp}.service"
bin="proctor-${comp}"
stage="$(mktemp -d /tmp/proctor-rupd.XXXXXX)"
tar -C "$stage" -xzf "$rpath"
cp "$stage/$bin" "$prefix/$bin"
chmod +x "$prefix/$bin"
systemctl restart "$unit"
rm -rf "$stage"
echo "remote updated $comp"
EOS
  done
}

cmd_remote_status_linux(){
  for comp in "${comps[@]}"; do
    local prefix unit
    prefix="$(component_prefix "$comp")"
    unit="$(component_unit "$comp")"
    remote_ssh bash -s -- "$comp" "$prefix" "$unit" <<'EOS'
comp="$1"; prefix="$2"; unit="$3"
echo "== $comp =="
echo "prefix: $prefix"
ls -la "$prefix" 2>/dev/null || echo "(missing)"
systemctl --no-pager --full status "$unit" 2>/dev/null || true
EOS
  done
}

cmd_remote_service_linux(){
  SVC_ACTION="${SVC_ACTION:-status}"
  for comp in "${comps[@]}"; do
    local unit
    unit="$(component_unit "$comp")"
    if [[ "$SVC_ACTION" == "status" ]]; then
      remote_ssh systemctl --no-pager --full status "$unit" || true
    else
      remote_ssh systemctl "$SVC_ACTION" "$unit"
    fi
  done
}

cmd_remote_log_linux(){
  [[ "$COMP" != "all" ]] || COMP=server
  local unit
  unit="$(component_unit "$COMP")"
  local args=(-u "$unit" --no-pager -n "$LOG_LINES")
  [[ "$LOG_FOLLOW" == "1" ]] && args+=(-f)
  remote_ssh journalctl "${args[@]}"
}

cmd_remote_uninstall_linux(){
  for comp in "${comps[@]}"; do
    local prefix unit
    prefix="$(component_prefix "$comp")"
    unit="$(component_unit "$comp")"
    remote_ssh bash -s -- "$prefix" "$unit" "$PURGE" <<'EOS'
prefix="$1"; unit="$2"; purge="$3"
systemctl stop "$unit" 2>/dev/null || true
systemctl disable "$unit" 2>/dev/null || true
rm -f "/etc/systemd/system/$unit"
systemctl daemon-reload 2>/dev/null || true
if [[ "$purge" == "1" ]]; then rm -rf "$prefix"; else rm -f "$prefix"/proctor-*; fi
echo "remote uninstalled"
EOS
  done
}

# ---------- Windows (OpenSSH + PowerShell) ----------
cmd_upload_windows(){
  require_windows_agent_only
  local bin rpath ps1 rps1
  bin="$(ensure_windows_agent_bin "$ROOT")"
  rpath="${REMOTE_PATH%/}/proctor-agent.exe"
  ps1="$SCRIPT_DIR/install-agent-windows.ps1"
  rps1="${REMOTE_PATH%/}/install-agent-windows.ps1"
  log "upload $bin → ${REMOTE_SSH_HOST}:$rpath"
  remote_scp "$bin" "$rpath"
  log "upload $ps1 → ${REMOTE_SSH_HOST}:$rps1"
  remote_scp "$ps1" "$rps1"
}

cmd_remote_install_windows(){
  require_windows_agent_only
  cmd_upload_windows
  local rpath rps1 server_url student classroom
  rpath="${REMOTE_PATH%/}/proctor-agent.exe"
  rps1="${REMOTE_PATH%/}/install-agent-windows.ps1"
  server_url="$(config_set_get server_url)"
  student="$(config_set_get student_name)"
  classroom="$(config_set_get classroom)"
  : "${server_url:=http://127.0.0.1:8080}"
  log "remote_install agent (Windows Service) server_url=$server_url"
  # 路径用正斜杠，OpenSSH/PowerShell 均可识别；中文参数走 -File 参数传递
  local args=(
    -File "$rps1"
    -BinPath "$rpath"
    -ServerUrl "$server_url"
    -Student "$student"
    -Classroom "$classroom"
  )
  [[ "$NO_START" == "1" ]] && args+=(-NoStart)
  remote_powershell "${args[@]}"
}

cmd_remote_update_windows(){
  require_windows_agent_only
  local bin rpath dest
  bin="$(ensure_windows_agent_bin "$ROOT")"
  rpath="${REMOTE_PATH%/}/proctor-agent.exe"
  dest="$(windows_agent_bin_remote)"
  log "upload $bin → ${REMOTE_SSH_HOST}:$rpath"
  remote_scp "$bin" "$rpath"
  log "remote_update agent (Windows)"
  remote_powershell -Command \
    "& { \$ErrorActionPreference='Stop'; \$src='$rpath'; \$dst='$dest'; if (Test-Path -LiteralPath \$dst) { try { & \$dst stop } catch {} }; New-Item -ItemType Directory -Force -Path (Split-Path \$dst) | Out-Null; Copy-Item -Force \$src \$dst; & \$dst start; & \$dst status }"
}

cmd_remote_status_windows(){
  require_windows_agent_only
  local dest
  dest="$(windows_agent_bin_remote)"
  remote_powershell -Command \
    "& { Write-Host '== agent =='; Write-Host \"bin: $dest\"; if (Test-Path -LiteralPath '$dest') { & '$dest' status } else { Write-Host '(missing)' }; Get-Service proctor-agent -ErrorAction SilentlyContinue | Format-List * }"
}

cmd_remote_service_windows(){
  require_windows_agent_only
  SVC_ACTION="${SVC_ACTION:-status}"
  local dest
  dest="$(windows_agent_bin_remote)"
  case "$SVC_ACTION" in
    start|stop|restart|status)
      remote_powershell -Command \
        "& { \$ErrorActionPreference='Stop'; if (-not (Test-Path -LiteralPath '$dest')) { throw 'agent not installed: $dest' }; & '$dest' $SVC_ACTION }"
      ;;
    *) die "未知服务动作: $SVC_ACTION" ;;
  esac
}

cmd_remote_log_windows(){
  die "remote_log 暂不支持 Windows（可用 remote_status / RDP 查事件查看器，或配置 agent.json log_file）"
}

cmd_remote_uninstall_windows(){
  require_windows_agent_only
  local dest purge_flag="$PURGE"
  dest="$(windows_agent_bin_remote)"
  remote_powershell -Command \
    "& { \$ErrorActionPreference='Continue'; \$bin='$dest'; if (Test-Path -LiteralPath \$bin) { try { & \$bin stop } catch {}; try { & \$bin uninstall } catch {} }; if ('$purge_flag' -eq '1') { Remove-Item -Recurse -Force 'C:\\Program Files\\proctor','C:\\ProgramData\\proctor' -ErrorAction SilentlyContinue }; Write-Host 'remote uninstalled' }"
}

# ---------- 分发 ----------
cmd_upload(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_upload_windows ;;
    linux) cmd_upload_linux ;;
    *) die "remote upload 不支持 OS=$(remote_os_family)（请用 Linux 或 Windows+OpenSSH）" ;;
  esac
}

cmd_remote_install(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_install_windows ;;
    linux) cmd_remote_install_linux ;;
    *) die "remote_install 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_update(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_update_windows ;;
    linux) cmd_remote_update_linux ;;
    *) die "remote_update 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_status(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_status_windows ;;
    linux) cmd_remote_status_linux ;;
    *) die "remote_status 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_service(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_service_windows ;;
    linux) cmd_remote_service_linux ;;
    *) die "remote_service 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_log(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_log_windows ;;
    linux) cmd_remote_log_linux ;;
    *) die "remote_log 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_uninstall(){
  init_remote_target
  case "$(remote_os_family)" in
    windows) cmd_remote_uninstall_windows ;;
    linux) cmd_remote_uninstall_linux ;;
    *) die "remote_uninstall 不支持 OS=$(remote_os_family)" ;;
  esac
}

cmd_remote_ssh(){
  require_remote_host
  _build_ssh_base
  if [[ -n "${REMOTE_SSH_PASSWORD:-}" ]]; then
    _with_sshpass "${SSH_CMD[@]}" -t "$REMOTE_SSH_HOST"
  else
    exec "${SSH_CMD[@]}" -t "$REMOTE_SSH_HOST"
  fi
}

case "$ACTION" in
  upload|up) cmd_upload ;;
  remote_install) cmd_remote_install ;;
  remote_update) cmd_remote_update ;;
  remote_status) cmd_remote_status ;;
  remote_service) cmd_remote_service ;;
  remote_log) cmd_remote_log ;;
  remote_uninstall) cmd_remote_uninstall ;;
  remote_ssh) cmd_remote_ssh ;;
  *) usage; die "未知 remote 动作: $ACTION" ;;
esac
