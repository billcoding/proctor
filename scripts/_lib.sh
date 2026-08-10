#!/usr/bin/env bash
# Proctor 公共函数（由 deploy.sh / 子脚本 source）

: "${APP_NAME:=proctor}"
: "${INSTALL_PREFIX:=/opt/proctor}"
: "${SYSTEMD_DIR:=/etc/systemd/system}"
: "${DIST_DIR:=}"
: "${CGO_ENABLED:=0}"

log(){ echo "[$(date '+%H:%M:%S')] $*" >&2; }
die(){ log "ERROR: $*"; exit 1; }

is_dry_run(){ [[ "${DRY_RUN:-0}" == "1" || "${DRY_RUN:-}" == "true" ]]; }

run_cmd(){
  if is_dry_run; then
    log "[dry-run] $*"
    return 0
  fi
  "$@"
}

os_family(){
  case "$(uname -s 2>/dev/null || echo unknown)" in
    Linux*) echo linux ;;
    Darwin*) echo darwin ;;
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    *) echo other ;;
  esac
}

has_systemd(){
  [[ "$(os_family)" == "linux" ]] && command -v systemctl >/dev/null 2>&1
}

has_help_flag(){
  local a
  for a in "$@"; do
    case "$a" in help|-h|--help) return 0 ;; esac
  done
  return 1
}

_load_dotenv_file(){
  local envfile="$1"
  [[ -f "$envfile" ]] || return 0
  case " ${_WORKSPACE_DOTENV_LOADED_LIST:-} " in
    *" $envfile "*) return 0 ;;
  esac
  set -a
  # shellcheck disable=SC1090
  source "$envfile"
  set +a
  _WORKSPACE_DOTENV_LOADED_LIST="${_WORKSPACE_DOTENV_LOADED_LIST:-} $envfile"
  export _WORKSPACE_DOTENV_LOADED_LIST
}

load_workspace_dotenv(){
  local root="${1:-}"
  [[ -n "$root" ]] || root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  _load_dotenv_file "$root/.env"
}

# ---------- 组件 ----------
# COMP: server|agent|all
normalize_component(){
  case "${1:-}" in
    server|s|srv) echo server ;;
    agent|a|agt) echo agent ;;
    all) echo all ;;
    *) return 1 ;;
  esac
}

component_bin(){
  case "$1" in
    server) echo proctor-server ;;
    agent) echo proctor-agent ;;
    *) die "未知组件: $1" ;;
  esac
}

component_prefix(){
  local comp="$1"
  echo "${INSTALL_PREFIX}/${comp}"
}

component_unit(){
  case "$1" in
    server) echo proctor-server.service ;;
    agent) echo proctor-agent.service ;;
    *) die "未知组件: $1" ;;
  esac
}

# ---------- 构建平台 ----------
default_goarch_for(){
  local os="$1"
  if [[ -n "${GOARCH:-}" ]]; then
    printf '%s' "$GOARCH"
    return 0
  fi
  case "$os" in
    darwin)
      case "$(uname -m 2>/dev/null || true)" in
        arm64|aarch64) printf 'arm64' ;;
        *) printf 'amd64' ;;
      esac
      ;;
    *)
      printf 'amd64'
      ;;
  esac
}

# 解析后: BUILD_PLATFORMS / BUILD_REMAINING / BUILD_WANT_HELP
parse_build_platforms(){
  BUILD_PLATFORMS=()
  BUILD_REMAINING=()
  BUILD_WANT_HELP=0
  local want_darwin=0 want_linux=0 want_windows=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -D|--darwin)  want_darwin=1; shift ;;
      -L|--linux)   want_linux=1; shift ;;
      -W|--windows) want_windows=1; shift ;;
      -a|--all)
        want_darwin=1; want_linux=1; want_windows=1; shift ;;
      help|-h|--help) BUILD_WANT_HELP=1; shift ;;
      amd64|arm64|arm|386)
        export GOARCH="$1"
        BUILD_REMAINING+=("$1")
        shift
        ;;
      *)
        BUILD_REMAINING+=("$1")
        shift
        ;;
    esac
  done
  if (( want_darwin == 0 && want_linux == 0 && want_windows == 0 )); then
    return 0
  fi
  if (( want_darwin )); then
    BUILD_PLATFORMS+=("darwin:$(default_goarch_for darwin)")
  fi
  if (( want_linux )); then
    BUILD_PLATFORMS+=("linux:$(default_goarch_for linux)")
  fi
  if (( want_windows )); then
    BUILD_PLATFORMS+=("windows:$(default_goarch_for windows)")
  fi
  return 0
}

binary_name(){
  local base="$1" goos="$2" goarch="$3" with_platform="${4:-0}"
  local name="$base"
  if [[ "$with_platform" == "1" ]]; then
    name="${base}-${goos}-${goarch}"
  fi
  [[ "$goos" == "windows" ]] && name="${name}.exe"
  printf '%s' "$name"
}

go_ldflags(){
  local ver="${APP_VERSION:-$(date '+%Y-%m-%dT%H:%M:%S')}"
  printf '%s' "-s -w -X 'github.com/billcoding/proctor/internal/agent.Version=${ver}'"
}

need_root_or_writable(){
  local prefix="$1"
  if is_dry_run; then
    return 0
  fi
  local uid
  uid="$(id -u 2>/dev/null || echo unknown)"
  if [[ "$uid" == "0" ]]; then
    return 0
  fi
  if mkdir -p "$prefix" 2>/dev/null && [[ -w "$prefix" ]]; then
    return 0
  fi
  die "无法写入 ${prefix}。请 sudo 执行，或改 --prefix / DRY_RUN=1"
}

# ---------- SSH / 远程 ----------
# REMOTE_OS=windows|linux 可强制指定；未设置时 remote_os_family 会 SSH 探测
# REMOTE_PATH 默认按 OS：Linux=/tmp，Windows=C:/Windows/Temp
parse_remote_flags(){
  REMOTE_REMAIN=()
  : "${REMOTE_SSH_HOST:=}"
  : "${REMOTE_SSH_PASSWORD:=}"
  : "${REMOTE_SSH_KEY:=}"
  : "${REMOTE_SSH_PORT:=}"
  : "${REMOTE_OS:=}"
  REMOTE_PATH_SET=0
  if [[ -n "${REMOTE_PATH+x}" && -n "${REMOTE_PATH:-}" ]]; then
    REMOTE_PATH_SET=1
  else
    : "${REMOTE_PATH:=/tmp}"
  fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -H|--host)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_SSH_HOST="$2"; shift 2 ;;
      --host=*) REMOTE_SSH_HOST="${1#--host=}"; shift ;;
      -P|--password)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_SSH_PASSWORD="$2"; shift 2 ;;
      --password=*) REMOTE_SSH_PASSWORD="${1#--password=}"; shift ;;
      -k|--key)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_SSH_KEY="$2"; shift 2 ;;
      --key=*) REMOTE_SSH_KEY="${1#--key=}"; shift ;;
      -p|--port)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_SSH_PORT="$2"; shift 2 ;;
      --port=*) REMOTE_SSH_PORT="${1#--port=}"; shift ;;
      -I|--prefix)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        INSTALL_PREFIX="$2"; shift 2 ;;
      --prefix=*) INSTALL_PREFIX="${1#--prefix=}"; shift ;;
      -R|--remote-path)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_PATH="$2"; REMOTE_PATH_SET=1; shift 2 ;;
      --remote-path=*) REMOTE_PATH="${1#--remote-path=}"; REMOTE_PATH_SET=1; shift ;;
      --os)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        REMOTE_OS="$2"; shift 2 ;;
      --os=*) REMOTE_OS="${1#--os=}"; shift ;;
      -n|--dry-run) DRY_RUN=1; shift ;;
      -S|--skip-build) SKIP_BUILD=1; shift ;;
      -N|--no-start) NO_START=1; shift ;;
      --set)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("$2"); shift 2 ;;
      --server-url)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("server_url=$2"); shift 2 ;;
      --student)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("student_name=$2"); shift 2 ;;
      --classroom)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("classroom=$2"); shift 2 ;;
      --listen)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("listen=$2"); shift 2 ;;
      --admin-token)
        [[ $# -ge 2 ]] || die "缺少参数: $1"
        CONFIG_SETS+=("admin_token=$2"); shift 2 ;;
      *)
        # user@host 位置参数
        if [[ -z "$REMOTE_SSH_HOST" && "$1" == *@* && "$1" != *=* && "$1" != -* ]]; then
          REMOTE_SSH_HOST="$1"; shift
        else
          REMOTE_REMAIN+=("$1"); shift
        fi
        ;;
    esac
  done
}

# 归一化并缓存远端 OS：linux|windows
_REMOTE_OS_CACHE=""
normalize_remote_os(){
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    win|windows|win32|win64) echo windows ;;
    linux) echo linux ;;
    darwin|macos|mac) echo darwin ;;
    *) return 1 ;;
  esac
}

remote_os_family(){
  if [[ -n "${_REMOTE_OS_CACHE:-}" ]]; then
    printf '%s\n' "$_REMOTE_OS_CACHE"
    return 0
  fi
  if [[ -n "${REMOTE_OS:-}" ]]; then
    _REMOTE_OS_CACHE="$(normalize_remote_os "$REMOTE_OS")" \
      || die "未知 REMOTE_OS=$REMOTE_OS（支持 windows|linux）"
    printf '%s\n' "$_REMOTE_OS_CACHE"
    return 0
  fi
  require_remote_host
  if is_dry_run; then
    # dry-run 无法探测，默认按 linux（可用 REMOTE_OS=windows 覆盖）
    _REMOTE_OS_CACHE=linux
    printf '%s\n' "$_REMOTE_OS_CACHE"
    return 0
  fi

  local out=""
  # 先确认 SSH 可达
  local ssh_err=""
  if ! ssh_err="$(remote_ssh echo proctor-remote-ok 2>&1)"; then
    die "无法 SSH 到 ${REMOTE_SSH_HOST}（检查网络/账号/密钥；Windows 需启用 OpenSSH Server）。${ssh_err}"
  fi

  # Probe 1: uname（Linux / 部分 Git Bash）
  out="$(remote_ssh sh -c 'uname -s 2>/dev/null' 2>/dev/null || true)"
  out="$(printf '%s' "$out" | tr -d '\r' | head -n1)"
  case "$out" in
    Linux*) _REMOTE_OS_CACHE=linux; printf '%s\n' linux; return 0 ;;
    Darwin*) _REMOTE_OS_CACHE=darwin; printf '%s\n' darwin; return 0 ;;
    MINGW*|MSYS*|CYGWIN*) _REMOTE_OS_CACHE=windows; printf '%s\n' windows; return 0 ;;
  esac

  # Probe 2: Windows cmd（需远端已开 OpenSSH Server）
  out="$(remote_ssh cmd.exe /c "echo %OS%" 2>/dev/null || true)"
  out="$(printf '%s' "$out" | tr -d '\r' | head -n1)"
  if [[ "$out" == *Windows* ]]; then
    _REMOTE_OS_CACHE=windows
    printf '%s\n' windows
    return 0
  fi

  # Probe 3: PowerShell
  out="$(remote_ssh powershell.exe -NoProfile -Command '$env:OS' 2>/dev/null || true)"
  out="$(printf '%s' "$out" | tr -d '\r' | head -n1)"
  if [[ "$out" == *Windows* ]]; then
    _REMOTE_OS_CACHE=windows
    printf '%s\n' windows
    return 0
  fi

  die "无法探测远端 OS。请确认 Windows 已启用 OpenSSH Server，或设置 REMOTE_OS=windows|linux / --os windows"
}

# 按远端 OS 校正默认临时目录（用户显式 -R/--remote-path 时不改）
ensure_remote_path_for_os(){
  local os
  os="$(remote_os_family)"
  if [[ "${REMOTE_PATH_SET:-0}" != "1" ]]; then
    case "$os" in
      windows) REMOTE_PATH="C:/Windows/Temp" ;;
      *) REMOTE_PATH="${REMOTE_PATH:-/tmp}" ;;
    esac
  fi
}

# 在远端执行 PowerShell（Windows OpenSSH）
remote_powershell(){
  require_remote_host
  if is_dry_run; then
    log "[dry-run] ssh ${REMOTE_SSH_HOST} powershell.exe $*"
    return 0
  fi
  remote_ssh powershell.exe -NoProfile -ExecutionPolicy Bypass "$@"
}

# 从 CONFIG_SETS 取 key（无则空）
config_set_get(){
  local key="$1" s
  for s in "${CONFIG_SETS[@]+"${CONFIG_SETS[@]}"}"; do
    if [[ "$s" == "$key="* ]]; then
      printf '%s\n' "${s#*=}"
      return 0
    fi
  done
  return 0
}

# Windows agent 安装路径约定（与 install-agent-windows.ps1 一致）
windows_agent_install_dir(){ printf '%s\n' 'C:/Program Files/proctor'; }
windows_agent_bin_remote(){ printf '%s\n' 'C:/Program Files/proctor/proctor-agent.exe'; }
windows_agent_conf_remote(){ printf '%s\n' 'C:/ProgramData/proctor/agent.json'; }

# 确保本地有 windows/amd64 agent 二进制
ensure_windows_agent_bin(){
  local root="${1:-}"
  [[ -n "$root" ]] || root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  local out="$root/dist/proctor-agent-windows-amd64.exe"
  if [[ ! -f "$out" ]]; then
    if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
      die "缺少 $out（先 ./deploy.sh build agent -W，或去掉 -S）"
    fi
    log "缺少 Windows agent，先 build agent -W"
    bash "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/build.sh" agent -W
  fi
  [[ -f "$out" ]] || die "构建后仍缺少: $out"
  printf '%s\n' "$out"
}

require_remote_host(){
  [[ -n "${REMOTE_SSH_HOST:-}" ]] || die "请指定远程主机: -H user@host 或 REMOTE_SSH_HOST / .env"
}

_build_ssh_base(){
  SSH_CMD=(ssh -o StrictHostKeyChecking=accept-new -o ServerAliveInterval=30)
  SCP_CMD=(scp -o StrictHostKeyChecking=accept-new)
  if [[ -n "${REMOTE_SSH_PORT:-}" ]]; then
    SSH_CMD+=(-p "$REMOTE_SSH_PORT")
    SCP_CMD+=(-P "$REMOTE_SSH_PORT")
  fi
  if [[ -n "${REMOTE_SSH_KEY:-}" ]]; then
    [[ -f "$REMOTE_SSH_KEY" ]] || die "SSH 密钥不存在: $REMOTE_SSH_KEY"
    SSH_CMD+=(-i "$REMOTE_SSH_KEY")
    SCP_CMD+=(-i "$REMOTE_SSH_KEY")
  fi
}

_with_sshpass(){
  if [[ -z "${REMOTE_SSH_PASSWORD:-}" ]]; then
    "$@"
    return
  fi
  if command -v sshpass >/dev/null 2>&1; then
    SSHPASS="$REMOTE_SSH_PASSWORD" sshpass -e "$@"
  else
    die "密码登录需要 sshpass（或改用 -k/--key）"
  fi
}

remote_ssh(){
  require_remote_host
  _build_ssh_base
  if is_dry_run; then
    log "[dry-run] ssh ${REMOTE_SSH_HOST} $*"
    return 0
  fi
  _with_sshpass "${SSH_CMD[@]}" "$REMOTE_SSH_HOST" "$@"
}

remote_scp(){
  require_remote_host
  _build_ssh_base
  local src="$1" dst="$2"
  if is_dry_run; then
    log "[dry-run] scp $src ${REMOTE_SSH_HOST}:$dst"
    return 0
  fi
  _with_sshpass "${SCP_CMD[@]}" "$src" "${REMOTE_SSH_HOST}:$dst"
}

# JSON 配置键值写入（简单替换/追加，适配 agent.json / server.json）
json_set_file(){
  local file="$1"
  shift
  local pairs=("$@")
  [[ -f "$file" ]] || die "配置不存在: $file"
  command -v python3 >/dev/null 2>&1 || die "需要 python3 以写入 JSON 配置"
  python3 - "$file" "${pairs[@]}" <<'PY'
import json, sys
path = sys.argv[1]
pairs = sys.argv[2:]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
for p in pairs:
    if "=" not in p:
        raise SystemExit(f"invalid set: {p}")
    k, v = p.split("=", 1)
    # bool/number heuristic
    if v.lower() in ("true", "false"):
        val = v.lower() == "true"
    else:
        try:
            if "." in v:
                val = float(v)
            else:
                val = int(v)
        except ValueError:
            val = v
    # nested via dotted key (one level enough for proctor)
    if "." in k:
        a, b = k.split(".", 1)
        if not isinstance(data.get(a), dict):
            data[a] = {}
        data[a][b] = val
    else:
        data[k] = val
with open(path, "w", encoding="utf-8") as f:
    json.dump(data, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY
}
